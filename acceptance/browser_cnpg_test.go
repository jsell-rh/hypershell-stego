package acceptance

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/out/deploy/allocation"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
	postgres "github.com/jsell-rh/hypershell-stego/out/postgres"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Read credentials only into the test process. Evidence contains object UIDs.
func (w *browserGatewayWorkload) sqlOptions(id string) (postgres.Options, map[string]string) {
	w.t.Helper()
	ns, err := gateways.DatabaseNamespace(w.f.database)
	if err != nil {
		w.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	read := func(path string) kube.Object {
		object, code, err := w.kubernetes.Request(ctx, "GET", path, nil)
		if err != nil || code != 200 || kube.String(object, "metadata", "uid") == "" {
			w.t.Fatal("CNPG SQL fixture object unavailable", code)
		}
		return object
	}
	value := func(object kube.Object, field string) string {
		data, err := base64.StdEncoding.DecodeString(kube.String(object, "data", field))
		if err != nil || len(data) == 0 {
			w.t.Fatal("CNPG SQL fixture field unavailable")
		}
		return string(data)
	}
	base := "/api/v1/namespaces/" + ns + "/secrets/"
	ca := read(base + "openshell-db-ca")
	secret := read(base + cnpgGatewayName(id) + "-credentials")
	keys := read(base + cnpgGatewayName(id) + "-keys")
	cluster := read("/apis/postgresql.cnpg.io/v1/namespaces/" + ns + "/clusters/openshell-db")
	role := cnpgGatewayRole(id)
	if value(secret, "username") != role {
		w.t.Fatal("Gateway SQL login differs from its ID")
	}
	return postgres.Options{Host: "openshell-db-rw." + ns + ".svc.cluster.local", Port: 5432, User: role, Database: role, Password: value(secret, "password"), CA: []byte(value(ca, "ca.crt"))}, map[string]string{"credentials": kube.String(secret, "metadata", "uid"), "keys": kube.String(keys, "metadata", "uid"), "cluster": kube.String(cluster, "metadata", "uid")}
}

func (w *browserGatewayWorkload) checkSQLIsolation() map[string]map[string]string {
	w.t.Helper()
	if len(w.gatewayIDs) != 2 {
		w.t.Fatal("SQL isolation check requires two Gateways")
	}
	identities := map[string]map[string]string{}
	for i, id := range w.gatewayIDs {
		o, identity := w.sqlOptions(id)
		identities[id] = identity
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		var secure, limited, owner bool
		err := postgres.ReadRow(ctx, o, `SELECT s.ssl, NOT(r.rolsuper OR r.rolcreatedb OR r.rolcreaterole OR r.rolreplication OR r.rolbypassrls OR r.rolinherit) AND r.rolcanlogin AND r.rolconnlimit=32 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_auth_members m WHERE m.member=r.oid), d.datdba=r.oid AND d.datconnlimit=32 FROM pg_catalog.pg_roles r JOIN pg_catalog.pg_database d ON d.datname=current_database() JOIN pg_catalog.pg_stat_ssl s ON s.pid=pg_backend_pid() WHERE r.rolname=current_user`, nil, &secure, &limited, &owner)
		cancel()
		if err != nil || !secure || !limited || !owner {
			w.t.Fatal("Gateway SQL TLS, ownership, or login permissions differ", err)
		}
		for _, database := range []string{cnpgGatewayRole(w.gatewayIDs[1-i]), "openshell", "postgres"} {
			o.Database = database
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			var one int
			err := postgres.ReadRow(ctx, o, "SELECT 1", nil, &one)
			cancel()
			var failure *postgres.Error
			if !errors.As(err, &failure) || failure.Stage != "connect" || (failure.SQLState != "28000" && failure.SQLState != "42501") {
				w.t.Fatal("Gateway SQL cross-database access was not explicitly denied", err)
			}
		}
	}
	w.t.Log("Both Gateway logins use verified TLS, own separate databases, and cannot connect to another Gateway or a system database")
	return identities
}

func (w *browserGatewayWorkload) checkSQLDeletion(id string) {
	w.t.Helper()
	other := ""
	for _, candidate := range w.gatewayIDs {
		if candidate != id {
			other = candidate
		}
	}
	if other == "" {
		w.t.Fatal("remaining Gateway missing")
	}
	o, _ := w.sqlOptions(other)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var absent bool
	if err := postgres.ReadRow(ctx, o, `SELECT NOT EXISTS(SELECT 1 FROM pg_catalog.pg_roles WHERE rolname=$1) AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_database WHERE datname=$1)`, []any{cnpgGatewayRole(id)}, &absent); err != nil || !absent {
		w.t.Fatal("deleted Gateway SQL state remains", err)
	}
	ns, _ := gateways.DatabaseNamespace(w.f.database)
	for _, path := range []string{"secrets/" + cnpgGatewayName(id) + "-credentials", "secrets/" + cnpgGatewayName(id) + "-keys", "configmaps/" + cnpgGatewayName(id) + "-key-identity"} {
		_, code, err := w.kubernetes.Request(ctx, "GET", "/api/v1/namespaces/"+ns+"/"+path, nil)
		if err != nil || code != 404 {
			w.t.Fatal("deleted Gateway credential or key record remains", code)
		}
	}
}

// Start the operator and its lifetime limit only after the generated allocator
// grants namespace access. The deadline excludes the image build.
func (w *browserGatewayWorkload) startCNPG() {
	w.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	a, err := allocation.New(w.kubernetes, w.p.namespace)
	if err != nil {
		w.t.Fatal(err)
	}
	ns, err := gateways.DatabaseNamespace(w.f.database)
	if err != nil {
		w.t.Fatal(err)
	}
	path := "/apis/rbac.authorization.k8s.io/v1/namespaces/" + ns + "/rolebindings/stego-" + a.Marker() + "-2"
	for {
		object, code, err := w.kubernetes.Request(ctx, "GET", path, nil)
		if err != nil {
			w.t.Fatal("CNPG allocation read failed", err)
		}
		if code == 200 && kube.String(object, "roleRef", "name") == "cnpg-manager" {
			break
		}
		select {
		case <-ctx.Done():
			w.t.Fatal("CNPG namespace permissions were not allocated")
		case <-time.After(time.Second):
		}
	}
	path = "/apis/batch/v1/namespaces/cnpg-system/jobs/cnpg-test-lifetime"
	job, code, err := w.kubernetes.Request(ctx, "GET", path, nil)
	if err != nil || code != 200 {
		w.t.Fatal("CNPG lifetime Job is unavailable", code)
	}
	_, err = w.kubernetes.PatchOwned(ctx, path, job, kube.Object{"spec": kube.Object{"suspend": false}}, kube.Owner{"stego.test/cnpg-run": w.p.namespace})
	if err != nil {
		w.t.Fatal("CNPG lifetime Job could not start", err)
	}
	path = "/apis/apps/v1/namespaces/cnpg-system/deployments/cnpg-controller-manager"
	deployment, code, err := w.kubernetes.Request(ctx, "GET", path, nil)
	if err != nil || code != 200 {
		w.t.Fatal("CNPG operator Deployment is unavailable", code)
	}
	refs, _ := kube.Nested(deployment, "metadata", "ownerReferences").([]any)
	bounded := false
	for _, raw := range refs {
		ref, _ := raw.(map[string]any)
		if kube.String(ref, "apiVersion") == "batch/v1" && kube.String(ref, "kind") == "Job" && kube.String(ref, "name") == "cnpg-test-lifetime" && kube.String(ref, "uid") == kube.String(job, "metadata", "uid") {
			bounded = true
		}
	}
	deadline, _ := kube.Nested(job, "spec", "activeDeadlineSeconds").(json.Number)
	ttl, _ := kube.Nested(job, "spec", "ttlSecondsAfterFinished").(json.Number)
	if !bounded || deadline != "1800" || ttl != "0" {
		w.t.Fatal("CNPG operator lifetime is not bounded")
	}
	_, err = w.kubernetes.PatchOwned(ctx, path, deployment, kube.Object{"spec": kube.Object{"replicas": 1}}, kube.Owner{"stego.test/cnpg-run": w.p.namespace})
	if err != nil {
		w.t.Fatal("CNPG operator Deployment could not start", err)
	}
	for {
		deployment, code, err = w.kubernetes.Request(ctx, "GET", path, nil)
		if err != nil || code != 200 {
			w.t.Fatal("CNPG operator readiness read failed", code)
		}
		available, _ := kube.Nested(deployment, "status", "availableReplicas").(json.Number)
		if available == "1" {
			break
		}
		select {
		case <-ctx.Done():
			w.t.Fatal("CNPG operator did not become available")
		case <-time.After(time.Second):
		}
	}
}

func (w *browserGatewayWorkload) checkProviderWriteDenied(address, ca string) {
	w.t.Helper()
	_, connection := grpcClient(w.t, address, testIdentity{config: Config{CAFile: ca}})
	client := pb.NewManagedDatabaseServiceClient(connection)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+w.tokens["workload"]))
	var before, after metadata.MD
	if _, err := client.GetManagedDatabase(ctx, &pb.GetManagedDatabaseRequest{Id: w.f.database}, grpc.Header(&before)); err != nil {
		w.t.Fatal("Gateway worker cannot read database placement", err)
	}
	version, err := rpc.ObservedResourceVersion(before)
	if err != nil {
		w.t.Fatal(err)
	}
	write, err := rpc.WithResourceVersion(ctx, version)
	if err != nil {
		w.t.Fatal(err)
	}
	if _, err := client.UpdateManagedDatabase(write, &pb.UpdateManagedDatabaseRequest{Id: w.f.database, Status: pointer("ready")}); status.Code(err) != codes.PermissionDenied {
		w.t.Fatal("Gateway worker can write database-provider observations", status.Code(err))
	}
	if _, err := client.GetManagedDatabase(ctx, &pb.GetManagedDatabaseRequest{Id: w.f.database}, grpc.Header(&after)); err != nil {
		w.t.Fatal(err)
	}
	next, err := rpc.ObservedResourceVersion(after)
	if err != nil || next != version {
		w.t.Fatal("denied database observation changed its version")
	}
	w.t.Log("Gateway worker can read placement but cannot write database-provider observations")
}
