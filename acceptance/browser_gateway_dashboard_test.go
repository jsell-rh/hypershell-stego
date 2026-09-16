package acceptance

import (
	"context"
	"encoding/json"
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
	if err := checkDashboardRoute(context.Background(), probe.client, origin); err != nil {
		w.t.Fatal(err)
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

// Inspect redirects without following them. The service client deliberately
// rejects these responses, so this check uses the browser fixture transport.
func checkDashboardRoute(ctx context.Context, browser *http.Client, origin string) error {
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
			return err
		}
	}
	return nil
}
