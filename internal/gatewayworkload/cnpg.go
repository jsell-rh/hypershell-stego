package gatewayworkload

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/databasecontroller"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
	dbclient "github.com/jsell-rh/hypershell-stego/out/postgres"
)

const sharedAPI = "/apis/postgresql.cnpg.io/v1/namespaces/"
const providerLabel = "hypershell.redhat.io/database-provider"
const maxManagedRoles = 1024

func validateCNPGDialAddress(address string) error { return dbclient.ValidateDialAddress(address) }
func sqlName(name string) string                   { return strings.Replace(name, "gw-", "gw_", 1) }
func cnpgHost(ns string) string {
	return databasecontroller.CNPGClusterName + "-rw." + ns + ".svc.cluster.local"
}
func (k *Kubernetes) sharedCluster(ctx context.Context, ns, dbID string) (object, error) {
	cluster, code, err := k.client.Request(ctx, http.MethodGet, sharedAPI+ns+"/clusters/"+databasecontroller.CNPGClusterName, nil)
	if err != nil {
		return nil, err
	}
	if code == 404 {
		return nil, ErrPending
	}
	if !keyResourceOwned(cluster, databaseOwner(dbID)) {
		return nil, errors.New("Gateway CNPG Cluster is not owned by its database")
	}
	return cluster, nil
}
func (k *Kubernetes) cnpgSecret(ctx context.Context, ns, name string, cluster object) (object, error) {
	secret, code, err := k.client.Request(ctx, http.MethodGet, "/api/v1/namespaces/"+ns+"/secrets/"+name, nil)
	if err != nil {
		return nil, err
	}
	if code == 404 {
		return nil, ErrPending
	}
	if kube.String(secret, "metadata", "uid") == "" || kube.String(secret, "metadata", "resourceVersion") == "" || kube.String(secret, "metadata", "deletionTimestamp") != "" {
		return nil, errors.New("CNPG Secret has no current identity")
	}
	refs, _ := kube.Nested(secret, "metadata", "ownerReferences").([]any)
	for _, item := range refs {
		ref, _ := item.(map[string]any)
		if kube.String(ref, "apiVersion") == "postgresql.cnpg.io/v1" && kube.String(ref, "kind") == "Cluster" && kube.String(ref, "name") == databasecontroller.CNPGClusterName && kube.String(ref, "uid") == kube.String(cluster, "metadata", "uid") {
			return secret, nil
		}
	}
	return nil, errors.New("CNPG Secret has a different Cluster owner")
}
func (k *Kubernetes) cnpgCA(ctx context.Context, ns string, cluster object) ([]byte, error) {
	secret, err := k.cnpgSecret(ctx, ns, databasecontroller.CNPGClusterName+"-ca", cluster)
	if err != nil {
		return nil, err
	}
	ca, err := data(secret, "ca.crt")
	if err != nil {
		return nil, err
	}
	return certificateBundle(ca)
}
func desiredRole(name, secret, id string, absent bool) object {
	if absent {
		return object{"name": name, "ensure": "absent", "comment": "hypershell Gateway " + id, "connectionLimit": -1, "inherit": true}
	}
	return object{"name": name, "ensure": "present", "comment": "hypershell Gateway " + id, "login": true, "superuser": false, "createdb": false, "createrole": false, "replication": false, "bypassrls": false, "inherit": false, "connectionLimit": 32, "inRoles": []any{}, "passwordSecret": object{"name": secret}}
}

