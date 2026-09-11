package acceptance

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/out/sdk"
)

func TestGeneratedSDKPreservesNullableAccountFields(t *testing.T) {
	for _, source := range []string{
		`{"name":"nullable"}`,
		`{"name":"nullable","description":null}`,
		`{"name":"nullable","description":""}`,
		`{"name":"nullable","description":"account description"}`,
	} {
		var input sdk.CreateGatewayServiceAccountJSONRequestBody
		if err := json.Unmarshal([]byte(source), &input); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		var want, got map[string]json.RawMessage
		if err := json.Unmarshal([]byte(source), &want); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(encoded, &got); err != nil {
			t.Fatal(err)
		}
		if string(want["description"]) != string(got["description"]) {
			t.Fatalf("SDK changed description presence or value: want %s, got %s", want["description"], got["description"])
		}
	}
}

func TestGeneratedSDKServiceAccountWorkflow(t *testing.T) {
	f := database(t)
	provider := newAccountProvider()
	_, gateway := accountService(t, f, provider)
	key, settings := issuer(t)
	rpcSettings, _ := startAccountProvisioner(t, provider, key, settings)
	settings = append(settings, rpcSettings...)
	_, config := broker(t, identity(t, "localhost"))
	binary := buildApplication(t)
	stop, address := startApplication(t, binary, f.dsn, config, settings...)
	defer func() { stop() }()
	var backend atomic.Value
	backend.Store(address)
	received := make(chan []byte, 4)
	proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/service_accounts") {
			body, err := io.ReadAll(io.LimitReader(r.Body, 4097))
			r.Body.Close()
			if err != nil || len(body) > 4096 {
				t.Error("invalid SDK account request")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			received <- body
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		target, err := url.Parse(backend.Load().(string))
		if err != nil {
			t.Error(err)
			return
		}
		httputil.NewSingleHostReverseProxy(target).ServeHTTP(w, r)
	}))
	proxy.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
	proxy.StartTLS()
	defer proxy.Close()
	ca := filepath.Join(t.TempDir(), "api-ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: proxy.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	clientFor := func(subject string) *sdk.Client {
		t.Helper()
		client, err := sdk.NewClient(sdk.Options{BaseURL: proxy.URL, CAFile: ca, Token: token(t, key, subject)})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(client.Close)
		return client
	}
	client, other := clientFor("alice"), clientFor("mallory")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	ids, secrets := []string{}, []string{}
	for _, source := range []string{
		`{"name":"omitted"}`,
		`{"name":"null","description":null}`,
		`{"name":"empty","description":""}`,
		`{"name":"value","description":"account description"}`,
	} {
		var input sdk.CreateGatewayServiceAccountJSONRequestBody
		if err := json.Unmarshal([]byte(source), &input); err != nil {
			t.Fatal(err)
		}
		created, err := client.CreateGatewayServiceAccountWithResponse(ctx, gateway.ID, input)
		if err != nil || created.JSON201 == nil {
			t.Fatal("SDK account creation failed", err)
		}
		var want, got map[string]json.RawMessage
		if err := json.Unmarshal([]byte(source), &want); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(<-received, &got); err != nil {
			t.Fatal(err)
		}
		if string(want["description"]) != string(got["description"]) {
			t.Fatal("SDK changed nullable input on the wire")
		}
		row := created.JSON201
		if row.Id == "" || row.GatewayId != gateway.ID || row.Credential.ClientSecret == nil || *row.Credential.ClientSecret == "" || !row.LastError.IsNull() || !row.RevokedAt.IsNull() {
			t.Fatal("SDK lost account creation fields")
		}
		if input.Description.IsSpecified() && !input.Description.IsNull() {
			value, err := row.Description.Get()
			expected, _ := input.Description.Get()
			if err != nil || value != expected {
				t.Fatal("SDK lost account description value")
			}
		} else if !row.Description.IsNull() {
			t.Fatal("SDK lost explicit null in the account response")
		}
		ids = append(ids, row.Id)
		secrets = append(secrets, *row.Credential.ClientSecret)
	}
	checkReads := func() {
		t.Helper()
		list, err := client.ListGatewayServiceAccountsWithResponse(ctx, gateway.ID, nil)
		if err != nil || list.JSON200 == nil || len(list.JSON200.Items) != len(ids) {
			t.Fatal("SDK lost the account list", err)
		}
		listed := map[string]bool{}
		for _, row := range list.JSON200.Items {
			if listed[row.Id] || !row.RevokedAt.IsNull() || !row.LastError.IsNull() {
				t.Fatal("SDK account list changed identity or null fields")
			}
			listed[row.Id] = true
		}
		for i, id := range ids {
			got, err := client.GetGatewayServiceAccountWithResponse(ctx, gateway.ID, id)
			if err != nil || got.JSON200 == nil || got.JSON200.Id != id || !got.JSON200.RevokedAt.IsNull() || !got.JSON200.LastError.IsNull() {
				t.Fatal("SDK account read failed", err)
			}
			if !listed[id] {
				t.Fatal("SDK account list lost a stored identity")
			}
			if i < 2 {
				if !got.JSON200.Description.IsNull() {
					t.Fatal("SDK read lost a null description")
				}
			} else {
				want := ""
				if i == 3 {
					want = "account description"
				}
				value, err := got.JSON200.Description.Get()
				if err != nil || value != want {
					t.Fatal("SDK read lost a stored description")
				}
			}
			for _, body := range [][]byte{got.Body, list.Body} {
				if bytes.Contains(body, []byte(secrets[i])) || bytes.Contains(body, []byte(`"client_secret":`)) {
					t.Fatal("SDK read exposed an account secret")
				}
			}
		}
		denied, err := other.GetGatewayServiceAccountWithResponse(ctx, gateway.ID, ids[0])
		if err != nil || denied.StatusCode() != 404 || denied.JSON404 == nil {
			t.Fatal("SDK lost denied account response", err)
		}
	}
	checkReads()
	stop()
	stop, address = startApplication(t, binary, f.dsn, config, settings...)
	backend.Store(address)
	checkReads()
	for _, id := range ids {
		revoked, err := client.RevokeGatewayServiceAccountWithResponse(ctx, gateway.ID, id)
		if err != nil || revoked.JSON200 == nil {
			t.Fatal("SDK account revocation failed", err)
		}
		when, err := revoked.JSON200.RevokedAt.Get()
		if err != nil || when.IsZero() || revoked.JSON200.Status != sdk.OpenShellGatewayServiceAccountStatusRevoked {
			t.Fatal("SDK lost the revocation time")
		}
		deleted, err := client.DeleteGatewayServiceAccountWithResponse(ctx, gateway.ID, id)
		if err != nil || deleted.StatusCode() != 204 {
			t.Fatal("SDK account deletion failed", err)
		}
	}
}
