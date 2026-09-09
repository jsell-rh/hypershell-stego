package gatewayworkload

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"

	"github.com/jsell-rh/hypershell-stego/internal/databasecontroller"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

type object = kube.Object
type Options struct {
	SandboxRuntimeClass                                    string
	ClusterID                                              string
	ServerURL, CAFile, TokenFile, ClusterIssuer            string
	Issuer, TrustBundleFile, SandboxImage, SupervisorImage string
}
type Kubernetes struct {
	client  *kube.Client
	options Options
	trust   string
}

func NewKubernetes(o Options) (*Kubernetes, error) {
	if _, err := Namespace(o.ClusterID); err != nil {
		return nil, errors.New("Gateway controller requires a managed cluster ID")
	}
	if (o.SandboxRuntimeClass != "" && !dnsLabel.MatchString(o.SandboxRuntimeClass)) || !dnsLabel.MatchString(o.ClusterIssuer) || !validIssuer(o.Issuer) || !digestImage.MatchString(o.SandboxImage) || !digestImage.MatchString(o.SupervisorImage) {
		return nil, errors.New("Gateway controller configuration is invalid")
	}
	f, err := os.Open(o.TrustBundleFile)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	trust, err := io.ReadAll(io.LimitReader(f, (512<<10)+1))
	if err != nil || len(trust) > 512<<10 {
		return nil, errors.New("Gateway trust bundle is invalid")
	}
	trust, err = certificateBundle(trust)
	if err != nil {
		return nil, err
	}
	c, err := kube.New(kube.Options{ServerURL: o.ServerURL, CAFile: o.CAFile, TokenFile: o.TokenFile})
	if err != nil {
		return nil, err
	}
	return &Kubernetes{c, o, string(trust)}, nil
}

// Only certificates can enter the public ConfigMap. Never copy a private key
// or other file contents that happen to be beside a certificate in the input.
func certificateBundle(input []byte) ([]byte, error) {
	var output []byte
	for len(bytes.TrimSpace(input)) != 0 {
		block, rest := pem.Decode(input)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, errors.New("Gateway trust bundle must contain only certificates")
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			return nil, errors.New("Gateway trust bundle contains an invalid certificate")
		}
		output = append(output, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: block.Bytes})...)
		input = rest
	}
	if len(output) == 0 {
		return nil, errors.New("Gateway trust bundle is empty")
	}
	return output, nil
}
func (k *Kubernetes) Close()                      { k.client.Close() }
func (k *Kubernetes) Handles(gw *pb.Gateway) bool { return gw.GetClusterId() == k.options.ClusterID }
func owner(id string) kube.Owner                  { return kube.Owner{ownerLabel: id, managerLabel: manager} }
func definition(api, kind, name, id string) object {
	return object{"apiVersion": api, "kind": kind, "metadata": object{"name": name, "labels": object{ownerLabel: id, managerLabel: manager}}}
}
func data(secret object, key string) ([]byte, error) {
	raw := kube.String(secret, "data", key)
	value, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(value) == 0 {
		return nil, errors.New("Gateway dependency Secret is invalid")
	}
	return value, nil
}
func (k *Kubernetes) ensure(ctx context.Context, path string, desired object, id string) (object, error) {
	return k.client.Ensure(ctx, path, desired, owner(id))
}