// Preserve every other role. The version belongs to the read used to build the
// list. A conflict requires a new observation; it must not rebase an old list.
func (k *Kubernetes) setSharedRole(ctx context.Context, ns, dbID string, cluster object, desired object) (object, error) {
	raw := kube.Nested(cluster, "spec", "managed", "roles")
	roles, ok := raw.([]any)
	if raw != nil && !ok {
		return nil, errors.New("CNPG managed roles are invalid")
	}
	if len(roles) > maxManagedRoles {
		return nil, errors.New("CNPG managed role limit exceeded")
	}
	next := make([]any, 0, len(roles)+1)
	seen := map[string]bool{}
	found := false
	for _, item := range roles {
		role, ok := item.(map[string]any)
		name := kube.String(role, "name")
		if !ok || name == "" || seen[name] {
			return nil, errors.New("CNPG managed roles have invalid identities")
		}
		seen[name] = true
		if name == desired["name"] {
			if kube.String(role, "comment") != desired["comment"] {
				return nil, errors.New("CNPG role has a different owner")
			}
			found = true
			next = append(next, desired)
		} else {
			next = append(next, role)
		}
	}
	if !found {
		if len(roles) >= maxManagedRoles {
			return nil, errors.New("CNPG managed role limit exceeded")
		}
		next = append(next, desired)
	}
	// CNPG defaults can add fields. Compare the fields we own, and remove extra
	// role settings by replacing this role when they differ from our contract.
	encodedOld, _ := json.Marshal(roles)
	encodedNext, _ := json.Marshal(next)
	if reflect.DeepEqual(encodedOld, encodedNext) {
		return cluster, nil
	}
	return k.client.PatchOwned(ctx, sharedAPI+ns+"/clusters/"+databasecontroller.CNPGClusterName, cluster, object{"spec": object{"managed": object{"roles": next}}}, databaseOwner(dbID))
}
func (k *Kubernetes) cnpgCredentials(ctx context.Context, gw *pb.Gateway, db *pb.ManagedDatabase) (object, error) {
	name, err := sharedResourceName(gw.Metadata.Id)
	if err != nil {
		return nil, err
	}
	ns, id, dbID := db.Namespace, gw.Metadata.Id, db.Metadata.Id
	if err = k.client.RequireResources(ctx, "postgresql.cnpg.io/v1", kube.APIResource{Name: "clusters", Kind: "Cluster", Namespaced: true, Verbs: []string{"get", "patch"}}, kube.APIResource{Name: "databases", Kind: "Database", Namespaced: true, Verbs: []string{"get", "create", "patch", "delete"}}); err != nil {
		return nil, err
	}
	cluster, err := k.sharedCluster(ctx, ns, dbID)
	if err != nil {
		return nil, err
	}
	core := "/api/v1/namespaces/" + ns
	secret, code, err := k.client.Request(ctx, http.MethodGet, core+"/secrets/"+name+"-credentials", nil)
	if err != nil {
		return nil, err
	}
	if code == 404 {
		// Do not replace credentials for a database that can contain encrypted data.
		_, dbCode, err := k.client.Request(ctx, http.MethodGet, sharedAPI+ns+"/databases/"+name, nil)
		if err != nil {
			return nil, err
		}
		if dbCode != 404 {
			return nil, errors.New("CNPG Gateway credentials are missing; restore the original Secret")
		}
		absent, err := k.sqlGatewayAbsent(ctx, ns, cluster, sqlName(name))
		if err != nil {
			return nil, err
		}
		if !absent {
			return nil, errors.New("CNPG Gateway credentials are missing for existing SQL state; restore the original Secret")
		}
		var password [32]byte
		if _, err = rand.Read(password[:]); err != nil {
			return nil, err
		}
		secret = sharedDefinition("Secret", name+"-credentials", id, dbID)
		secret["metadata"].(object)["labels"].(object)["cnpg.io/reload"] = "true"
		secret["type"] = "kubernetes.io/basic-auth"
		secret["immutable"] = true
		secret["data"] = object{"username": base64.StdEncoding.EncodeToString([]byte(sqlName(name))), "password": base64.StdEncoding.EncodeToString([]byte(hex.EncodeToString(password[:])))}
		secret, _, err = k.client.Request(ctx, http.MethodPost, core+"/secrets", secret)
		if err != nil {
			return nil, err
		}
	}
	if !keyResourceOwned(secret, sharedOwner(id, dbID)) || secret["immutable"] != true {
		return nil, errors.New("CNPG Gateway credentials have a different owner or are not immutable")
	}
	if kube.String(secret, "metadata", "labels", "cnpg.io/reload") != "true" {
		watched := sharedDefinition("Secret", name+"-credentials", id, dbID)
		watched["metadata"].(object)["labels"].(object)["cnpg.io/reload"] = "true"
		secret, err = k.client.Ensure(ctx, core+"/secrets", watched, sharedOwner(id, dbID))
		if err != nil {
			return nil, err
		}
		if !keyResourceOwned(secret, sharedOwner(id, dbID)) || secret["immutable"] != true {
			return nil, errors.New("CNPG credentials changed during watch repair")
		}
	}
	user, err := data(secret, "username")
	if err != nil || string(user) != sqlName(name) {
		return nil, errors.New("CNPG Gateway SQL role does not match its ID")
	}
	password, err := data(secret, "password")
	if err != nil {
		return nil, err
	}
	if _, err = hex.DecodeString(string(password)); err != nil || len(password) != 64 {
		return nil, errors.New("CNPG Gateway password is invalid")
	}
	cluster, err = k.setSharedRole(ctx, ns, dbID, cluster, desiredRole(sqlName(name), name+"-credentials", id, false))
	if err != nil {
		return nil, err
	}
	database := sharedDefinition("Database", name, id, dbID)
	database["apiVersion"] = "postgresql.cnpg.io/v1"
	database["spec"] = object{"cluster": object{"name": databasecontroller.CNPGClusterName}, "name": sqlName(name), "owner": sqlName(name), "databaseReclaimPolicy": "delete", "connectionLimit": 32}
	database, err = k.client.Ensure(ctx, sharedAPI+ns+"/databases", database, sharedOwner(id, dbID))
	if err != nil {
		return nil, err
	}
	generation := cnpgInteger(database, "metadata", "generation")
	if generation < 1 || cnpgInteger(database, "status", "observedGeneration") != generation || kube.Nested(database, "status", "applied") != true {
		return nil, ErrPending
	}
	reconciled, _ := kube.Nested(cluster, "status", "managedRolesStatus", "byStatus", "reconciled").([]any)
	ready := false
	for _, role := range reconciled {
		if role == sqlName(name) {
			ready = true
		}
	}
	if !ready || kube.String(cluster, "status", "managedRolesStatus", "passwordStatus", sqlName(name), "resourceVersion") != kube.String(secret, "metadata", "resourceVersion") {
		return nil, ErrPending
	}
	ca, err := k.cnpgCA(ctx, ns, cluster)
	if err != nil {
		return nil, err
	}
	roleReady, err := k.cnpgRoleReady(ctx, ns, sqlName(name), string(password), ca)
	if err != nil {
		return nil, err
	}
	if !roleReady {
		if err := k.requestCNPGRepair(ctx, ns, dbID); err != nil {
			return nil, err
		}
		return nil, ErrPending
	}
	address := url.URL{Scheme: "postgresql", User: url.UserPassword(sqlName(name), string(password)), Host: cnpgHost(ns) + ":5432", Path: "/" + sqlName(name)}
	address.RawQuery = url.Values{"sslmode": {"verify-full"}, "sslrootcert": {"/etc/openshell-db/ca.crt"}}.Encode()
	return object{"uri": base64.StdEncoding.EncodeToString([]byte(address.String())), "ca.crt": base64.StdEncoding.EncodeToString(ca)}, nil
}

