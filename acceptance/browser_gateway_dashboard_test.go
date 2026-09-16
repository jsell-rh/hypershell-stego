package acceptance

import (
	"encoding/json"
	"os"
	"path/filepath"

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
