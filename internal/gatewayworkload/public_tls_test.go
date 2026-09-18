package gatewayworkload

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func publicTestCertificate(t *testing.T, host string, expiry time.Time, usage x509.ExtKeyUsage) ([]byte, []byte, []byte) {
	t.Helper()
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test issuer"}, IsCA: true, BasicConstraintsValid: true, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	rootDER, err := x509.CreateCertificate(rand.Reader, root, root, &rootKey.PublicKey, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), DNSNames: []string{host}, NotBefore: time.Now().Add(-time.Hour), NotAfter: expiry, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, root, &leafKey.PublicKey, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	key, err := x509.MarshalPKCS8PrivateKey(leafKey)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key})
}

func TestPublicTrustRequiresCertificateOnlyFile(t *testing.T) {
	ca, _, key := publicTestCertificate(t, "gateway.example.test", time.Now().Add(time.Hour), x509.ExtKeyUsageServerAuth)
	file := filepath.Join(t.TempDir(), "public-ca.pem")
	if err := os.WriteFile(file, ca, 0644); err != nil {
		t.Fatal(err)
	}
	options := Options{PublicDomain: "example.test", PublicIssuer: "public-issuer", PublicRouter: "default", PublicCAFile: file}
	if roots, err := publicTrust(options); err != nil || roots == nil {
		t.Fatal("public CA file rejected", err)
	}
	if err := os.WriteFile(file, append(ca, key...), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := publicTrust(options); err == nil {
		t.Fatal("private key entered public trust")
	}
}

func TestPublicTLSConfigurationKeepsInternalNames(t *testing.T) {
	namespace := "openshell-0123456789abcdef"
	for _, enabled := range []bool{false, true} {
		options := Options{}
		if enabled {
			options.PublicDomain = "example.test"
		}
		config := configuration(namespace, namespace, "sandbox-account", options)
		if strings.Contains(config, "%!") || !strings.Contains(config, `grpc_endpoint = "https://openshell-gateway.`+namespace+`.svc.cluster.local:8080"`) || !strings.Contains(config, `cert_path = "/etc/openshell-tls/tls.crt"`) {
			t.Fatal("public TLS changed internal configuration")
		}
		if strings.Contains(config, "external_cert_path") != enabled || strings.Contains(config, "external_server_names") != enabled {
			t.Fatal("public TLS selection differs")
		}
		if enabled && !strings.Contains(config, `external_server_names = ["gw-`+namespace+`.example.test"]`) {
			t.Fatal("public SNI differs from the assigned hostname")
		}
		gw, release := records(t)
		rendered := resources(gw, "allocated-gateway", release, oidcConfig{}, object{}, object{}, object{}, "hash", enabled)
		var found bool
		for _, entry := range rendered {
			if entry.object["apiVersion"] == "rbac.authorization.k8s.io/v1" || entry.object["kind"] == "ServiceAccount" {
				t.Fatal("Gateway workload declares authority owned by the allocator")
			}
			if entry.object["kind"] != "Deployment" {
				continue
			}
			spec := entry.object["spec"].(object)["template"].(object)["spec"].(object)
			if spec["serviceAccountName"] != "allocated-gateway" || spec["automountServiceAccountToken"] != true {
				t.Fatal("Gateway does not use its allocated account and explicit token mount")
			}
			for _, volume := range spec["volumes"].([]object) {
				if volume["name"] == "public-tls" {
					found = true
					if volume["secret"].(object)["secretName"] != "openshell-public-tls" {
						t.Fatal("public TLS Secret differs")
					}
				}
			}
		}
		if found != enabled {
			t.Fatal("public TLS Secret mount differs")
		}
	}
	for _, options := range []Options{{PublicIssuer: "issuer"}, {PublicDomain: "example.test"}, {PublicDomain: "*.example.test", PublicIssuer: "issuer"}, {PublicDomain: "example.test.", PublicIssuer: "issuer"}, {PublicDomain: "127.0.0.1", PublicIssuer: "issuer"}, {PublicDomain: "example.test", PublicIssuer: "issuer", PublicCAFile: "relative.pem"}} {
		if _, err := publicTrust(options); err == nil {
			t.Fatal("invalid public TLS settings accepted")
		}
	}
}

func TestPublicTLSSecretCannotSupplyItsOwnTrust(t *testing.T) {
	for _, scenario := range []string{"valid", "foreign issuer", "foreign owner", "private key in certificate", "missing", "deleting"} {
		t.Run(scenario, func(t *testing.T) {
			gw, _ := records(t)
			host := publicHostname(gw.Namespace, "example.test")
			root, cert, key := publicTestCertificate(t, host, time.Now().Add(time.Hour), x509.ExtKeyUsageServerAuth)
			roots := x509.NewCertPool()
			roots.AppendCertsFromPEM(root)
			if scenario == "foreign issuer" {
				root, cert, key = publicTestCertificate(t, host, time.Now().Add(time.Hour), x509.ExtKeyUsageServerAuth)
			}
			if scenario == "private key in certificate" {
				cert = append(cert, key...)
			}
			secret := definition("v1", "Secret", "openshell-public-tls", gw.Metadata.Id)
			secret["type"] = "kubernetes.io/tls"
			secret["metadata"].(object)["namespace"] = gw.Namespace
			secret["metadata"].(object)["uid"] = "secret-id"
			secret["metadata"].(object)["resourceVersion"] = "1"
			secret["data"] = object{"tls.crt": base64.StdEncoding.EncodeToString(cert), "tls.key": base64.StdEncoding.EncodeToString(key), "ca.crt": base64.StdEncoding.EncodeToString(root)}
			if scenario == "foreign owner" {
				secret["metadata"].(object)["labels"] = object{ownerLabel: "other", managerLabel: manager}
			}
			if scenario == "deleting" {
				secret["metadata"].(object)["deletionTimestamp"] = "2026-09-15T00:00:00Z"
			}
			created := false
			k := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/certificates/openshell-public-tls"):
					w.WriteHeader(http.StatusNotFound)
				case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/certificates"):
					var value object
					if json.NewDecoder(r.Body).Decode(&value) != nil {
						t.Error("invalid certificate request")
						w.WriteHeader(400)
						return
					}
					raw, _ := json.Marshal(value)
					if !bytes.Contains(raw, []byte(`"name":"public-issuer"`)) || !bytes.Contains(raw, []byte(host)) {
						t.Error("certificate does not match the selected public issuer and host")
					}
					created = true
					value["metadata"].(map[string]any)["uid"] = "certificate-id"
					value["metadata"].(map[string]any)["resourceVersion"] = "1"
					w.WriteHeader(http.StatusCreated)
					json.NewEncoder(w).Encode(value)
				case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/secrets/openshell-public-tls"):
					if scenario == "missing" {
						w.WriteHeader(http.StatusNotFound)
						return
					}
					json.NewEncoder(w).Encode(secret)
				default:
					t.Error("unexpected public TLS request")
					w.WriteHeader(500)
				}
			})
			k.options.PublicDomain = "example.test"
			k.options.PublicIssuer = "public-issuer"
			k.publicRoots = roots
			got, err := k.ensurePublicTLS(context.Background(), gw)
			if !created {
				t.Fatal("public certificate was not requested")
			}
			if scenario == "valid" {
				if err != nil || !bytes.Equal(got, cert) {
					t.Fatal("valid public certificate rejected", err)
				}
			} else {
				if err == nil || len(got) != 0 {
					t.Fatal("unsafe or incomplete public certificate accepted")
				}
				if (scenario == "missing" || scenario == "deleting") && !errors.Is(err, ErrPending) {
					t.Fatal("pending public certificate did not remain pending", err)
				}
			}
		})
	}
}