func (k *Kubernetes) deleteSharedDatabase(ctx context.Context, gw *pb.Gateway) error {
	name, err := sharedResourceName(gw.GetMetadata().GetId())
	if err != nil {
		return err
	}
	ns, err := gateways.DatabaseNamespace(gw.GetDatabaseId())
	if err != nil {
		return err
	}
	namespace, code, err := k.client.Request(ctx, http.MethodGet, "/api/v1/namespaces/"+ns, nil)
	if err != nil {
		return err
	}
	if code == 404 {
		return nil
	}
	if !keyResourceOwned(namespace, databaseOwner(gw.DatabaseId)) {
		return errors.New("Gateway database namespace has a different owner")
	}
	if k.allocation != nil && kube.String(namespace, "metadata", "labels", "stego.dev/allocation-profile") == "database" {
		_, err := k.allocation.NamespaceGone(ctx, "database", ns, gw.DatabaseId)
		return err
	}
	switch kube.String(namespace, "metadata", "labels", providerLabel) {
	case gateways.ProviderDeployment:
		// A changed routing label must not hide a live shared Cluster.
		_, clusterCode, err := k.client.Request(ctx, http.MethodGet, sharedAPI+ns+"/clusters/"+databasecontroller.CNPGClusterName, nil)
		if err != nil {
			return err
		}
		if clusterCode != 404 {
			return errors.New("Gateway database provider identity conflicts with a CNPG Cluster")
		}
		return nil
	case gateways.ProviderCNPG:
	default:
		return errors.New("Gateway database namespace has no valid provider identity")
	}
	cluster, err := k.sharedCluster(ctx, ns, gw.DatabaseId)
	if err != nil {
		return err
	}
	own := sharedOwner(gw.Metadata.Id, gw.DatabaseId)
	gone, err := k.client.DeleteOwned(ctx, sharedAPI+ns+"/databases/"+name, own)
	if err != nil {
		return err
	}
	if !gone {
		return ErrPending
	}
	cluster, err = k.setSharedRole(ctx, ns, gw.DatabaseId, cluster, desiredRole(sqlName(name), "", gw.Metadata.Id, true))
	if err != nil {
		return err
	}
	absent, err := k.sqlGatewayAbsent(ctx, ns, cluster, sqlName(name))
	if err != nil {
		return err
	}
	if !absent {
		if err := k.requestCNPGRepair(ctx, ns, gw.DatabaseId); err != nil {
			return err
		}
		return ErrPending
	}
	// Keep the absent role declaration. It repairs a late SQL role recreation.
	// Its explicit list limit fails closed until an archive policy is defined.
	core := "/api/v1/namespaces/" + ns
	for _, path := range []string{core + "/secrets/" + name + "-credentials", core + "/secrets/" + name + "-keys", core + "/configmaps/" + name + "-key-identity"} {
		gone, err = k.client.DeleteOwned(ctx, path, own)
		if err != nil {
			return err
		}
		if !gone {
			return ErrPending
		}
	}
	return nil
}

