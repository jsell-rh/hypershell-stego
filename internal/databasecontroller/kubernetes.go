// Package databasecontroller supplies the Hypershell database workflow.
package databasecontroller

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"regexp"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

const PostgresImage = "postgres@sha256:d3e1620b530c944afa6e887d22eb899824da68e19c52024bf98f5220c88a65b2"
const CredentialsName = "openshell-db-credentials"
const WorkloadName = "openshell-gateway-db"
const ownerLabel = "hypershell.redhat.io/database-id"
const managerLabel = "app.kubernetes.io/managed-by"
const manager = "hypershell-database-controller"

var dnsName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
var ErrPending = errors.New("database workload is not ready")

type KubernetesOptions struct{ ServerURL, CAFile, TokenFile, ClusterIssuer string }
type Kubernetes struct {
	client *kube.Client
	issuer string
}
type object = kube.Object

func NewKubernetes(o KubernetesOptions) (*Kubernetes, error) {
	if !dnsName.MatchString(o.ClusterIssuer) {
		return nil, errors.New("database controller requires a ClusterIssuer name")
	}
	c, err := kube.New(kube.Options{ServerURL: o.ServerURL, CAFile: o.CAFile, TokenFile: o.TokenFile})
	if err != nil {
		return nil, err
	}
	return &Kubernetes{c, o.ClusterIssuer}, nil
}
func (k *Kubernetes) Close() { k.client.Close() }
func (k *Kubernetes) request(ctx context.Context, method, path string, input object) (object, int, error) {
	return k.client.Request(ctx, method, path, input)
}
func nested(o object, keys ...string) any  { return kube.Nested(o, keys...) }
func str(o object, keys ...string) string  { return kube.String(o, keys...) }
func owner(id string) kube.Owner           { return kube.Owner{ownerLabel: id, managerLabel: manager} }
func contains(actual, desired object) bool { return kube.Contains(actual, desired) }
func integer(o object, keys ...string) int64 {
	number, ok := nested(o, keys...).(json.Number)
	if !ok {
		return -1
	}
	value, err := number.Int64()
	if err != nil {
		return -1
	}
	return value
}
func labels(id string) object {
	return object{ownerLabel: id, managerLabel: manager, "app.kubernetes.io/name": "openshell", "app.kubernetes.io/component": "database"}
}
func owned(o object, id string) bool {
	return str(o, "metadata", "labels", ownerLabel) == id && str(o, "metadata", "labels", managerLabel) == manager
}
func definition(api, kind, name, id string) object {
	return object{"apiVersion": api, "kind": kind, "metadata": object{"name": name, "labels": labels(id)}}
}
func validatePlacement(db *pb.ManagedDatabase) error {
	if db == nil || db.GetProvider() != gateways.ProviderDeployment {
		return errors.New("database controller requires deployment placement")
	}
	ns, err := gateways.DatabaseNamespace(db.GetMetadata().GetId())
	if err != nil || db.GetNamespace() != ns {
		return errors.New("database namespace does not match its ID")
	}
	return nil
}

func validateDatabase(db *pb.ManagedDatabase) error {
	if err := validatePlacement(db); err != nil {
		return err
	}

	switch db.GetEngine() {
	case "", "postgres", "postgresql":
	default:
		return errors.New("deployment database engine is not supported")
	}
	switch db.GetEngineVersion() {
	case "", "18", "18.6":
	default:
		return errors.New("deployment database engine version is not supported")
	}
	if db.GetRegion() != "" || db.GetInstanceClass() != "" {
		return errors.New("deployment database region and instance class overrides are not supported")
	}
	if db.GetConnectionSecret() != "" && db.GetConnectionSecret() != CredentialsName {
		return errors.New("deployment database connection Secret override is not supported")
	}
	return nil
}

