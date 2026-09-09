package gatewayworkload

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	keycloak "github.com/jsell-rh/hypershell-stego/internal/serviceaccountkeycloak"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"github.com/segmentio/ksuid"
)

var testClusterID = ksuid.New().String()

func records(t *testing.T) (*pb.Gateway, *pb.ManagedDatabase, *pb.GatewayRelease) {
	t.Helper()
	id, database, release := ksuid.New().String(), ksuid.New().String(), ksuid.New().String()
	ns, _ := Namespace(id)
	dbNS, _ := gateways.DatabaseNamespace(database)
	client, _ := keycloak.GatewayClientID(id)
	config, _ := json.Marshal(oidcConfig{Issuer: "https://issuer.example/realm", ClientID: client, Audience: client, JWKSTTL: 3600, RolesClaim: "hypershell.roles", AdminRole: keycloak.RoleAdmin, UserRole: keycloak.RoleUser})
	oidc := string(config)
	ready := "ready"
	return &pb.Gateway{ClusterId: testClusterID, Metadata: &pb.ObjectReference{Id: id}, Namespace: ns, DatabaseId: database, ReleaseId: release, Oidc: &oidc}, &pb.ManagedDatabase{Metadata: &pb.ObjectReference{Id: database}, Namespace: dbNS, Provider: gateways.ProviderDeployment, Status: &ready}, &pb.GatewayRelease{Metadata: &pb.ObjectReference{Id: release}, Image: "registry.example/gateway@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
}
func fixture(t *testing.T, handler http.HandlerFunc) *Kubernetes {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	dir := t.TempDir()
	ca, token := filepath.Join(dir, "ca"), filepath.Join(dir, "token")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(token, []byte("acceptance-token"), 0600); err != nil {
		t.Fatal(err)
	}
	image := "registry.example/image@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	k, err := NewKubernetes(Options{ClusterID: testClusterID, ServerURL: server.URL, CAFile: ca, TokenFile: token, ClusterIssuer: "issuer", Issuer: "https://issuer.example/realm", TrustBundleFile: ca, SandboxImage: image, SupervisorImage: image})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(k.Close)
	return k
}
func TestInvalidGatewayCannotWriteResources(t *testing.T) {
	for _, tc := range []string{"cluster", "namespace", "database", "release", "image", "issuer", "audience", "roles", "driver", "supervisor"} {
		t.Run(tc, func(t *testing.T) {
			gw, db, release := records(t)
			switch tc {
			case "cluster":
				gw.ClusterId = ksuid.New().String()
			case "namespace":
				gw.Namespace = "foreign"
			case "database":
				db.Metadata.Id = ksuid.New().String()
			case "release":
				release.Metadata.Id = ksuid.New().String()
			case "image":
				release.Image = "registry.example/gateway:latest"
			case "issuer", "audience", "roles":
				var o oidcConfig
				_ = json.Unmarshal([]byte(gw.GetOidc()), &o)
				if tc == "issuer" {
					o.Issuer = "https://foreign.example"
				}
				if tc == "audience" {
					o.Audience = "another-gateway"
				}
				if tc == "roles" {
					o.AdminRole = ""
				}
				raw, _ := json.Marshal(o)
				v := string(raw)
				gw.Oidc = &v
			case "driver":
				v := `{"type":"external"}`
				gw.CredentialDriver = &v
			case "supervisor":
				v := "registry.example/untrusted:latest"
				gw.SupervisorImage = &v
			}
			k := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				t.Error("invalid Gateway reached Kubernetes")
				w.WriteHeader(500)
			})
			if err := k.Ensure(context.Background(), gw, db, release); err == nil {
				t.Fatal("invalid Gateway accepted")
			}
		})
	}
}
func TestLostOrForeignKeysCannotBeReplaced(t *testing.T) {
	for _, tc := range []string{"lost", "foreign namespace", "foreign keys", "corrupt keys", "replaced keys"} {
		t.Run(tc, func(t *testing.T) {
			gw, db, _ := records(t)
			k := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Error("unsafe key write")
					w.WriteHeader(500)
					return
				}
				if r.URL.Path == "/api/v1/namespaces/"+db.Namespace {
					ownerID := db.Metadata.Id
					if tc == "foreign namespace" {
						ownerID = "other"
					}
					_ = json.NewEncoder(w).Encode(object{"metadata": object{"uid": "uid", "resourceVersion": "1", "labels": object{"hypershell.redhat.io/database-id": ownerID, managerLabel: "hypershell-database-controller", ownerLabel: gw.Metadata.Id}, "annotations": object{keysMarker: gw.Metadata.Id + ":original-key-fingerprint"}}})
					return
				}
				if tc == "lost" {
					w.WriteHeader(404)
					return
				}
				ownerID := gw.Metadata.Id
				if tc == "foreign keys" {
					ownerID = "other"
				}
				secret := definition("v1", "Secret", keysName, ownerID)
				secret["immutable"] = true
				secret["data"] = object{"signing.pem": "broken"}
				if tc == "replaced keys" {
					values, err := newKeys()
					if err != nil {
						t.Error(err)
						w.WriteHeader(500)
						return
					}
					secret["data"] = values
				}
				_ = json.NewEncoder(w).Encode(secret)
			})
			if _, err := k.keys(context.Background(), gw, db); err == nil {
				t.Fatal("lost or foreign key material accepted")
			}
		})
	}
}
func TestSigningKeyPairAndEncryptionKeyAreValidated(t *testing.T) {
	values, err := newKeys()
	if err != nil {
		t.Fatal(err)
	}
	secret := object{"data": values}
	if err := validateKeys(secret); err != nil {
		t.Fatal(err)
	}
	other, err := newKeys()
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"signing.pem", "public.pem", "kid", "key-encryption-key"} {
		original := values[field]
		values[field] = other[field]
		if field == "key-encryption-key" {
			values[field] = "aW52YWxpZA=="
		}
		if err := validateKeys(secret); err == nil {
			t.Fatalf("invalid %s accepted", field)
		}
		values[field] = original
	}
}

func TestTrustBundleCannotPublishPrivateMaterial(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	key := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("private material")})
	for _, input := range [][]byte{nil, []byte("not a certificate"), append(append([]byte{}, cert...), key...)} {
		if _, err := certificateBundle(input); err == nil {
			t.Fatal("invalid trust bundle accepted")
		}
	}
	value, err := certificateBundle(append([]byte("private annotation outside PEM\n"), cert...))
	if err != nil || !bytes.Equal(value, cert) {
		t.Fatal("public trust bundle contains data outside its certificates", err)
	}
}
