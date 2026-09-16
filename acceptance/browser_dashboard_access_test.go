package acceptance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"

	keycloak "github.com/jsell-rh/hypershell-stego/internal/serviceaccountkeycloak"
)

// Use real OAuth sessions and the generated HTTPS backend. These checks add
// no browser process; the owner workflow separately uses the rendered UI.
func (w *browserGatewayWorkload) newDashboardViewer(id string) *consoleBrowser {
	w.t.Helper()
	if w.public == nil {
		return nil
	}
	origin, err := keycloak.GatewayConsoleOrigin(id, w.public.Domain)
	if err != nil {
		w.t.Fatal(err)
	}
	ca := filepath.Join(w.t.TempDir(), "dashboard-ca.pem")
	if err := os.WriteFile(ca, []byte(w.public.CA), 0600); err != nil {
		w.t.Fatal(err)
	}
	browser := newConsoleBrowser(w.t, origin, ca, w.identity.options.CAFile)
	browser.apiPrefix = "/api/v1"
	browser.loginTo(w.t, w.identity, "console-bob", "/workspaces")
	return browser
}

func (w *browserGatewayWorkload) checkDashboardViewer(browser *consoleBrowser, member bool) {
	w.t.Helper()
	if browser == nil {
		return
	}
	response := browser.api(w.t, "GET", "/workspaces", nil)
	var workspaces []struct{ Metadata struct{ Name string } }
	if response.StatusCode != 200 || json.Unmarshal(response.Body, &workspaces) != nil {
		w.t.Fatal("dashboard viewer workspace list failed", response.StatusCode)
	}
	var names, want []string
	for _, workspace := range workspaces {
		names = append(names, workspace.Metadata.Name)
	}
	slices.Sort(names)
	wantRead := 403
	if member {
		want, wantRead = []string{"default"}, 200
	}
	if !slices.Equal(names, want) {
		w.t.Fatal("dashboard workspace list did not follow membership", names)
	}
	if response = browser.api(w.t, "GET", "/workspaces/default", nil); response.StatusCode != wantRead {
		w.t.Fatal("dashboard workspace read did not follow membership", response.StatusCode)
	}
	for _, request := range []struct{ method, path, body string }{
		{"GET", "/workspaces/owner-private", ""},
		{"POST", "/workspaces", `{"name":"dashboard-viewer-denied"}`},
		{"DELETE", "/workspaces/default", ""},
	} {
		if response = browser.api(w.t, request.method, request.path, []byte(request.body)); response.StatusCode != 403 {
			w.t.Fatal("dashboard viewer reached a denied operation", request.method, request.path, response.StatusCode)
		}
	}
}