func (k *Kubernetes) ensure(ctx context.Context, collection string, want object, id string) (object, error) {
	return k.client.Ensure(ctx, collection, want, owner(id))
}
func (k *Kubernetes) Ensure(ctx context.Context, db *pb.ManagedDatabase) error {
	if err := validateDatabase(db); err != nil {
		return err
	}
	id, ns := db.Metadata.Id, db.Namespace
	namespace := definition("v1", "Namespace", ns, id)
	namespace["metadata"].(object)["labels"].(object)["hypershell.redhat.io/database-provider"] = "deployment"
	namespace["metadata"].(object)["labels"].(object)["pod-security.kubernetes.io/enforce"] = "restricted"
	if _, err := k.ensure(ctx, "/api/v1/namespaces", namespace, id); err != nil {
		return err
	}
	core := "/api/v1/namespaces/" + ns
	// cert-manager owns issuance and renewal. The certificate name and Secret are
	// fixed within this database's namespace.
	cert := definition("cert-manager.io/v1", "Certificate", WorkloadName, id)
	cert["spec"] = object{"secretName": "openshell-db-tls", "issuerRef": object{"name": k.issuer, "kind": "ClusterIssuer", "group": "cert-manager.io"}, "dnsNames": []string{WorkloadName + "." + ns + ".svc.cluster.local"}, "privateKey": object{"algorithm": "ECDSA", "size": 256, "rotationPolicy": "Always"}, "usages": []string{"server auth"}, "duration": "2160h", "renewBefore": "360h", "secretTemplate": object{"labels": labels(id)}}
	if _, err := k.ensure(ctx, "/apis/cert-manager.io/v1/namespaces/"+ns+"/certificates", cert, id); err != nil {
		return err
	}
	tlsSecret, code, err := k.request(ctx, http.MethodGet, core+"/secrets/openshell-db-tls", nil)
	if err != nil {
		return err
	}
	if code == 404 {
		return ErrPending
	}
	if !owned(tlsSecret, id) {
		return errors.New("database TLS Secret has a different owner")
	}
	ca := str(tlsSecret, "data", "ca.crt")
	crt := str(tlsSecret, "data", "tls.crt")
	if ca == "" || crt == "" || str(tlsSecret, "data", "tls.key") == "" {
		return errors.New("database TLS Secret is incomplete")
	}
	if err := verifyCertificate(ca, crt, WorkloadName+"."+ns+".svc.cluster.local"); err != nil {
		return err
	}
	if err := k.credentials(ctx, core, id, ns, ca); err != nil {
		return err
	}
	if err := k.bootstrapSecret(ctx, core, id); err != nil {
		return err
	}
	pvc := definition("v1", "PersistentVolumeClaim", WorkloadName+"-data", id)
	pvc["spec"] = object{"accessModes": []string{"ReadWriteOnce"}, "resources": object{"requests": object{"storage": "1Gi"}}}
	if _, err := k.ensure(ctx, core+"/persistentvolumeclaims", pvc, id); err != nil {
		return err
	}
	config := definition("v1", "ConfigMap", "openshell-db-config", id)
	config["data"] = object{"init.sh": `psql -v ON_ERROR_STOP=1 --username postgres --dbname postgres --set=app_password="$APP_PASSWORD" <<'SQL'
CREATE ROLE openshell LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD :'app_password';
CREATE DATABASE openshell OWNER openshell;
SQL
`, "pg_hba.conf": "local all all trust\nhostssl all all 0.0.0.0/0 scram-sha-256\nhostssl all all ::/0 scram-sha-256\n"}
	if _, err := k.ensure(ctx, core+"/configmaps", config, id); err != nil {
		return err
	}
	service := definition("v1", "Service", WorkloadName, id)
	service["spec"] = object{"type": "ClusterIP", "selector": object{ownerLabel: id}, "ports": []object{{"name": "postgresql", "port": 5432, "targetPort": 5432, "protocol": "TCP"}}}
	if _, err := k.ensure(ctx, core+"/services", service, id); err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(crt))
	deployment := deploymentObject(id, ns, hex.EncodeToString(digest[:]))
	current, err := k.ensure(ctx, "/apis/apps/v1/namespaces/"+ns+"/deployments", deployment, id)
	if err != nil {
		return err
	}
	generation := integer(current, "metadata", "generation")
	observed := integer(current, "status", "observedGeneration")
	ready := integer(current, "status", "readyReplicas")
	updated := integer(current, "status", "updatedReplicas")
	if generation <= 0 || observed < generation || ready != 1 || updated != 1 {
		return ErrPending
	}
	return nil
}
func (k *Kubernetes) credentials(ctx context.Context, core, id, ns, ca string) error {
	path := core + "/secrets/" + CredentialsName
	old, code, err := k.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	password := ""
	if code != 404 {
		if !owned(old, id) {
			return errors.New("database credentials have a different owner")
		}
		data, err := base64.StdEncoding.DecodeString(str(old, "data", "password"))
		if err != nil || len(data) != 64 {
			return errors.New("database password is invalid")
		}
		if _, err := hex.DecodeString(string(data)); err != nil {
			return errors.New("database password is invalid")
		}
		password = string(data)
	} else {
		_, code, err := k.request(ctx, http.MethodGet, core+"/persistentvolumeclaims/"+WorkloadName+"-data", nil)
		if err != nil {
			return err
		}
		if code != 404 {
			return errors.New("database credentials are missing for an existing volume")
		}
		data := make([]byte, 32)
		if _, err := rand.Read(data); err != nil {
			return err
		}
		password = hex.EncodeToString(data)
	}
	host := WorkloadName + "." + ns + ".svc.cluster.local"
	data := object{}
	for key, value := range map[string]string{"host": host, "port": "5432", "dbname": "openshell", "user": "openshell", "password": password, "uri": "postgresql://openshell:" + password + "@" + host + ":5432/openshell?sslmode=verify-full", "sslmode": "verify-full"} {
		data[key] = base64.StdEncoding.EncodeToString([]byte(value))
	}
	data["ca.crt"] = ca
	desired := definition("v1", "Secret", CredentialsName, id)
	desired["type"] = "Opaque"
	desired["data"] = data
	// Do not reread and adopt a competing secret. The first observed version
	// protects the credential choice made above.
	if code == 404 {
		_, _, err = k.request(ctx, http.MethodPost, core+"/secrets", desired)
		return err
	}
	if contains(old, desired) {
		return nil
	}
	desired["metadata"].(object)["resourceVersion"] = str(old, "metadata", "resourceVersion")
	desired["metadata"].(object)["uid"] = str(old, "metadata", "uid")
	_, _, err = k.request(ctx, http.MethodPatch, path, desired)
	return err
}
func deploymentObject(id, namespace, certificateHash string) object {
	env := []object{
		{"name": "POSTGRES_USER", "value": "postgres"},
		{"name": "POSTGRES_DB", "value": "postgres"},
		{"name": "DB_TLS_NAME", "value": WorkloadName + "." + namespace + ".svc.cluster.local"},
		{"name": "POSTGRES_PASSWORD", "valueFrom": object{"secretKeyRef": object{"name": "openshell-db-bootstrap", "key": "password"}}},
		{"name": "APP_PASSWORD", "valueFrom": object{"secretKeyRef": object{"name": CredentialsName, "key": "password"}}},
	}
	env = append(env, object{"name": "PGDATA", "value": "/var/lib/postgresql/data/pgdata"}, object{"name": "POSTGRES_INITDB_ARGS", "value": "--auth-host=scram-sha-256"})
	container := object{"name": "postgres", "image": PostgresImage, "imagePullPolicy": "IfNotPresent", "args": []string{"postgres", "-c", "ssl=on", "-c", "ssl_min_protocol_version=TLSv1.3", "-c", "ssl_cert_file=/tls/tls.crt", "-c", "ssl_key_file=/tls/tls.key", "-c", "hba_file=/config/pg_hba.conf"}, "env": env,
		"securityContext": object{"allowPrivilegeEscalation": false, "privileged": false, "readOnlyRootFilesystem": true, "capabilities": object{"drop": []string{"ALL"}}},
		"resources":       object{"requests": object{"cpu": "100m", "memory": "256Mi"}, "limits": object{"cpu": "500m", "memory": "512Mi"}},
		"ports":           []object{{"name": "postgresql", "containerPort": 5432}},
		"volumeMounts":    []object{{"name": "data", "mountPath": "/var/lib/postgresql/data"}, {"name": "run", "mountPath": "/var/run/postgresql"}, {"name": "tmp", "mountPath": "/tmp"}, {"name": "tls", "mountPath": "/tls", "readOnly": true}, {"name": "config", "mountPath": "/config", "readOnly": true}, {"name": "config", "mountPath": "/docker-entrypoint-initdb.d/init.sh", "subPath": "init.sh", "readOnly": true}},
		"readinessProbe":  object{"exec": object{"command": []string{"sh", "-ec", `export PGPASSWORD="$APP_PASSWORD"; exec psql "host=$DB_TLS_NAME hostaddr=127.0.0.1 sslmode=verify-full sslrootcert=/tls/ca.crt user=openshell dbname=openshell connect_timeout=2" -Atqc 'SELECT 1'`}}, "periodSeconds": 3, "timeoutSeconds": 3},
		"startupProbe":    object{"exec": object{"command": []string{"pg_isready", "-h", "127.0.0.1", "-U", "openshell", "-d", "openshell"}}, "periodSeconds": 3, "failureThreshold": 40, "timeoutSeconds": 2},
	}
	pod := object{"automountServiceAccountToken": false, "securityContext": object{"runAsNonRoot": true, "runAsUser": 70, "runAsGroup": 70, "fsGroup": 70, "seccompProfile": object{"type": "RuntimeDefault"}}, "containers": []object{container}, "volumes": []object{{"name": "data", "persistentVolumeClaim": object{"claimName": WorkloadName + "-data"}}, {"name": "run", "emptyDir": object{"sizeLimit": "16Mi"}}, {"name": "tmp", "emptyDir": object{"sizeLimit": "16Mi"}}, {"name": "tls", "secret": object{"secretName": "openshell-db-tls", "defaultMode": 288}}, {"name": "config", "configMap": object{"name": "openshell-db-config", "defaultMode": 292}}}}
	deployment := definition("apps/v1", "Deployment", WorkloadName, id)
	deployment["spec"] = object{"replicas": 1, "strategy": object{"type": "Recreate"}, "selector": object{"matchLabels": object{ownerLabel: id}}, "template": object{"metadata": object{"labels": labels(id), "annotations": object{"hypershell.redhat.io/certificate-sha256": certificateHash}}, "spec": pod}}
	return deployment
}
func (k *Kubernetes) Delete(ctx context.Context, db *pb.ManagedDatabase) error {
	if err := validatePlacement(db); err != nil {
		return err
	}
	gone, err := k.client.DeleteOwned(ctx, "/api/v1/namespaces/"+db.Namespace, owner(db.Metadata.Id))
	if err != nil {
		return err
	}
	if !gone {
		return ErrPending
	}
	return nil
}

