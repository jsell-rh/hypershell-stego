package acceptance

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	model "github.com/jsell-rh/hypershell-stego/out/storage"
)

type responseStatus struct {
	http.ResponseWriter
	status int
}

func (w *responseStatus) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

// delayedKeycloak holds one selected request, then applies it after release.
// The accepted work continues after the caller cancels its connection.
func delayedKeycloak(t *testing.T, upstream *keycloakFixture, operation string) (*keycloakFixture, *atomic.Bool, <-chan struct{}, func(), <-chan int) {
	t.Helper()
	target, err := url.Parse(upstream.options.ServerURL)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := os.ReadFile(upstream.options.CAFile)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("invalid Keycloak CA")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS13}}
	t.Cleanup(transport.CloseIdleConnections)
	proxy := &httputil.ReverseProxy{Transport: transport, Rewrite: func(request *httputil.ProxyRequest) { request.SetURL(target); request.Out.Host = request.In.Host }}
	var armed atomic.Bool
	entered := make(chan struct{})
	release := make(chan struct{})
	completed := make(chan int, 1)
	var once sync.Once
	unlock := func() { once.Do(func() { close(release) }) }
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		delayed := false
		match := (operation == "enable" && r.Method == http.MethodPut) || (operation == "create" && r.Method == http.MethodPost && r.URL.Path == "/admin/realms/workflow/clients")
		if match && armed.Load() {
			body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			r.Body.Close()
			if err != nil {
				http.Error(w, "read failed", 400)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			var update struct {
				Enabled bool `json:"enabled"`
			}
			if json.Unmarshal(body, &update) == nil && (operation == "create" || update.Enabled) && armed.CompareAndSwap(true, false) {
				delayed = true
				close(entered)
				<-release
			}
		}
		if delayed {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Second)
			defer cancel()
			r = r.Clone(ctx)
			output := &responseStatus{ResponseWriter: w}
			proxy.ServeHTTP(output, r)
			completed <- output.status
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(func() { unlock(); server.Close() })
	caFile := filepath.Join(t.TempDir(), "proxy-ca.pem")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	fixture := *upstream
	fixture.options.ServerURL = server.URL
	fixture.options.CAFile = caFile
	return &fixture, &armed, entered, unlock, completed
}

