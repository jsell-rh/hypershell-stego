package acceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	keycloak "github.com/jsell-rh/hypershell-stego/internal/serviceaccountkeycloak"
)

func (w *browserGatewayWorkload) startRenderedDashboard(id string) *renderedBrowser {
	w.t.Helper()
	if w.public == nil {
		return nil
	}
	origin, err := keycloak.GatewayConsoleOrigin(id, w.public.Domain)
	if err != nil {
		w.t.Fatal(err)
	}
	response := w.owner.api(w.t, "GET", "/gateways/"+id, nil)
	var gateway httpapi.Gateway
	if response.StatusCode != 200 || json.Unmarshal(response.Body, &gateway) != nil || gateway.ConsoleAddress == nil || *gateway.ConsoleAddress != origin {
		w.t.Fatal("ready Gateway has no verified console address")
	}
	ca := filepath.Join(w.t.TempDir(), "console-public-ca.pem")
	if err := os.WriteFile(ca, []byte(w.public.CA), 0600); err != nil {
		w.t.Fatal(err)
	}
	// Check the route from the browser fixture, not only from the controller Pod.
	probe := newConsoleBrowser(w.t, origin, ca)
	certificate, err := checkDashboardRoute(context.Background(), probe.client, origin)
	if err != nil {
		w.t.Fatal(err)
	}
	w.t.Log("Dashboard route passed verified HTTPS and protected-document redirect checks from the browser fixture")
	leafFile := filepath.Join(w.t.TempDir(), "verified-console-leaf.pem")
	if err := os.WriteFile(leafFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate}), 0600); err != nil {
		w.t.Fatal("verified dashboard certificate cannot be saved")
	}
	browser := newRenderedBrowser(w.t, origin, leafFile, w.identity.certificate)
	if browser == nil {
		w.t.Fatal("public Gateway workflow requires a rendered browser")
	}
	browser.ID = id
	browser.IdentityOrigin = w.identity.options.ServerURL
	browser.directory = filepath.Join(browser.directory, "gateway-dashboard")
	if err := os.MkdirAll(browser.directory, 0700); err != nil {
		w.t.Fatal(err)
	}
	browser.run(w.t, "dashboard-create")
	return browser
}

// Inspect redirects without following them. The service client deliberately
// rejects these responses, so this check uses the browser fixture transport.
func checkDashboardRoute(ctx context.Context, browser *http.Client, origin string) ([]byte, error) {
	var certificate []byte
	client := *browser
	client.Timeout = 5 * time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	for _, check := range []struct {
		path   string
		status int
	}{{"/readyz", http.StatusOK}, {"/workspaces", http.StatusSeeOther}} {
		err := func() error {
			call, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			request, err := http.NewRequestWithContext(call, http.MethodGet, origin+check.path, nil)
			if err != nil {
				return fmt.Errorf("dashboard fixture HTTPS request is invalid")
			}
			request.Header.Set("Sec-Fetch-Site", "none")
			response, err := client.Do(request)
			if err != nil {
				return fmt.Errorf("dashboard fixture HTTPS request failed: %s", check.path)
			}
			defer response.Body.Close()
			if response.TLS == nil || len(response.TLS.VerifiedChains) == 0 || len(response.TLS.VerifiedChains[0]) == 0 || len(response.TLS.PeerCertificates) == 0 || !response.TLS.VerifiedChains[0][0].Equal(response.TLS.PeerCertificates[0]) {
				return fmt.Errorf("dashboard fixture peer certificate was not verified")
			}
			leaf := response.TLS.PeerCertificates[0].Raw
			if len(leaf) == 0 || len(leaf) > 16<<10 || certificate != nil && !bytes.Equal(certificate, leaf) {
				return fmt.Errorf("dashboard fixture peer certificate changed or exceeds its limit")
			}
			certificate = append([]byte(nil), leaf...)
			body, err := io.ReadAll(io.LimitReader(response.Body, 1025))
			if err != nil || len(body) > 1024 || response.StatusCode != check.status {
				return fmt.Errorf("dashboard fixture HTTPS response differs: %s", check.path)
			}
			if check.path == "/readyz" && string(body) != "ok\n" || check.path == "/workspaces" && response.Header.Get("Location") != "/auth/login?return_to=%2Fworkspaces" {
				return fmt.Errorf("dashboard fixture HTTPS response differs: %s", check.path)
			}
			return nil
		}()
		if err != nil {
			return nil, err
		}
	}
	return certificate, nil
}