func (k *Kubernetes) Ensure(ctx context.Context, gw *pb.Gateway, db *pb.ManagedDatabase, release *pb.GatewayRelease) error {
	if !k.Handles(gw) {
		return errors.New("Gateway belongs to a different managed cluster")
	}
	oidc, err := validate(gw, db, release, k.options.Issuer)
	if err != nil {
		return err
	}
	if gw.GetSupervisorImage() != "" && gw.GetSupervisorImage() != k.options.SupervisorImage {
		return errors.New("Gateway supervisor image differs from controller configuration")
	}
	id, ns := gw.Metadata.Id, gw.Namespace
	keys, err := k.keys(ctx, gw, db)
	if err != nil {
		return err
	}
	dbSecret, code, err := k.client.Request(ctx, http.MethodGet, "/api/v1/namespaces/"+db.Namespace+"/secrets/"+databasecontroller.CredentialsName, nil)
	if err != nil {
		return err
	}
	if code == 404 {
		return ErrPending
	}
	if !databaseOwner(db.Metadata.Id).Matches(dbSecret) {
		return errors.New("Gateway database Secret has a different owner")
	}
	dbData := object{}
	for key, want := range map[string]string{"host": databasecontroller.WorkloadName + "." + db.Namespace + ".svc.cluster.local", "user": "openshell", "dbname": "openshell", "port": "5432", "sslmode": "verify-full"} {
		value, err := data(dbSecret, key)
		if err != nil || string(value) != want {
			return errors.New("Gateway database connection does not match its placement")
		}
	}
	password, err := data(dbSecret, "password")
	if err != nil {
		return err
	}
	if _, err = hex.DecodeString(string(password)); err != nil || len(password) != 64 {
		return errors.New("Gateway database password is invalid")
	}
	ca, err := data(dbSecret, "ca.crt")
	if err != nil || !x509.NewCertPool().AppendCertsFromPEM(ca) {
		return errors.New("Gateway database CA is invalid")
	}
	address := url.URL{Scheme: "postgresql", User: url.UserPassword("openshell", string(password)), Host: databasecontroller.WorkloadName + "." + db.Namespace + ".svc.cluster.local:5432", Path: "/openshell"}
	address.RawQuery = url.Values{"sslmode": {"verify-full"}, "sslrootcert": {"/etc/openshell-db/ca.crt"}}.Encode()
	dbData["uri"] = base64.StdEncoding.EncodeToString([]byte(address.String()))
	dbData["ca.crt"] = base64.StdEncoding.EncodeToString(ca)
	namespace := definition("v1", "Namespace", ns, id)
	namespace["metadata"].(object)["labels"].(object)["pod-security.kubernetes.io/enforce"] = "restricted"
	if _, err = k.ensure(ctx, "/api/v1/namespaces", namespace, id); err != nil {
		return err
	}
	core := "/api/v1/namespaces/" + ns
	for name, values := range map[string]object{keysName: keys, "openshell-gateway-db-credentials": dbData} {
		secret := definition("v1", "Secret", name, id)
		secret["type"] = "Opaque"
		secret["data"] = values
		if name == keysName {
			secret["immutable"] = true
		}
		if _, err = k.ensure(ctx, core+"/secrets", secret, id); err != nil {
			return err
		}
	}
	host := Name + "." + ns + ".svc.cluster.local"
	for _, name := range []string{"openshell-server-tls", "openshell-client-tls"} {
		cert := definition("cert-manager.io/v1", "Certificate", name, id)
		usage := "server auth"
		if name == "openshell-client-tls" {
			usage = "client auth"
		}
		cert["spec"] = object{"secretName": name, "issuerRef": object{"name": k.options.ClusterIssuer, "kind": "ClusterIssuer"}, "dnsNames": []string{host}, "privateKey": object{"algorithm": "ECDSA", "size": 256, "rotationPolicy": "Always"}, "usages": []string{usage}, "duration": "2160h", "renewBefore": "360h", "secretTemplate": object{"labels": object{ownerLabel: id, managerLabel: manager}}}
		if _, err = k.ensure(ctx, "/apis/cert-manager.io/v1/namespaces/"+ns+"/certificates", cert, id); err != nil {
			return err
		}
	}
	server, code, err := k.client.Request(ctx, http.MethodGet, core+"/secrets/openshell-server-tls", nil)
	if err != nil {
		return err
	}
	if code == 404 {
		return ErrPending
	}
	if !owner(id).Matches(server) {
		return errors.New("Gateway TLS Secret has a different owner")
	}
	crt, err := data(server, "tls.crt")
	if err != nil {
		return err
	}
	key, err := data(server, "tls.key")
	if err != nil {
		return err
	}
	pair, err := tls.X509KeyPair(crt, key)
	if err != nil {
		return errors.New("Gateway TLS key does not match its certificate")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return err
	}
	root, err := data(server, "ca.crt")
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(root) {
		return errors.New("Gateway TLS CA is invalid")
	}
	intermediates := x509.NewCertPool()
	for _, raw := range pair.Certificate[1:] {
		c, err := x509.ParseCertificate(raw)
		if err != nil {
			return err
		}
		intermediates.AddCert(c)
	}
	if _, err = leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, DNSName: host}); err != nil {
		return errors.New("Gateway TLS certificate is not valid for its Service")
	}
	sandboxNS := ns
	if k.options.SandboxRuntimeClass != "" {
		sandboxNS, _ = SandboxNamespace(id)
		if err := k.ensureSandbox(ctx, id, sandboxNS, core, roots); err != nil {
			return err
		}
	}
	config := definition("v1", "ConfigMap", Name+"-config", id)
	config["data"] = object{"gateway.toml": configuration(ns, sandboxNS, k.options), "trust.pem": k.trust}
	if _, err = k.ensure(ctx, core+"/configmaps", config, id); err != nil {
		return err
	}
	for _, entry := range resources(gw, sandboxNS, release, oidc, config, dbData, keys, hex.EncodeToString(sha256sum(crt))) {
		if _, err = k.ensure(ctx, entry.path, entry.object, id); err != nil {
			return err
		}
	}
	current, _, err := k.client.Request(ctx, http.MethodGet, "/apis/apps/v1/namespaces/"+ns+"/deployments/"+Name, nil)
	if err != nil {
		return err
	}
	integer := func(keys ...string) int64 {
		n, ok := kube.Nested(current, keys...).(json.Number)
		if !ok {
			return -1
		}
		v, err := n.Int64()
		if err != nil {
			return -1
		}
		return v
	}
	generation := integer("metadata", "generation")
	if generation < 1 || integer("status", "observedGeneration") < generation || integer("status", "readyReplicas") != 1 || integer("status", "updatedReplicas") != 1 {
		return ErrPending
	}
	return nil
}
func sha256sum(value []byte) []byte { sum := sha256.Sum256(value); return sum[:] }