func verifyCertificate(caEncoded, certificateEncoded, hostname string) error {
	ca, err := base64.StdEncoding.DecodeString(caEncoded)
	if err != nil {
		return errors.New("invalid database CA")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return errors.New("invalid database CA")
	}
	encoded, err := base64.StdEncoding.DecodeString(certificateEncoded)
	if err != nil {
		return errors.New("invalid database certificate")
	}
	block, rest := pem.Decode(encoded)
	if block == nil {
		return errors.New("invalid database certificate")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return errors.New("invalid database certificate")
	}
	intermediates := x509.NewCertPool()
	intermediates.AppendCertsFromPEM(rest)
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, DNSName: hostname, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		return errors.New("database certificate verification failed")
	}
	return nil
}

func (k *Kubernetes) bootstrapSecret(ctx context.Context, core, id string) error {
	old, code, err := k.request(ctx, http.MethodGet, core+"/secrets/openshell-db-bootstrap", nil)
	if err != nil {
		return err
	}
	if code != 404 {
		if !owned(old, id) {
			return errors.New("database bootstrap Secret has a different owner")
		}
		password, err := base64.StdEncoding.DecodeString(str(old, "data", "password"))
		if err != nil || len(password) != 64 {
			return errors.New("invalid database bootstrap Secret")
		}
		return nil
	}
	_, code, err = k.request(ctx, http.MethodGet, core+"/persistentvolumeclaims/"+WorkloadName+"-data", nil)
	if err != nil {
		return err
	}
	if code != 404 {
		return errors.New("database bootstrap Secret is missing for an existing volume")
	}
	password := make([]byte, 32)
	if _, err := rand.Read(password); err != nil {
		return err
	}
	secret := definition("v1", "Secret", "openshell-db-bootstrap", id)
	secret["type"] = "Opaque"
	secret["data"] = object{"password": base64.StdEncoding.EncodeToString([]byte(hex.EncodeToString(password)))}
	_, _, err = k.request(ctx, http.MethodPost, core+"/secrets", secret)
	return err
}