func TestRevocationSurvivesDelayedEnableAfterDatabaseLoss(t *testing.T) {
	k := startKeycloak(t)
	delayed, arm, entered, release, completed := delayedKeycloak(t, k, "enable")
	defer release()
	f := database(t)
	_, gateway := accountService(t, f, newAccountProvider())
	k.bindGateway(t, "gateway-audience", gateway.ID)
	oidc := fmt.Sprintf(`{"issuer":%q,"client_id":"gateway-audience","audience":"gateway-audience"}`, delayed.options.ServerURL+"/realms/workflow")
	if _, err := f.db.Exec("UPDATE gateways SET oidc=$1 WHERE id=$2", oidc, gateway.ID); err != nil {
		t.Fatal(err)
	}
	observeGatewayFixture(t, f, gateway.ID)
	key, settings := issuer(t)
	providerSettings, _ := startRealProvisioner(t, delayed, key, settings)
	settings = append(settings, providerSettings...)
	_, config := broker(t, identity(t, "localhost"))
	binary := buildApplication(t)
	stop, address := startApplication(t, binary, f.dsn, config, settings...)
	path := "/api/hypershell/v1/gateways/" + gateway.ID + "/service_accounts"
	owner := token(t, key, "alice")
	code, data := requestJSON(t, "POST", address+path, owner, []byte(`{"name":"delayed-enable","role":"openshell-admin"}`))
	if code != 201 {
		t.Fatalf("create: %d %s", code, data)
	}
	var created struct {
		ID         string `json:"id"`
		Credential struct {
			Secret string `json:"client_secret"`
		} `json:"credential"`
	}
	if json.Unmarshal(data, &created) != nil || created.Credential.Secret == "" {
		t.Fatal("incomplete creation response")
	}
	stored, err := f.storage.Get(context.Background(), "ServiceAccount", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	row := stored.(model.ServiceAccount)
	arm.Store(true)
	if _, err := f.db.Exec("UPDATE role_bindings SET role_id=(SELECT id FROM roles WHERE name='gateway:viewer') WHERE gateway_id=$1 AND user_id=$2", gateway.ID, row.CreatedByUserID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(20 * time.Second):
		t.Fatal("role repair did not reach its enable request")
	}
	// The database lock protects normal calls, but a lost connection releases it
	// while an external service can still hold an accepted request.
	var pid int
	if err := f.db.QueryRow("SELECT a.pid FROM pg_stat_activity a WHERE datname=current_database() AND state='idle in transaction' AND a.pid<>pg_backend_pid() AND EXISTS (SELECT 1 FROM pg_locks l WHERE l.pid=a.pid AND l.relation='gateways'::regclass AND l.mode='RowShareLock')").Scan(&pid); err != nil {
		t.Fatalf("find held transaction: %v", err)
	}
	var terminated bool
	if err := f.db.QueryRow("SELECT pg_terminate_backend($1)", pid).Scan(&terminated); err != nil || !terminated {
		t.Fatalf("terminate transaction: %v", err)
	}
	code, data = requestJSON(t, "POST", address+path+"/"+created.ID+"/revoke", owner, []byte(`{}`))
	if code != 200 {
		t.Fatalf("revoke after lock loss: %d %s", code, data)
	}
	response, _ := k.issue(t, row.ClientID, created.Credential.Secret)
	if response.StatusCode == 200 {
		t.Fatal("revocation did not stop issuance before delayed work")
	}
	// Stop background recovery so it cannot hide a temporary reactivation.
	stop()
	release()
	select {
	case code := <-completed:
		t.Logf("delayed provider update returned HTTP %d", code)
		if code != http.StatusNotFound {
			t.Fatal("delayed update did not reject the removed identity")
		}
	case <-time.After(12 * time.Second):
		t.Fatal("delayed request did not complete")
	}
	response, _ = k.issue(t, row.ClientID, created.Credential.Secret)
	if response.StatusCode == 200 {
		t.Fatal("delayed enable restored token issuance after committed revocation")
	}
	stored, err = f.storage.Get(context.Background(), "ServiceAccount", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	terminal := stored.(model.ServiceAccount)
	if terminal.Status != "revoked" || terminal.Active || terminal.RevokedAt == nil {
		t.Fatal("revocation metadata was not retained")
	}
	if _, err := f.db.Exec("UPDATE role_bindings SET role_id=(SELECT id FROM roles WHERE name='gateway:owner') WHERE gateway_id=$1 AND user_id=$2", gateway.ID, row.CreatedByUserID); err != nil {
		t.Fatal(err)
	}
	stop, address = startApplication(t, binary, f.dsn, config, settings...)
	defer stop()
	code, _ = requestJSON(t, "GET", address+path+"/"+created.ID, owner, nil)
	if code != 200 {
		t.Fatal("revoked metadata was lost after restart")
	}
	code, _ = requestJSON(t, "POST", address+path+"/"+created.ID+"/revoke", owner, []byte(`{}`))
	if code != 200 {
		t.Fatalf("repeated revoke: %d", code)
	}
	response, _ = k.issue(t, row.ClientID, created.Credential.Secret)
	if response.StatusCode == 200 {
		t.Fatal("restart or restored grant restored revoked credential")
	}
	code, _ = requestJSON(t, "DELETE", address+path+"/"+created.ID, owner, nil)
	if code != 204 {
		t.Fatalf("delete revoked metadata: %d", code)
	}
	var audits int
	if err := f.db.QueryRow("SELECT count(*) FROM service_account_audits WHERE service_account_id=$1", created.ID).Scan(&audits); err != nil || audits == 0 {
		t.Fatal("deletion lost the account audit history")
	}

}