func (k *Kubernetes) Delete(ctx context.Context, id string) error {
	ns, err := Namespace(id)
	if err != nil {
		return err
	}
	sandboxNS, _ := SandboxNamespace(id)
	gone, err := k.client.DeleteOwned(ctx, "/api/v1/namespaces/"+sandboxNS, owner(id))
	if err != nil {
		return err
	}
	if !gone {
		return ErrPending
	}
	for _, path := range []string{admissionAPI + "/validatingadmissionpolicybindings/", admissionAPI + "/validatingadmissionpolicies/", mutationAPI + "/mutatingadmissionpolicybindings/", mutationAPI + "/mutatingadmissionpolicies/"} {
		gone, err := k.client.DeleteOwned(ctx, path+sandboxNS, owner(id))
		if err != nil {
			return err
		}
		if !gone {
			return ErrPending
		}
	}
	// Remove the cluster binding before the namespace. Namespace deletion removes
	// namespaced resources. The database retains the durable encryption keys.
	for _, path := range []string{"/apis/rbac.authorization.k8s.io/v1/clusterrolebindings/" + ns, "/apis/rbac.authorization.k8s.io/v1/clusterroles/" + ns, "/api/v1/namespaces/" + ns} {
		gone, err := k.client.DeleteOwned(ctx, path, owner(id))
		if err != nil {
			return err
		}
		if !gone {
			return ErrPending
		}
	}
	return nil
}

