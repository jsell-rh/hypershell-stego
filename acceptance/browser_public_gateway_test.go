package acceptance

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"

	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
)

type browserPublicGateway struct {
	Domain    string   `json:"domain"`
	Issuer    string   `json:"issuer"`
	Router    string   `json:"router"`
	CA        string   `json:"ca_pem"`
	Endpoints []string `json:"endpoints"`
	roots     *x509.CertPool
}

func (w *browserGatewayWorkload) preparePublicGateway() {
	w.t.Helper()
	path := os.Getenv("STEGO_TEST_GATEWAY_PUBLIC_CONFIG")
	required := os.Getenv("STEGO_TEST_REQUIRE_PUBLIC_GATEWAY")
	if required != "" && required != "0" && required != "1" {
		w.t.Fatal("invalid public Gateway test requirement")
	}
	if path == "" {
		if required == "1" {
			w.t.Fatal("public Gateway test configuration is missing")
		}
		return
	}
	file, err := os.Open(path)
	if err != nil {
		w.t.Fatal("public Gateway test configuration is unavailable")
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 {
		w.t.Fatal("public Gateway test configuration is too large")
	}
	var config browserPublicGateway
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil || decoder.Decode(new(any)) != io.EOF || config.Domain == "" || config.Issuer == "" || config.Router == "" || len(config.Endpoints) == 0 || len(config.Endpoints) > 16 {
		w.t.Fatal("public Gateway test configuration is invalid")
	}
	config.roots = x509.NewCertPool()
	if !config.roots.AppendCertsFromPEM([]byte(config.CA)) {
		w.t.Fatal("public Gateway test trust is invalid")
	}
	// The fixture renderer checks the names, certificate-only trust, and exact
	// IP/port pairs. The production worker and network renderer check them again.
	w.public = &config
}

func (w *browserGatewayWorkload) publicWorkerSettings(env map[string]string, files map[string][]byte, target []string) []string {
	if w.public == nil {
		return target
	}
	env["HYPERSHELL_GATEWAY_PUBLIC_DOMAIN"] = w.public.Domain
	env["HYPERSHELL_GATEWAY_PUBLIC_ISSUER"] = w.public.Issuer
	env["HYPERSHELL_GATEWAY_PUBLIC_ROUTER"] = w.public.Router
	env["HYPERSHELL_GATEWAY_PUBLIC_CA_FILE"] = "/var/run/stego/public-ca.pem"
	files["public-ca.pem"] = []byte(w.public.CA)
	for _, endpoint := range w.public.Endpoints {
		target = append(target, "--egress", "gateway-public="+endpoint)
	}
	return target
}

func (w *browserGatewayWorkload) requirePublicEndpoint(gateway httpapi.Gateway) {
	w.t.Helper()
	if w.public == nil {
		return
	}
	want := "https://gw-" + gateway.Namespace + "." + w.public.Domain
	if gateway.RouteAddress == nil || *gateway.RouteAddress != want {
		w.t.Fatal("healthy public Gateway has no matching controller address")
	}
}

func (w *browserGatewayWorkload) publicRPCAddress(gateway httpapi.Gateway) string {
	w.requirePublicEndpoint(gateway)
	return net.JoinHostPort("gw-"+gateway.Namespace+"."+w.public.Domain, "443")
}

func (w *browserGatewayWorkload) recordPublicRPC(gateway httpapi.Gateway) {
	w.t.Helper()
	if w.public == nil {
		return
	}
	directory := os.Getenv("STEGO_BROWSER_ARTIFACT_DIR")
	if directory == "" {
		w.t.Fatal("public Gateway evidence directory is missing")
	}
	record := map[string]any{"gateway_id": gateway.ID, "namespace": gateway.Namespace, "address": *gateway.RouteAddress, "router": w.public.Router, "issuer": w.public.Issuer, "verified_tls": true, "owner_provider_write_and_read": true, "unauthenticated_denied": true, "forged_token_denied": true, "foreign_audience_denied": true, "ungranted_user_denied": true, "gateway_pod_replaced": true, "provider_data_preserved": true}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		w.t.Fatal(err)
	}
	if err = os.MkdirAll(directory, 0700); err != nil {
		w.t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(directory, "gateway-public-rpc.json"), append(data, '\n'), 0600); err != nil {
		w.t.Fatal(err)
	}
}
