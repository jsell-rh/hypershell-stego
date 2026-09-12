package acceptance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"html"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	keycloak "github.com/jsell-rh/hypershell-stego/internal/serviceaccountkeycloak"
	web "github.com/jsell-rh/hypershell-stego/out/application/client"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"github.com/segmentio/ksuid"
	"github.com/twmb/franz-go/pkg/kfake"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// A dedicated cluster fixture can replace Docker. It must use a new test realm.
func browserProvider(t *testing.T) *keycloakFixture {
	t.Helper()
	base := os.Getenv("STEGO_TEST_BROWSER_KEYCLOAK_URL")
	if base == "" {
		return startKeycloak(t)
	}
	ca := os.Getenv("STEGO_TEST_BROWSER_KEYCLOAK_CA_FILE")
	client, err := web.New(web.Options{BaseURL: base, CAFile: ca})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	secret := filepath.Join(t.TempDir(), "admin-secret")
	if err := os.WriteFile(secret, []byte("acceptance-only-admin-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	certificate := os.Getenv("STEGO_TEST_BROWSER_KEYCLOAK_CERT_FILE")
	if certificate == "" {
		certificate = ca
	}
	return &keycloakFixture{certificate: certificate, options: keycloak.Options{ServerURL: base, Realm: "workflow", ClientID: "provisioner", SecretFile: secret, CAFile: ca}, http: client}
}
func consoleProgram(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "console")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	args := []string{"build", "-mod=readonly", "-o", binary}
	if raceEnabled {
		args = append(args, "-race")
	}
	args = append(args, "./out")
	command := exec.CommandContext(ctx, "go", args...)
	command.Dir = "../console"
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build console: %v\n%s", err, output)
	}
	return binary
}
func consoleAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.2:0")
	if err != nil {
		t.Fatal(err)
	}
	address := "https://" + listener.Addr().String()
	listener.Close()
	return address
}
func startConsole(t *testing.T, binary, address, dsn, api string, apiCA string, k *keycloakFixture, id testIdentity, secret, key string, telemetry ...string) (func(), func() string) {
	t.Helper()
	target, _ := url.Parse(address)
	dir := filepath.Dir(id.config.CAFile)
	command := exec.Command(binary)
	command.Env = append(os.Environ(), "PORT="+target.Port(), "DATABASE_URL="+dsn,
		"STEGO_HTTP_TLS_CERT="+filepath.Join(dir, "server.pem"), "STEGO_HTTP_TLS_KEY="+filepath.Join(dir, "server-key.pem"),
		"STEGO_BROWSER_ORIGIN="+address, "STEGO_BROWSER_API_URL="+api, "STEGO_BROWSER_API_CA_FILE="+apiCA,
		"STEGO_BROWSER_ISSUER="+k.options.ServerURL+"/realms/workflow", "STEGO_BROWSER_ISSUER_CA_FILE="+k.options.CAFile,
		"STEGO_BROWSER_CLIENT_ID=hypershell-console", "STEGO_BROWSER_CLIENT_SECRET_FILE="+secret, "STEGO_BROWSER_SESSION_KEY_FILE="+key)
	command.Env = append(command.Env, telemetry...)
	output := runtimeOutput{ready: make(chan string, 1), grpcReady: make(chan string, 1)}
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		_ = command.Process.Signal(syscall.SIGTERM)
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("console exit: %v\n%s", err, output.String())
			}
		case <-time.After(12 * time.Second):
			_ = command.Process.Kill()
			<-done
			t.Error("console did not stop")
		}
	}
	t.Cleanup(stop)
	select {
	case <-output.ready:
	case err := <-done:
		stopped = true
		t.Fatalf("console startup: %v\n%s", err, output.String())
	case <-time.After(20 * time.Second):
		stop()
		t.Fatal("console did not start")
	}
	client, err := web.New(web.Options{BaseURL: address, CAFile: id.config.CAFile})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for _, path := range []string{"/livez", "/readyz"} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		for {
			response, err := client.Do(ctx, "GET", path, nil, nil)
			if err != nil || (response.StatusCode != 200 && (path != "/readyz" || response.StatusCode != 503)) {
				cancel()
				t.Fatalf("console probe %s: %d %v", path, response.StatusCode, err)
			}
			if response.StatusCode == 200 {
				break
			}
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				cancel()
				t.Fatal("console did not become ready")
			case <-timer.C:
			}
		}
		cancel()
	}
	return stop, output.String
}

type consoleBrowser struct {
	client *http.Client
	origin string
	csrf   string
}

