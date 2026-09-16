package acceptance

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDashboardProbeFailureCategoriesExcludeTransportDetails(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{"canceled", context.Canceled},
		{"timeout", context.DeadlineExceeded},
		{"certificate-authority", x509.UnknownAuthorityError{}},
		{"certificate-hostname", x509.HostnameError{Host: "private.invalid"}},
		{"certificate-invalid", x509.CertificateInvalidError{Detail: "private certificate"}},
		{"dns", &net.DNSError{Name: "private.invalid", Err: "private DNS error"}},
		{"connection", &net.OpError{Op: "dial", Err: errors.New("private endpoint")}},
		{"connection-closed", io.EOF},
		{"connection-closed", io.ErrUnexpectedEOF},
		{"transport", errors.New("private transport error")},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := &url.Error{Op: "Get", URL: "https://private.invalid/?secret=private", Err: test.err}
			if got := dashboardProbeFailure(err); got != test.name {
				t.Fatalf("unexpected bounded failure category: %q", got)
			}
		})
	}
}

func TestDashboardRouteProbeInspectsRedirectWithoutFollowing(t *testing.T) {
	for _, scenario := range []string{"valid", "foreign redirect", "wrong return path", "oversized response", "wrong readiness", "wrong status", "canceled", "unverified peer"} {
		t.Run(scenario, func(t *testing.T) {
			var followed atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Sec-Fetch-Site") != "none" {
					t.Error("probe did not send browser navigation metadata")
				}
				switch r.URL.Path {
				case "/readyz":
					if scenario == "wrong readiness" {
						_, _ = w.Write([]byte("not ready\n"))
					} else {
						_, _ = w.Write([]byte("ok\n"))
					}
				case "/workspaces":
					target := "/auth/login?return_to=%2Fworkspaces"
					if scenario == "foreign redirect" {
						target = "https://unreachable.invalid/auth/login"
					}
					if scenario == "wrong return path" {
						target = "/auth/login?return_to=%2F"
					}
					w.Header().Set("Location", target)
					status := http.StatusSeeOther
					if scenario == "wrong status" {
						status = http.StatusFound
					}
					w.WriteHeader(status)
					if scenario == "oversized response" {
						_, _ = w.Write([]byte(strings.Repeat("x", 1025)))
					}
				default:
					followed.Add(1)
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			defer server.Close()
			client := server.Client()
			if scenario == "unverified peer" {
				transport := client.Transport.(*http.Transport).Clone()
				// This negative test must not produce a usable browser pin.
				transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
				defer transport.CloseIdleConnections()
				client = &http.Client{Transport: transport}
			}
			client.CheckRedirect = func(*http.Request, []*http.Request) error {
				followed.Add(1)
				return nil
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if scenario == "canceled" {
				cancel()
			}
			certificate, err := checkDashboardRoute(ctx, client, server.URL)
			if (err == nil) != (scenario == "valid") {
				t.Fatal("unexpected route probe result", err)
			}
			if err == nil && !bytes.Equal(certificate, server.Certificate().Raw) || err != nil && certificate != nil {
				t.Fatal("probe returned an incorrect or unverified browser certificate")
			}
			if followed.Load() != 0 {
				t.Fatal("probe followed a redirect or called the original redirect handler")
			}
		})
	}
}

func TestDashboardRouteProbeReturnsVerifiedLeafWithoutRootInServerChain(t *testing.T) {
	id := identity(t, "localhost")
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/readyz":
			_, _ = w.Write([]byte("ok\n"))
		case "/workspaces":
			http.Redirect(w, r, "/auth/login?return_to=%2Fworkspaces", http.StatusSeeOther)
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	server.TLS = id.server.Clone()
	server.TLS.MinVersion = tls.VersionTLS13
	server.TLS.ClientAuth = tls.NoClientCert
	server.StartTLS()
	defer server.Close()
	if len(server.TLS.Certificates[0].Certificate) != 1 {
		t.Fatal("fixture must omit the CA from its served chain")
	}
	probe := newConsoleBrowser(t, server.URL, id.config.CAFile)
	certificate, err := checkDashboardRoute(context.Background(), probe.client, server.URL)
	if err != nil || !bytes.Equal(certificate, server.TLS.Certificates[0].Certificate[0]) {
		t.Fatal("probe did not return the CA-verified leaf certificate", err)
	}
}
