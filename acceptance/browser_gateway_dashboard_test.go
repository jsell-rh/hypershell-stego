package acceptance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
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
	_, err = checkDashboardRoute(context.Background(), probe.client, origin)
	if err != nil {
		w.t.Fatal(err)
	}
	w.t.Log("Dashboard route passed verified HTTPS and protected-document redirect checks from the browser fixture")
	if os.Getenv("STEGO_TEST_BROWSER_PUBLIC_CA_SHA256") != fmt.Sprintf("%x", sha256.Sum256([]byte(w.public.CA))) {
		w.t.Fatal("browser fixture does not declare the expected public CA")
	}
	// The fixture CA remains trusted when namespace recovery issues a new leaf.
	// Keep the separate identity fixture's existing leaf pin.
	browser := newRenderedBrowser(w.t, origin, w.identity.certificate)
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
				return fmt.Errorf("dashboard fixture HTTPS request failed: %s (%s)", check.path, dashboardProbeFailure(err))
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

// Keep diagnostics useful without printing URLs or arbitrary transport errors.
func dashboardProbeFailure(err error) string {
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var authority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var certificate x509.CertificateInvalidError
	switch {
	case errors.As(err, &authority):
		return "certificate-authority"
	case errors.As(err, &hostname):
		return "certificate-hostname"
	case errors.As(err, &certificate):
		return "certificate-invalid"
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return "dns"
	}
	var network net.Error
	if errors.As(err, &network) && network.Timeout() {
		return "timeout"
	}
	var operation *net.OpError
	if errors.As(err, &operation) {
		return "connection"
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return "connection-closed"
	}
	return "transport"
}