func newConsoleBrowser(t *testing.T, origin string, caFiles ...string) *consoleBrowser {
	t.Helper()
	roots := x509.NewCertPool()
	for _, name := range caFiles {
		data, err := os.ReadFile(name)
		if err != nil || !roots.AppendCertsFromPEM(data) {
			t.Fatal("invalid browser test CA")
		}
	}
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots}, ResponseHeaderTimeout: 10 * time.Second}
	t.Cleanup(transport.CloseIdleConnections)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &consoleBrowser{origin: origin, client: &http.Client{Transport: transport, Jar: jar, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (b *consoleBrowser) request(t *testing.T, method, address string, body []byte, headers http.Header) web.Response {
	t.Helper()
	request, err := http.NewRequest(method, address, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if headers != nil {
		request.Header = headers.Clone()
	}
	response, err := b.client.Do(request)
	if err != nil {
		t.Fatal("console request failed", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if err != nil || len(data) > 4<<20 {
		t.Fatal("console response exceeded limit")
	}
	return web.Response{StatusCode: response.StatusCode, Header: response.Header, Body: data}
}
func (b *consoleBrowser) session(t *testing.T) web.Response {
	t.Helper()
	response := b.request(t, "GET", b.origin+"/auth/session", nil, nil)
	var value struct {
		Authenticated bool   `json:"authenticated"`
		CSRF          string `json:"csrf_token"`
	}
	if response.StatusCode != 200 || json.Unmarshal(response.Body, &value) != nil || !value.Authenticated || value.CSRF == "" {
		t.Fatal("console session is unavailable", response.StatusCode)
	}
	b.csrf = value.CSRF
	for _, private := range []string{"access_token", "refresh_token", "id_token", "acceptance-only-console-secret"} {
		if bytes.Contains(response.Body, []byte(private)) {
			t.Fatal("private credentials reached browser")
		}
	}
	return response
}
func (b *consoleBrowser) login(t *testing.T, k *keycloakFixture, username string) {
	t.Helper()
	response := b.request(t, "GET", b.origin+"/auth/login?return_to=%2Fgateways%2Fnew", nil, http.Header{"Sec-Fetch-Site": {"same-origin"}})
	if response.StatusCode != 302 {
		t.Fatal("console login did not redirect", response.StatusCode)
	}
	authorization, err := url.Parse(response.Header.Get("Location"))
	provider, _ := url.Parse(k.options.ServerURL)
	if err != nil || authorization.Scheme != provider.Scheme || authorization.Host != provider.Host || authorization.Query().Get("code_challenge_method") != "S256" || authorization.Query().Get("nonce") == "" {
		t.Fatal("unsafe console authorization redirect")
	}
	response = b.request(t, "GET", authorization.String(), nil, nil)
	if response.StatusCode != 200 {
		t.Fatal("provider form failed", response.StatusCode)
	}
	form := ""
	for _, tag := range regexp.MustCompile(`<form\b[^>]*>`).FindAllString(string(response.Body), -1) {
		if strings.Contains(tag, `id="kc-form-login"`) {
			match := regexp.MustCompile(`action="([^"]+)"`).FindStringSubmatch(tag)
			if len(match) == 2 {
				form = html.UnescapeString(match[1])
			}
		}
	}
	target, err := url.Parse(form)
	if err != nil || target.Scheme != provider.Scheme || target.Host != provider.Host || target.User != nil {
		t.Fatal("untrusted provider form")
	}
	response = b.request(t, "POST", target.String(), []byte(url.Values{"username": {username}, "password": {"acceptance-only-user-password"}, "credentialId": {""}}.Encode()), http.Header{"Content-Type": {"application/x-www-form-urlencoded"}})
	callback, err := url.Parse(response.Header.Get("Location"))
	expected, _ := url.Parse(b.origin)
	if response.StatusCode != 302 || err != nil || callback.Scheme != expected.Scheme || callback.Host != expected.Host || callback.Path != "/auth/callback" {
		t.Fatal("provider did not return to console", response.StatusCode)
	}
	response = b.request(t, "GET", callback.String(), nil, http.Header{"Sec-Fetch-Site": {"cross-site"}})
	if response.StatusCode != 303 || response.Header.Get("Location") != "/gateways/new" {
		t.Fatal("console callback failed", response.StatusCode)
	}
	found := false
	for _, cookie := range (&http.Response{Header: response.Header}).Cookies() {
		if cookie.Name == "__Host-Http-stego_session" {
			found = true
			if !cookie.Secure || !cookie.HttpOnly || cookie.Path != "/" || cookie.Domain != "" || cookie.SameSite != http.SameSiteStrictMode {
				t.Fatal("unsafe session cookie")
			}
		}
	}
	if !found {
		t.Fatal("session cookie missing")
	}
	b.session(t)
}
func (b *consoleBrowser) api(t *testing.T, method, path string, body []byte) web.Response {
	t.Helper()
	headers := http.Header{}
	if method != "GET" && method != "HEAD" {
		headers.Set("Origin", b.origin)
		headers.Set("X-CSRF-Token", b.csrf)
		headers.Set("Content-Type", "application/json")
	}
	return b.request(t, method, b.origin+"/api/hypershell/v1"+path, body, headers)
}

func browserSDKWorkflow(t *testing.T, alice, bob *consoleBrowser, ca string, request any) string {
	t.Helper()
	for _, name := range []string{"index.js", "index.d.ts", "package.json"} {
		generated, err := os.ReadFile(filepath.Join("../out/browsertelemetry", name))
		if err != nil {
			t.Fatal(err)
		}
		installed, err := os.ReadFile(filepath.Join("typescript/node_modules/@stego/browser-telemetry", name))
		if err != nil || !bytes.Equal(generated, installed) {
			t.Fatal("browser telemetry fixture does not match generated output; run npm ci with --install-links")
		}
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("Node.js is required for the generated browser SDK workflow")
	}
	origin, _ := url.Parse(alice.origin)
	cookieHeader := func(b *consoleBrowser) string {
		var pairs []string
		for _, cookie := range b.client.Jar.Cookies(origin) {
			pairs = append(pairs, cookie.Name+"="+cookie.Value)
		}
		return strings.Join(pairs, "; ")
	}
	data, err := json.Marshal(map[string]any{"origin": alice.origin, "owner": cookieHeader(alice), "other": cookieHeader(bob), "request": request})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	input, output := filepath.Join(dir, "input.json"), filepath.Join(dir, "output.json")
	if err := os.WriteFile(input, data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "browser_sdk_workflow.mjs", input, output)
	command.Env = append(os.Environ(), "NODE_EXTRA_CA_CERTS="+ca, "NODE_OPTIONS=--max-old-space-size=256")
	if logs, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated browser SDK workflow: %v\n%s", err, logs)
	}
	data, err = os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(data, &result) != nil || result.ID == "" {
		t.Fatal("SDK did not return a Gateway ID")
	}
	return result.ID
}

type renderedBrowser struct {
	Origin    string   `json:"origin"`
	Pins      []string `json:"pins"`
	Session   string   `json:"session"`
	ID        string   `json:"id"`
	directory string
	diagnose  func()
}

func (b *renderedBrowser) run(t *testing.T, phase string) {
	t.Helper()
	input, output := filepath.Join(b.directory, "input.json"), filepath.Join(b.directory, phase+".json")
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(input, data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "node", "browser_rendered_workflow.mjs", input, output, phase)
	command.Env = append(os.Environ(), "NODE_OPTIONS=--max-old-space-size=256")
	if logs, err := command.CombinedOutput(); err != nil {
		if b.diagnose != nil {
			b.diagnose()
		}
		t.Fatalf("rendered browser %s: %v\n%s", phase, err, logs)
	}
	if phase == "verify" || phase == "close" {
		b.Session = ""
	}
	if phase == "create" {
		data, err = os.ReadFile(output)
		if err != nil {
			t.Fatal(err)
		}
		if json.Unmarshal(data, b) != nil || b.ID == "" || b.Session == "" {
			t.Fatal("browser did not return Gateway and session IDs")
		}
	}
}
func newRenderedBrowser(t *testing.T, origin string, certificates ...string) *renderedBrowser {
	t.Helper()
	if os.Getenv("STEGO_REQUIRE_BROWSER") != "1" {
		return nil
	}
	dir := os.Getenv("STEGO_BROWSER_ARTIFACT_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	b := &renderedBrowser{Origin: origin, directory: dir}
	t.Cleanup(func() {
		if b.Session != "" {
			b.run(t, "close")
		}
	})
	for _, name := range certificates {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		block, _ := pem.Decode(data)
		if block == nil {
			t.Fatal("invalid browser certificate")
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
		b.Pins = append(b.Pins, base64.StdEncoding.EncodeToString(hash[:]))
	}
	return b
}

func TestGeneratedBrowserGatewayWorkflow(t *testing.T) {
	runBrowserGatewayWorkflow(t, nil)
}

func runBrowserGatewayWorkflow(t *testing.T, deployment *kubernetesBrowser) {
	var k *keycloakFixture
	if deployment == nil {
		k = browserProvider(t)
	} else {
		k = startKubernetesKeycloak(t, deployment.namespace, deployment.apply, deployment.command)
	}
	settings, _ := k.apiLoginSetup(t)
	var signals *httpDiagnosticCollector
	var telemetry []string
	if deployment == nil {
		signals, telemetry = newHTTPDiagnosticCollector(t)
	} else {
		signals, telemetry = newHTTPDiagnosticCollectorAt(t, deployment.host("fixture"), "0.0.0.0:19093")
	}
	settings = append(settings, telemetry...)
	providerLogs := func() string { return "" }
	restartProvider := func() {}
	if os.Getenv("STEGO_REQUIRE_BROWSER") == "1" {
		key, providerAuth := issuer(t)
		var providerSettings []string
		if deployment == nil {
			values, stop, logs := startRealProvisionerWithLogs(t, k, key, append(providerAuth, telemetry...))
			defer stop()
			providerSettings, providerLogs = values, logs
		} else {
			providerSettings, providerLogs, restartProvider = deployment.startProvisioner(k, key, append(providerAuth, telemetry...))
		}
		settings = append(settings, providerSettings...)
	}
	aliceID := k.human(t, "console-alice")
	k.human(t, "console-bob")
	response := k.adminRequest(t, "GET", "/clients?clientId=hypershell", nil)
	var clients []struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(response.Body, &clients) != nil || len(clients) != 1 {
		t.Fatal("API client missing")
	}
	response = k.adminRequest(t, "GET", "/clients/"+clients[0].ID+"/roles/gateway:creator", nil)
	var role map[string]any
	if json.Unmarshal(response.Body, &role) != nil {
		t.Fatal("creator role missing")
	}
	k.adminRequest(t, "POST", "/users/"+aliceID+"/role-mappings/clients/"+clients[0].ID, []any{role})
	address := ""
	if deployment == nil {
		address = consoleAddress(t)
	} else {
		address = "https://" + deployment.host("hypershell-console") + ":8443"
	}
	audience := map[string]any{"name": "api-audience", "protocol": "openid-connect", "protocolMapper": "oidc-audience-mapper", "config": map[string]string{"included.client.audience": "hypershell", "access.token.claim": "true", "id.token.claim": "false"}}
	roles := map[string]any{"name": "console-roles", "protocol": "openid-connect", "protocolMapper": "oidc-usermodel-client-role-mapper", "config": map[string]string{"usermodel.clientRoleMapping.clientId": "hypershell", "claim.name": "resource_access.hypershell.roles", "jsonType.label": "String", "multivalued": "true", "access.token.claim": "true", "id.token.claim": "true"}}
	k.adminRequest(t, "POST", "/clients", map[string]any{"clientId": "hypershell-console", "protocol": "openid-connect", "publicClient": false, "secret": "acceptance-only-console-secret", "enabled": true, "standardFlowEnabled": true, "directAccessGrantsEnabled": false, "fullScopeAllowed": true, "redirectUris": []string{address + "/auth/callback"}, "defaultClientScopes": []string{"basic", "profile", "roles", "email"}, "attributes": map[string]string{"pkce.code.challenge.method": "S256", "access.token.lifespan": "20", "post.logout.redirect.uris": address + "/auth/logout"}, "protocolMappers": []any{audience, roles}})
	f := database(t)
	var workload *browserGatewayWorkload
	if deployment != nil && os.Getenv("STEGO_TEST_BROWSER_WORKLOAD") == "1" {
		workload, settings = prepareBrowserGatewayWorkload(t, deployment, f, k, settings)
	}
	sessions := databaseSetup(t, false)
	schema, err := os.ReadFile("../console/out/browser/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.db.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	apiHost := "localhost"
	var brokerConfig Config
	if deployment == nil {
		_, brokerConfig = broker(t, identity(t, "localhost"))
	} else {
		apiHost = deployment.host("hypershell")
		host := deployment.host("fixture")
		_, brokerConfig = broker(t, identity(t, host), kfake.ListenFn(func(network, address string) (net.Listener, error) {
			ln, err := net.Listen("tcp", "0.0.0.0:19092")
			if err != nil {
				return nil, err
			}
			return advertisedListener{ln, serviceAddress(host + ":19092")}, nil
		}))
	}
	consumer := kafkaConsumer(t, brokerConfig)
	apiIdentity := identity(t, apiHost)
	dir := filepath.Dir(apiIdentity.config.CAFile)
	settings = append(settings, "STEGO_HTTP_TLS_CERT="+filepath.Join(dir, "server.pem"), "STEGO_HTTP_TLS_KEY="+filepath.Join(dir, "server-key.pem"), "STEGO_GRPC_TLS_CERT="+filepath.Join(dir, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(dir, "server-key.pem"))
	settings = append(settings, "HYPERSHELL_DEFAULT_GATEWAY_RELEASE_ID="+f.release, "HYPERSHELL_DEFAULT_GATEWAY_CLUSTER_ID="+f.cluster)
	apiProgram := ""
	if deployment == nil {
		apiProgram = buildApplication(t)
	}
	startAPI := func(settings ...string) (func(), string, string) {
		if deployment == nil {
			return startBoth(t, apiProgram, f.dsn, brokerConfig, settings...)
		}
		return deployment.startAPI(f, brokerConfig, apiIdentity, settings)
	}
	stopAPI, api, rpc := startAPI(settings...)
	defer func() { stopAPI() }()
	api = strings.Replace(api, "http://", "https://", 1)
	consoleURL, _ := url.Parse(address)
	consoleIdentity := identity(t, consoleURL.Hostname())
	secretFile := filepath.Join(t.TempDir(), "client-secret")
	keyFile := filepath.Join(t.TempDir(), "session-key")
	if err := os.WriteFile(secretFile, []byte("acceptance-only-console-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	oldSessionKey := base64.StdEncoding.EncodeToString(makeRandom(t, 32))
	nextSessionKey := base64.StdEncoding.EncodeToString(makeRandom(t, 32))
	if err := os.WriteFile(keyFile, []byte(oldSessionKey), 0600); err != nil {
		t.Fatal(err)
	}
	writeSessionKeys := func(keys ...string) {
		t.Helper()
		data, err := json.Marshal(map[string]any{"version": 1, "keys": keys})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(keyFile, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	binary := ""
	if deployment == nil {
		binary = consoleProgram(t)
	}
	startBrowser := func() (func(), func() string) {
		if deployment == nil {
			return startConsole(t, binary, address, sessions.dsn, api, apiIdentity.config.CAFile, k, consoleIdentity, secretFile, keyFile, telemetry...)
		}
		return deployment.startConsole(sessions, address, api, apiIdentity.config.CAFile, k, consoleIdentity, secretFile, keyFile, telemetry)
	}
	stop, logs := startBrowser()
	alice := newConsoleBrowser(t, address, consoleIdentity.config.CAFile, k.options.CAFile)
	bob := newConsoleBrowser(t, address, consoleIdentity.config.CAFile, k.options.CAFile)
	alice.login(t, k, "console-alice")
	for _, external := range []string{api, k.options.ServerURL} {
		target, _ := url.Parse(external)
		for _, cookie := range alice.client.Jar.Cookies(target) {
			if strings.HasPrefix(cookie.Name, "__Host-Http-stego_") {
				t.Fatal("console cookie reached another service host")
			}
		}
	}
	bob.login(t, k, "console-bob")
	sdkID := browserSDKWorkflow(t, alice, bob, consoleIdentity.config.CAFile, f.request("browser-sdk-workflow"))
	checkBrowserTraceChain(t, signals)
	var sdkGrants int
	if err := f.db.QueryRow("SELECT count(*) FROM role_bindings b JOIN roles r ON r.id=b.role_id JOIN users u ON u.id=b.user_id WHERE b.gateway_id=$1 AND b.scope='gateway' AND r.name='gateway:owner' AND u.subject=$2", sdkID, aliceID).Scan(&sdkGrants); err != nil || sdkGrants != 1 {
		t.Fatal("SDK Gateway owner grant missing", err)
	}
	if readEvent(t, consumer, sdkID) == "" {
		t.Fatal("SDK Gateway creation lost its event")
	}
	awaitQueueEmpty(t, f)
	body, err := json.Marshal(f.request("browser-workflow"))
	if err != nil {
		t.Fatal(err)
	}
	response = alice.request(t, "POST", address+"/api/hypershell/v1/gateways", body, http.Header{"Origin": {address}, "Content-Type": {"application/json"}})
	if response.StatusCode != 403 {
		t.Fatal("missing CSRF was accepted", response.StatusCode)
	}
	response = bob.api(t, "POST", "/gateways", body)
	if response.StatusCode != 403 {
		t.Fatal("creation role was not enforced", response.StatusCode)
	}
	expectedStatus := 201
	rendered := newRenderedBrowser(t, address, filepath.Join(filepath.Dir(consoleIdentity.config.CAFile), "server.pem"), k.certificate)
	if rendered != nil {
		rendered.diagnose = func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			rows, err := sessions.db.QueryContext(ctx, "SELECT state,count(*),max(extract(epoch from CURRENT_TIMESTAMP-changed_at)) FROM stego_browser_sessions GROUP BY state")
			if err != nil {
				t.Logf("session diagnostic: %v", err)
			} else {
				defer rows.Close()
				for rows.Next() {
					var state string
					var count int
					var age float64
					if err := rows.Scan(&state, &count, &age); err != nil {
						t.Log(err)
					} else {
						t.Logf("session state=%s count=%d maximum age=%.2f", state, count, age)
					}
				}
			}
			t.Logf("browser process diagnostics: %s", logs())
		}
		rendered.run(t, "create")
		checkRenderedBrowserSignals(t, signals)
		rendered.run(t, "reload")
		response = alice.api(t, "GET", "/gateways/"+rendered.ID, nil)
		if response.StatusCode != 200 {
			t.Fatal("rendered Gateway cannot be retrieved")
		}
		expectedStatus = 200
	} else {
		response = alice.api(t, "POST", "/gateways", body)
	}
	var gateway httpapi.Gateway
	if response.StatusCode != expectedStatus || json.Unmarshal(response.Body, &gateway) != nil {
		t.Fatal("browser Gateway creation failed", response.StatusCode)
	}
	if _, err := ksuid.Parse(gateway.ID); err != nil || gateway.Kind != "Gateway" || gateway.Href != "/api/hypershell/v1/gateways/"+gateway.ID || gateway.CreatedBy != "console-alice" || (workload == nil && gateway.DatabaseID != f.database) {
		t.Fatal("browser Gateway contract changed")
	}
	var grants int
	if err := f.db.QueryRow("SELECT count(*) FROM role_bindings b JOIN roles r ON r.id=b.role_id JOIN users u ON u.id=b.user_id WHERE b.gateway_id=$1 AND b.scope='gateway' AND r.name='gateway:owner' AND u.subject=$2", gateway.ID, aliceID).Scan(&grants); err != nil || grants != 1 {
		t.Fatal("Gateway owner grant missing", err)
	}
	if readEvent(t, consumer, gateway.ID) == "" {
		t.Fatal("browser creation lost its event")
	}
	awaitQueueEmpty(t, f)
	if workload != nil {
		workload.start(alice, rpc, apiIdentity.config.CAFile, gateway.ID)
	}
	assertAccess := func() {
		t.Helper()
		response := alice.api(t, "GET", "/gateways/"+gateway.ID, nil)
		if response.StatusCode != 200 {
			t.Fatal("owner cannot read Gateway", response.StatusCode)
		}
		response = bob.api(t, "GET", "/gateways/"+gateway.ID, nil)
		if response.StatusCode != 404 {
			t.Fatal("other user can read Gateway", response.StatusCode)
		}
		response = bob.api(t, "GET", "/gateways", nil)
		var list struct {
			Items []json.RawMessage `json:"items"`
		}
		if response.StatusCode != 200 || json.Unmarshal(response.Body, &list) != nil || len(list.Items) != 0 {
			t.Fatal("Gateway list was not filtered")
		}
	}
	assertAccess()
	assertGRPC := func() {
		t.Helper()
		aliceToken, bobToken := k.browserLogin(t, "hypershell", "console-alice"), k.browserLogin(t, "hypershell", "console-bob")
		client, _ := grpcClient(t, rpc, apiIdentity)
		rpcContext, cancelRPC := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelRPC()
		call := func(token string) context.Context {
			return metadata.NewOutgoingContext(rpcContext, metadata.Pairs("authorization", "Bearer "+token))
		}
		if got, err := client.GetGateway(call(aliceToken), &pb.GetGatewayRequest{Id: gateway.ID}); err != nil || got.GetGateway().GetMetadata().GetId() != gateway.ID {
			t.Fatal("gRPC cannot read browser-created Gateway", err)
		}
		if _, err := client.GetGateway(call(bobToken), &pb.GetGatewayRequest{Id: gateway.ID}); status.Code(err) != codes.NotFound {
			t.Fatal("gRPC access differs", err)
		}
	}
	assertGRPC()
	stop()
	before := logs()
	stopAPI()
	apiTarget, _ := url.Parse(api)
	restartSettings := append(append([]string{}, settings...), "PORT="+apiTarget.Port(), "STEGO_GRPC_ADDR="+rpc)
	stopAPI, api, rpc = startAPI(restartSettings...)
	api = strings.Replace(api, "http://", "https://", 1)
	// First add the next read key. Keep the old write key during this rollout.
	writeSessionKeys(oldSessionKey, nextSessionKey)
	stop, logs = startBrowser()
	defer func() { stop() }()
	assertGRPC()
	alice.session(t)
	assertAccess()
	if rendered != nil {
		rendered.run(t, "reload")
	}
	stop()
	before += logs()
	// All instances can now read the next key. Switch the write key.
	writeSessionKeys(nextSessionKey, oldSessionKey)
	stop, logs = startBrowser()
	alice.session(t)
	assertAccess()
	if rendered != nil {
		signals.unavailable.Store(true)
		rendered.run(t, "verify")
		signals.unavailable.Store(false)
	}
	alice.session(t)
	assertAccess()
	// A completed renewal changes the stored encrypted session without login.
	var beforeRefresh []byte
	originURL, _ := url.Parse(address)
	sessionID := ""
	for _, cookie := range alice.client.Jar.Cookies(originURL) {
		if cookie.Name == "__Host-Http-stego_session" {
			sessionID = cookie.Value
		}
	}
	rawID, err := base64.RawURLEncoding.DecodeString(sessionID)
	if err != nil || len(rawID) != 32 {
		t.Fatal("invalid session ID")
	}
	idHash := sha256.Sum256(rawID)
	if err := sessions.db.QueryRow("SELECT payload FROM stego_browser_sessions WHERE id_hash=$1", idHash[:]).Scan(&beforeRefresh); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(25 * time.Second)
	for {
		time.Sleep(time.Second)
		alice.session(t)
		var after []byte
		if err := sessions.db.QueryRow("SELECT payload FROM stego_browser_sessions WHERE id_hash=$1 AND state='active'", idHash[:]).Scan(&after); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(beforeRefresh, after) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("provider token was not renewed")
		}
	}
	assertAccess()
	if rendered != nil {
		runtimeLogs := func() string {
			all := providerLogs() + before + logs()
			if workload != nil {
				for _, output := range workload.outputs {
					all += output()
				}
			}
			if deployment != nil {
				all += string(deployment.command(nil, "logs", "deployment/hypershell", "--tail=10000"))
			}
			return all
		}
		if workload == nil {
			checkRenderedServiceAccounts(t, f, k, aliceID, rendered, alice, restartProvider, runtimeLogs)
		} else {
			workload.check(gateway.ID)
			signals.workers.check(t)
			checkRenderedServiceAccountsOnGateway(t, f, k, gateway.ID, "rendered-browser-workflow", workload.audience(gateway.ID), rendered, alice, restartProvider, runtimeLogs, workload.checkCredential)
		}
	}
	response = alice.request(t, "GET", address+"/auth/logout", nil, nil)
	if response.StatusCode != 200 || !bytes.Contains(response.Body, []byte(`name="csrf_token" value="`+alice.csrf+`"`)) {
		t.Fatal("logout confirmation failed", response.StatusCode)
	}
	assertAccess()
	response = alice.request(t, "POST", address+"/auth/logout", []byte(url.Values{"csrf_token": {alice.csrf}}.Encode()), http.Header{"Origin": {address}, "Content-Type": {"application/x-www-form-urlencoded"}})
	providerLogout := response.Header.Get("Location")
	target, parseErr := url.Parse(providerLogout)
	providerURL, _ := url.Parse(k.options.ServerURL)
	if response.StatusCode != 303 || parseErr != nil || target.Scheme != providerURL.Scheme || target.Host != providerURL.Host || target.Query().Get("client_id") != "hypershell-console" || target.Query().Get("post_logout_redirect_uri") != address+"/auth/logout" || target.Query().Get("id_token_hint") != "" {
		t.Fatal("invalid provider logout redirect", response.StatusCode)
	}
	if response = alice.api(t, "GET", "/gateways/"+gateway.ID, nil); response.StatusCode != 401 {
		t.Fatal("logout retained API access", response.StatusCode)
	}
	var reauth struct {
		Error  string `json:"error"`
		Login  string `json:"login_url"`
		Status int    `json:"statusCode"`
	}
	if json.Unmarshal(response.Body, &reauth) != nil || reauth.Error != "reauth_required" || reauth.Login != "/auth/login" || reauth.Status != 401 {
		t.Fatal("browser reauthentication contract changed")
	}
	// Use the identity provider's confirmation form. No OAuth token enters HTML.
	response = alice.request(t, "GET", providerLogout, nil, nil)
	if response.StatusCode == 200 {
		forms := regexp.MustCompile(`(?s)<form\b[^>]*>.*?</form>`).FindAllString(string(response.Body), -1)
		action := ""
		values := url.Values{}
		for _, form := range forms {
			if !strings.Contains(form, `id="kc-logout-confirm"`) {
				continue
			}
			match := regexp.MustCompile(`action="([^"]+)"`).FindStringSubmatch(form)
			if len(match) == 2 {
				action = html.UnescapeString(match[1])
			}
			for _, input := range regexp.MustCompile(`<input\b[^>]*>`).FindAllString(form, -1) {
				name := regexp.MustCompile(`name="([^"]+)"`).FindStringSubmatch(input)
				value := regexp.MustCompile(`value="([^"]*)"`).FindStringSubmatch(input)
				if len(name) == 2 && len(value) == 2 {
					values.Set(html.UnescapeString(name[1]), html.UnescapeString(value[1]))
				}
			}
		}
		target, parseErr = url.Parse(action)
		if parseErr != nil || target.Scheme != providerURL.Scheme || target.Host != providerURL.Host || target.User != nil {
			t.Fatal("untrusted provider logout form")
		}
		response = alice.request(t, "POST", target.String(), []byte(values.Encode()), http.Header{"Content-Type": {"application/x-www-form-urlencoded"}})
	}
	if response.StatusCode != 302 || response.Header.Get("Location") != address+"/auth/logout" {
		t.Fatal("provider logout did not return to console", response.StatusCode)
	}
	// Login must now show the password form. A retained provider session would redirect.
	alice.login(t, k, "console-alice")
	if workload != nil {
		workload.checkAllocatedDeletion(gateway.ID)
	}
	for _, log := range []string{before, logs()} {
		for _, private := range []string{"acceptance-only-console-secret", "acceptance-only-user-password", "code_verifier", "access_token", "refresh_token", "private-collector-fault", oldSessionKey, nextSessionKey} {
			if strings.Contains(log, private) {
				t.Fatal("private browser data reached process logs")
			}
		}
	}
	t.Log("Generated console passed real Keycloak login, Gateway creation, grants, REST and gRPC access, event delivery, process restart, session key rotation, renewal, and logout")
}

func checkBrowserTraceChain(t *testing.T, collector *httpDiagnosticCollector) {
	t.Helper()
	const traceID = "0af7651916cd43dd8448eb211c80319c"

	spans := map[string]*tracepb.Span{}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case batch := <-collector.traces.received:
			for _, resource := range batch.ResourceSpans {
				for _, scope := range resource.ScopeSpans {
					for _, span := range scope.Spans {
						if hex.EncodeToString(span.TraceId) == traceID {
							spans[hex.EncodeToString(span.SpanId)] = span
						}
					}
				}
			}
		case <-timer.C:
			t.Fatal("browser, backend client, and API spans did not form one trace")
		}
		for _, api := range spans {
			if api.Kind != tracepb.Span_SPAN_KIND_SERVER || api.Name != "POST /api/hypershell/v1/gateways" {
				continue
			}
			client := spans[hex.EncodeToString(api.ParentSpanId)]
			if client == nil || client.Kind != tracepb.Span_SPAN_KIND_CLIENT {
				continue
			}
			browser := spans[hex.EncodeToString(client.ParentSpanId)]
			if browser != nil && browser.Kind == tracepb.Span_SPAN_KIND_SERVER && browser.Name == "POST /api/hypershell/v1/{resource}" && spans[hex.EncodeToString(browser.ParentSpanId)] != nil {
				root := spans[hex.EncodeToString(browser.ParentSpanId)]
				if root.Name != "gateway.workflow.create" || len(root.ParentSpanId) != 0 {
					continue
				}
				checkBrowserLogAndMetric(t, collector)
				return
			}
		}
	}
}

func checkBrowserLogAndMetric(t *testing.T, collector *httpDiagnosticCollector) {
	t.Helper()
	logSeen, metricSeen := false, false
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for !logSeen || !metricSeen {
		select {
		case batch := <-collector.logs.received:
			for _, resource := range batch.ResourceLogs {
				identity := false
				for _, a := range resource.Resource.GetAttributes() {
					if a.Key == "service.name" && a.Value.GetStringValue() == "hypershell-web-console" {
						identity = true
					}
				}
				if !identity {
					continue
				}
				for _, scope := range resource.ScopeLogs {
					for _, record := range scope.LogRecords {
						if record.Body.GetStringValue() == "gateway.created" && hex.EncodeToString(record.TraceId) == "0af7651916cd43dd8448eb211c80319c" {
							logSeen = true
						}
					}
				}
			}
		case batch := <-collector.metrics.received:
			for _, resource := range batch.ResourceMetrics {
				identity := false
				for _, a := range resource.Resource.GetAttributes() {
					if a.Key == "service.name" && a.Value.GetStringValue() == "hypershell-web-console" {
						identity = true
					}
				}
				if !identity {
					continue
				}
				for _, scope := range resource.ScopeMetrics {
					for _, metric := range scope.Metrics {
						if metric.Name == "gateway.created" {
							for _, point := range metric.GetSum().GetDataPoints() {
								if point.GetAsDouble() == 1 || point.GetAsInt() == 1 {
									metricSeen = true
								}
							}
						}
					}
				}
			}
		case <-timer.C:
			t.Fatal("browser log and metric did not reach the collector")
		}
	}
}

func checkRenderedBrowserSignals(t *testing.T, collector *httpDiagnosticCollector) {
	t.Helper()
	spans := map[string]*tracepb.Span{}
	logTraces := map[string]bool{}
	metricSeen := false
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case batch := <-collector.traces.received:
			for _, resource := range batch.ResourceSpans {
				for _, scope := range resource.ScopeSpans {
					for _, span := range scope.Spans {
						spans[hex.EncodeToString(span.SpanId)] = span
					}
				}
			}
		case batch := <-collector.logs.received:
			for _, resource := range batch.ResourceLogs {
				if signalAttribute(resource.Resource.GetAttributes(), "service.name").GetStringValue() != "hypershell-web-console" {
					continue
				}
				for _, scope := range resource.ScopeLogs {
					for _, record := range scope.LogRecords {
						if record.Body.GetStringValue() == "gateway.workflow.completed" && signalAttribute(record.Attributes, "gateway.action").GetStringValue() == "provision" && signalAttribute(record.Attributes, "gateway.outcome").GetStringValue() == "succeeded" {
							logTraces[hex.EncodeToString(record.TraceId)] = true
						}
					}
				}
			}
		case batch := <-collector.metrics.received:
			for _, resource := range batch.ResourceMetrics {
				if signalAttribute(resource.Resource.GetAttributes(), "service.name").GetStringValue() != "hypershell-web-console" {
					continue
				}
				for _, scope := range resource.ScopeMetrics {
					for _, metric := range scope.Metrics {
						if metric.Name != "gateway.probes" {
							continue
						}
						for _, point := range metric.GetSum().GetDataPoints() {
							if signalAttribute(point.Attributes, "gateway.action").GetStringValue() == "provision" && signalAttribute(point.Attributes, "gateway.outcome").GetStringValue() == "succeeded" && (point.GetAsDouble() > 0 || point.GetAsInt() > 0) {
								metricSeen = true
							}
						}
					}
				}
			}
		case <-deadline.C:
			t.Fatal("rendered Gateway did not deliver its trace, log, and metric")
		}
		for _, api := range spans {
			if api.Kind != tracepb.Span_SPAN_KIND_SERVER || api.Name != "POST /api/hypershell/v1/gateways" {
				continue
			}
			client := spans[hex.EncodeToString(api.ParentSpanId)]
			if client == nil || client.Kind != tracepb.Span_SPAN_KIND_CLIENT {
				continue
			}
			backend := spans[hex.EncodeToString(client.ParentSpanId)]
			if backend == nil || backend.Kind != tracepb.Span_SPAN_KIND_SERVER {
				continue
			}
			dependency := spans[hex.EncodeToString(backend.ParentSpanId)]
			if dependency == nil || dependency.Name != "gateway.dependency.provision" {
				continue
			}
			root := spans[hex.EncodeToString(dependency.ParentSpanId)]
			if root != nil && root.Name == "gateway.workflow.provision" && len(root.ParentSpanId) == 0 && bytes.Equal(root.TraceId, api.TraceId) && logTraces[hex.EncodeToString(root.TraceId)] && metricSeen {
				return
			}
		}
	}
}