// Use the bootstrap application role for a bound, read-only catalog query.
// An optional IP route changes the socket destination, never the TLS identity.
func (k *Kubernetes) sqlGatewayAbsent(ctx context.Context, ns string, cluster object, role string) (bool, error) {
	ca, err := k.cnpgCA(ctx, ns, cluster)
	if err != nil {
		return false, err
	}
	secret, err := k.cnpgSecret(ctx, ns, databasecontroller.CNPGClusterName+"-app", cluster)
	if err != nil {
		return false, err
	}
	user, err := data(secret, "username")
	if err != nil || string(user) != "openshell" {
		return false, errors.New("CNPG check credentials have an invalid user")
	}
	password, err := data(secret, "password")
	if err != nil {
		return false, err
	}
	options := dbclient.Options{Host: cnpgHost(ns), Port: 5432, User: "openshell", Database: "openshell", Password: string(password), CA: ca, DialAddress: k.options.CNPGDialAddress}
	var exists bool
	if err = dbclient.ReadRow(ctx, options, "SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_roles WHERE rolname=$1) OR EXISTS (SELECT 1 FROM pg_catalog.pg_database WHERE datname=$1)", []any{role}, &exists); err != nil {
		return false, err
	}
	return !exists, nil
}

func cnpgInteger(value object, keys ...string) int64 {
	n, ok := kube.Nested(value, keys...).(json.Number)
	if !ok {
		return -1
	}
	v, err := n.Int64()
	if err != nil {
		return -1
	}
	return v
}

// A current Kubernetes generation cannot prove current SQL permissions. Use
// the Gateway role itself, so this also verifies its password and login rights.
func (k *Kubernetes) cnpgRoleReady(ctx context.Context, ns, role, password string, ca []byte) (bool, error) {
	options := dbclient.Options{Host: cnpgHost(ns), Port: 5432, User: role, Database: role, Password: password, CA: ca, DialAddress: k.options.CNPGDialAddress}
	var roleReady, databaseReady bool
	err := dbclient.ReadRow(ctx, options, `SELECT r.rolcanlogin AND NOT (r.rolsuper OR r.rolcreatedb OR r.rolcreaterole OR r.rolreplication OR r.rolbypassrls OR r.rolinherit) AND r.rolconnlimit=32 AND (r.rolvaliduntil IS NULL OR r.rolvaliduntil='infinity'::timestamptz) AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_auth_members m WHERE m.member=r.oid), d.datdba=r.oid AND d.datallowconn AND d.datconnlimit=32 FROM pg_catalog.pg_roles r JOIN pg_catalog.pg_database d ON d.datname=current_database() WHERE r.rolname=current_user AND r.rolname=$1`, []any{role}, &roleReady, &databaseReady)
	if err != nil {
		var failure *dbclient.Error
		if errors.As(err, &failure) && failure.Stage == "connect" && (failure.SQLState == "28P01" || failure.SQLState == "28000") {
			return false, nil
		}
		return false, err
	}
	if !databaseReady {
		return false, errors.New("CNPG Gateway SQL database settings differ from their declaration")
	}
	return roleReady, nil
}

// Ask the operator to reconcile after confirmed SQL drift. Do not write this
// annotation on a successful check or on an unknown connection failure.
func (k *Kubernetes) requestCNPGRepair(ctx context.Context, ns, dbID string) error {
	desired := object{"apiVersion": "postgresql.cnpg.io/v1", "kind": "Cluster", "metadata": object{"name": databasecontroller.CNPGClusterName, "labels": object{managerLabel: "hypershell-database-controller", "hypershell.redhat.io/database-id": dbID}, "annotations": object{"cnpg.io/reloadedAt": time.Now().UTC().Format(time.RFC3339Nano)}}}
	_, err := k.client.Ensure(ctx, sharedAPI+ns+"/clusters", desired, databaseOwner(dbID))
	return err
}
