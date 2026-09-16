package acceptance

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	keycloak "github.com/jsell-rh/hypershell-stego/internal/serviceaccountkeycloak"
	web "github.com/jsell-rh/hypershell-stego/out/application/client"
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
	probe, err := web.New(web.Options{BaseURL: origin, CAFile: ca})
	if err != nil {
		w.t.Fatal("dashboard fixture HTTPS client setup failed")
	}
	defer probe.Close()
	for _, check := range []struct {
		path   string
		status int
	}{{"/readyz", http.StatusOK}, {"/workspaces", http.StatusSeeOther}} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		result, err := probe.Do(ctx, http.MethodGet, check.path, http.Header{"Sec-Fetch-Site": {"none"}}, nil)
		cancel()
		if err != nil || result.StatusCode != check.status {
			w.t.Fatal("dashboard fixture HTTPS route check failed", check.path, result.StatusCode)
		}
		if check.path == "/readyz" && string(result.Body) != "ok\n" || check.path == "/workspaces" && result.Header.Get("Location") != "/auth/login?return_to=%2Fworkspaces" {
			w.t.Fatal("dashboard fixture HTTPS response differs from the generated contract", check.path)
		}
	}
	w.t.Log("Dashboard route passed verified HTTPS and protected-document redirect checks from the browser fixture")
	browser := newRenderedBrowser(w.t, origin, ca, w.identity.certificate)
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
