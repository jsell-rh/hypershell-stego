package gatewayworkload

import (
	"crypto/x509"
	"encoding/base64"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInternalTLSRequiresOperatorTrust(t *testing.T) {
	k := fixture(t, func(http.ResponseWriter, *http.Request) { t.Error("invalid trust reached the cluster") })
	invalid := filepath.Join(t.TempDir(), "invalid.pem")
	if err := os.WriteFile(invalid, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "relative.pem", invalid, filepath.Join(t.TempDir(), "missing.pem")} {
		options := k.options
		options.InternalCAFile = name
		client, err := NewKubernetes(options)
		if err == nil || client != nil {
			t.Fatal("missing or invalid operator trust accepted")
		}
	}
}

func TestInternalTLSSecretCannotSupplyItsOwnTrust(t *testing.T) {
	for _, scenario := range []string{"valid", "foreign issuer", "missing trust", "foreign owner", "wrong namespace", "missing UID", "deleting", "private key in certificate"} {
		t.Run(scenario, func(t *testing.T) {
			gw, _ := records(t)
			host := Name + "." + gw.Namespace + ".svc.cluster.local"
			root, cert, key := publicTestCertificate(t, host, time.Now().Add(time.Hour), x509.ExtKeyUsageServerAuth)
			roots := x509.NewCertPool()
			roots.AppendCertsFromPEM(root)
			if scenario == "foreign issuer" {
				root, cert, key = publicTestCertificate(t, host, time.Now().Add(time.Hour), x509.ExtKeyUsageServerAuth)
			}
			if scenario == "missing trust" {
				roots = nil
			}
			if scenario == "private key in certificate" {
				cert = append(cert, key...)
			}
			secret := definition("v1", "Secret", "openshell-server-tls", gw.Metadata.Id)
			secret["type"] = "kubernetes.io/tls"
			meta := secret["metadata"].(object)
			meta["namespace"], meta["uid"], meta["resourceVersion"] = gw.Namespace, "secret-id", "1"
			secret["data"] = object{"tls.crt": base64.StdEncoding.EncodeToString(cert), "tls.key": base64.StdEncoding.EncodeToString(key), "ca.crt": base64.StdEncoding.EncodeToString(root)}
			switch scenario {
			case "foreign owner":
				meta["labels"] = object{ownerLabel: "other", managerLabel: manager}
			case "wrong namespace":
				meta["namespace"] = "other"
			case "missing UID":
				delete(meta, "uid")
			case "deleting":
				meta["deletionTimestamp"] = "2026-09-15T00:00:00Z"
			}
			k := &Kubernetes{internalRoots: roots}
			value, err := k.verifyInternalTLS(secret, gw.Metadata.Id, gw.Namespace, host)
			if scenario == "valid" {
				if err != nil || len(value) == 0 {
					t.Fatal("valid internal certificate rejected", err)
				}
			} else if err == nil || len(value) != 0 {
				t.Fatal("untrusted internal TLS Secret accepted")
			}
		})
	}
}