// Owns permits cleanup of retained resources after a cluster assignment changed.
// An unassigned live Gateway is never created or changed by this provider.
func (k *Kubernetes) Owns(ctx context.Context, gw *pb.Gateway) (bool, error) {
	id := gw.GetMetadata().GetId()
	ns, err := Namespace(id)
	if err != nil {
		return false, err
	}
	sandboxNS, _ := SandboxNamespace(id)
	dbNS, err := gateways.DatabaseNamespace(gw.GetDatabaseId())
	if err != nil {
		return false, err
	}
	for _, entry := range []struct {
		path     string
		database bool
	}{
		{"/api/v1/namespaces/" + ns, false},
		{"/api/v1/namespaces/" + sandboxNS, false},
		{"/apis/rbac.authorization.k8s.io/v1/clusterrolebindings/" + ns, false},
		{"/apis/rbac.authorization.k8s.io/v1/clusterroles/" + ns, false},
		{"/api/v1/namespaces/" + dbNS, true},
	} {
		row, code, err := k.client.Request(ctx, http.MethodGet, entry.path, nil)
		if err != nil {
			return false, err
		}
		if code == 404 {
			continue
		}
		if entry.database {
			if databaseOwner(gw.GetDatabaseId()).Matches(row) && kube.String(row, "metadata", "labels", ownerLabel) == id {
				return true, nil
			}
		} else if owner(id).Matches(row) {
			return true, nil
		}
	}
	return false, nil
}

// GatewayIDs includes orphan cluster bindings after namespace removal. Every
// deletion still requires an explicit deleted state from the application API.
func (k *Kubernetes) GatewayIDs(ctx context.Context) ([]string, error) {
	seen := map[string]bool{}
	ids := []string{}
	for _, scan := range []struct {
		collection, selector string
		database             bool
	}{
		{"/api/v1/namespaces", managerLabel + "=" + manager, false},
		{"/apis/rbac.authorization.k8s.io/v1/clusterrolebindings", managerLabel + "=" + manager, false},
		{"/apis/rbac.authorization.k8s.io/v1/clusterroles", managerLabel + "=" + manager, false},
		{"/api/v1/namespaces", managerLabel + "=hypershell-database-controller," + ownerLabel, true},
	} {
		cursor := ""
		for page := 0; page < 100; page++ {
			query := url.Values{"limit": {"100"}, "labelSelector": {scan.selector}, "continue": {cursor}}
			list, _, err := k.client.Request(ctx, http.MethodGet, scan.collection+"?"+query.Encode(), nil)
			if err != nil {
				return nil, err
			}
			items, ok := list["items"].([]any)
			if !ok || len(items) > 100 {
				return nil, errors.New("Gateway resource inventory is invalid")
			}
			for _, item := range items {
				row, ok := item.(map[string]any)
				if !ok {
					return nil, errors.New("Gateway resource inventory is invalid")
				}
				id := kube.String(row, "metadata", "labels", ownerLabel)
				ns, err := Namespace(id)
				matches := owner(id).Matches(row)
				if scan.database {
					dbID := kube.String(row, "metadata", "labels", "hypershell.redhat.io/database-id")
					var dbErr error
					ns, dbErr = gateways.DatabaseNamespace(dbID)
					matches = dbErr == nil && databaseOwner(dbID).Matches(row)
				}
				sandboxNS, _ := SandboxNamespace(id)
				name := kube.String(row, "metadata", "name")
				validName := name == ns || (!scan.database && scan.collection == "/api/v1/namespaces" && name == sandboxNS)
				if err != nil || !validName || !matches {
					return nil, errors.New("Gateway resource inventory has invalid ownership")
				}
				if !seen[id] {
					seen[id] = true
					ids = append(ids, id)
				}
			}
			next := kube.String(list, "metadata", "continue")
			if next == "" {
				break
			}
			if next == cursor || page == 99 {
				return nil, errors.New("Gateway resource inventory exceeds its limit")
			}
			cursor = next
		}
	}
	return ids, nil
}
