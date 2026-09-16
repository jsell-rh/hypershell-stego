package acceptance

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDashboardRouteProbeInspectsRedirectWithoutFollowing(t *testing.T) {
	for _, scenario := range []string{"valid", "foreign redirect", "wrong return path", "oversized response", "wrong readiness", "wrong status", "canceled"} {
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
			client.CheckRedirect = func(*http.Request, []*http.Request) error {
				followed.Add(1)
				return nil
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if scenario == "canceled" {
				cancel()
			}
			err := checkDashboardRoute(ctx, client, server.URL)
			if (err == nil) != (scenario == "valid") {
				t.Fatal("unexpected route probe result", err)
			}
			if followed.Load() != 0 {
				t.Fatal("probe followed a redirect or called the original redirect handler")
			}
		})
	}
}
