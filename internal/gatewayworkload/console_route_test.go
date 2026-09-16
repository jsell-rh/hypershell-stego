package gatewayworkload

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPublicConsoleProbeRequiresTLSAndAnonymousBoundary(t *testing.T) {
	for _, mode := range []string{"valid", "wrong certificate", "untrusted", "unready", "unexpected health", "authenticated session", "open proxy", "redirect", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != "GET" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Error("probe supplied credentials or an unexpected method")
				}
				switch r.URL.Path {
				case "/readyz":
					if mode == "unready" {
						w.WriteHeader(503)
						return
					}
					if mode == "unexpected health" {
						_, _ = io.WriteString(w, "other process")
						return
					}
					_, _ = io.WriteString(w, "ok\n")
				case "/auth/session":
					if mode == "redirect" {
						w.Header().Set("Location", "https://foreign.example.test")
						w.WriteHeader(302)
						return
					}
					if mode == "authenticated session" {
						_, _ = io.WriteString(w, "{\"authenticated\":true,\"roles\":[]}\n")
						return
					}
					_, _ = io.WriteString(w, "{\"authenticated\":false,\"roles\":[]}\n")
				case "/api/v1/readyz":
					if mode == "open proxy" {
						w.WriteHeader(200)
						return
					}
					w.WriteHeader(401)
				default:
					t.Error("unexpected probe path")
					w.WriteHeader(500)
				}
			}))
			server.Config.ErrorLog = log.New(io.Discard, "", 0)
			server.StartTLS()
			defer server.Close()
			cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
			ca := cert
			if mode == "wrong certificate" || mode == "untrusted" {
				other, leaf, _ := publicTestCertificate(t, "other.example.test", time.Now().Add(time.Hour), x509.ExtKeyUsageServerAuth)
				if mode == "wrong certificate" {
					cert = leaf
				} else {
					ca = other
				}
			}
			file := filepath.Join(t.TempDir(), "public-ca.pem")
			if err := os.WriteFile(file, ca, 0600); err != nil {
				t.Fatal(err)
			}
			k := &Kubernetes{options: Options{PublicCAFile: file}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "canceled" {
				cancel()
			}
			err := k.probePublicConsole(ctx, strings.TrimPrefix(server.URL, "https://"), cert)
			if mode == "valid" {
				if err != nil || calls.Load() != 3 {
					t.Fatal("valid console boundary failed", err, calls.Load())
				}
			} else if err == nil {
				t.Fatal("invalid console boundary accepted")
			}
			if (mode == "wrong certificate" || mode == "untrusted" || mode == "canceled") && calls.Load() != 0 {
				t.Fatal("invalid TLS or canceled call reached HTTP")
			}
		})
	}
}
