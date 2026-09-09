package acceptance

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"html"
	"io"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	keycloak "github.com/jsell-rh/hypershell-stego/internal/serviceaccountkeycloak"
	web "github.com/jsell-rh/hypershell-stego/out/application/client"
	"github.com/jsell-rh/hypershell-stego/out/auth"
)

func (k *keycloakFixture) adminRequest(t *testing.T, method, path string, value any) web.Response {
	t.Helper()
	response, grant := k.issue(t, "provisioner", "acceptance-only-admin-secret")
	if response.StatusCode != 200 {
		t.Fatal("fixture administrator login failed")
	}
	var body []byte
	var err error
	if value != nil {
		body, err = json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
	}
	response, err = k.http.Do(context.Background(), method, "/admin/realms/workflow"+path, http.Header{"Authorization": {"Bearer " + grant["access_token"].(string)}, "Content-Type": {"application/json"}}, body)
	if err != nil || response.StatusCode >= 300 {
		t.Fatalf("fixture administration %s %s: %d %v", method, path, response.StatusCode, err)
	}
	return response
}

func (k *keycloakFixture) human(t *testing.T, name string) string {
	t.Helper()
	response := k.adminRequest(t, "POST", "/users", map[string]any{"username": name, "email": name + "@example.test", "emailVerified": true, "firstName": name, "lastName": "Example", "enabled": true, "credentials": []any{map[string]any{"type": "password", "value": "acceptance-only-user-password", "temporary": false}}})
	location, err := url.Parse(response.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(location.Path, "/")
	id := parts[len(parts)-1]
	if id == "" {
		t.Fatal("fixture user has no ID")
	}
	return id
}

// browserLogin completes the provider's browser form and a PKCE S256 exchange.
// Passwords go only to the provider form. The OAuth password grant is not used.
func (k *keycloakFixture) browserLogin(t *testing.T, clientID, username string) string {
	t.Helper()
	verifier := base64.RawURLEncoding.EncodeToString(makeRandom(t, 32))
	challenge := sha256.Sum256([]byte(verifier))
	state := base64.RawURLEncoding.EncodeToString(makeRandom(t, 24))
	callback := "http://127.0.0.1:7777/callback"
	query := url.Values{"client_id": {clientID}, "redirect_uri": {callback}, "response_type": {"code"}, "scope": {"openid"}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}, "state": {state}}
	ca, err := os.ReadFile(k.options.CAFile)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("invalid browser CA")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots}, Proxy: nil, ResponseHeaderTimeout: 5 * time.Second}
	defer transport.CloseIdleConnections()
	browser := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	jar, _ := cookiejar.New(nil)
	origin, _ := url.Parse(k.options.ServerURL)
	request := func(method, path string, body []byte) web.Response {
		t.Helper()
		target, _ := url.Parse(k.options.ServerURL + path)
		headers := http.Header{}
		for _, cookie := range jar.Cookies(target) {
			headers.Add("Cookie", cookie.Name+"="+cookie.Value)
		}
		if body != nil {
			headers.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		request, err := http.NewRequest(method, k.options.ServerURL+path, strings.NewReader(string(body)))
		if err != nil {
			t.Fatal(err)
		}
		request.Header = headers
		result, err := browser.Do(request)
		if err != nil {
			t.Fatal("browser request failed", err)
		}
		data, err := io.ReadAll(io.LimitReader(result.Body, (1<<20)+1))
		result.Body.Close()
		if err != nil || len(data) > 1<<20 {
			t.Fatal("browser response exceeds its limit", err)
		}
		response := web.Response{StatusCode: result.StatusCode, Header: result.Header, Body: data}
		jar.SetCookies(target, (&http.Response{Header: response.Header}).Cookies())
		return response
	}
	response := request("GET", "/realms/workflow/protocol/openid-connect/auth?"+query.Encode(), nil)
	if response.StatusCode != 200 {
		t.Fatalf("browser authorization: %d, redirect error=%v, disabled client=%v", response.StatusCode, strings.Contains(string(response.Body), "redirect_uri"), strings.Contains(string(response.Body), "disabled"))
	}
	form := ""
	for _, tag := range regexp.MustCompile(`<form\b[^>]*>`).FindAllString(string(response.Body), -1) {
		if strings.Contains(tag, `id="kc-form-login"`) {
			matches := regexp.MustCompile(`action="([^"]+)"`).FindStringSubmatch(tag)
			if len(matches) == 2 {
				form = html.UnescapeString(matches[1])
			}
		}
	}
	target, err := url.Parse(form)
	if err != nil || target.Scheme != origin.Scheme || target.Host != origin.Host || target.User != nil {
		t.Fatal("provider login form is absent or outside the trusted origin")
	}
	response = request("POST", target.RequestURI(), []byte(url.Values{"username": {username}, "password": {"acceptance-only-user-password"}, "credentialId": {""}}.Encode()))
	redirect, err := url.Parse(response.Header.Get("Location"))
	if response.StatusCode != 302 || err != nil || redirect.Scheme != "http" || redirect.Host != "127.0.0.1:7777" || redirect.Path != "/callback" || redirect.Query().Get("state") != state || redirect.Query().Get("code") == "" {
		t.Fatalf("browser login did not return an authorization code: %d", response.StatusCode)
	}
	response = request("POST", "/realms/workflow/protocol/openid-connect/token", []byte(url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "redirect_uri": {callback}, "code": {redirect.Query().Get("code")}, "code_verifier": {verifier}}.Encode()))
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if response.StatusCode != 200 || json.Unmarshal(response.Body, &token) != nil || token.AccessToken == "" {
		t.Fatalf("PKCE exchange: %d", response.StatusCode)
	}
	return token.AccessToken
}

func (k *keycloakFixture) apiLoginSetup(t *testing.T) ([]string, []byte) {
	t.Helper()
	audience := map[string]any{"name": "api-audience", "protocol": "openid-connect", "protocolMapper": "oidc-audience-mapper", "config": map[string]string{"included.client.audience": "hypershell", "access.token.claim": "true", "id.token.claim": "false"}}
	response := k.adminRequest(t, "POST", "/clients", map[string]any{"clientId": "hypershell", "protocol": "openid-connect", "publicClient": true, "enabled": true, "standardFlowEnabled": true, "directAccessGrantsEnabled": false, "fullScopeAllowed": true, "redirectUris": []string{"http://127.0.0.1:7777/callback"}, "defaultClientScopes": []string{"basic", "profile", "roles"}, "attributes": map[string]string{"pkce.code.challenge.method": "S256"}, "protocolMappers": []any{audience}})
	location, _ := url.Parse(response.Header.Get("Location"))
	clientPath := strings.TrimPrefix(location.Path, "/admin/realms/workflow")
	k.adminRequest(t, "POST", clientPath+"/roles", map[string]string{"name": "gateway:creator"})
	response, err := k.http.Do(context.Background(), "GET", "/realms/workflow/protocol/openid-connect/certs", nil, nil)
	if err != nil || response.StatusCode != 200 {
		t.Fatal("read provider keys")
	}
	var document struct {
		Keys []struct{ N, E, Alg, Use string }
	}
	if json.Unmarshal(response.Body, &document) != nil {
		t.Fatal("invalid provider keys")
	}
	var key *rsa.PublicKey
	for _, entry := range document.Keys {
		if entry.Alg != "RS256" || entry.Use != "sig" {
			continue
		}
		n, err := base64.RawURLEncoding.DecodeString(entry.N)
		if err != nil {
			t.Fatal(err)
		}
		e, err := base64.RawURLEncoding.DecodeString(entry.E)
		if err != nil {
			t.Fatal(err)
		}
		exponent := 0
		for _, b := range e {
			exponent = exponent*256 + int(b)
		}
		if key != nil {
			t.Fatal("fixture has more than one active RS256 key")
		}
		key = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: exponent}
	}
	if key == nil {
		t.Fatal("fixture has no RS256 key")
	}
	encoded, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "provider-key.pem")
	if err := os.WriteFile(file, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: encoded}), 0600); err != nil {
		t.Fatal(err)
	}
	return []string{"STEGO_AUTH_PUBLIC_KEY_FILE=" + file, "STEGO_AUTH_ISSUER=" + k.options.ServerURL + "/realms/workflow", "STEGO_AUTH_AUDIENCE=hypershell", "STEGO_AUTH_ROLES_CLAIM=resource_access.hypershell.roles"}, response.Body
}

func TestGatewayUserLoginFollowsStoredGrants(t *testing.T) {
	k := startKeycloakConfigured(t, func(realm map[string]any) { realm["editUsernameAllowed"] = true })
	settings, jwks := k.apiLoginSetup(t)
	aliceID, bobID, controllerID := k.human(t, "alice"), k.human(t, "bob"), k.human(t, "controller")
	response := k.adminRequest(t, "GET", "/clients?clientId=hypershell", nil)
	var clients []struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(response.Body, &clients) != nil || len(clients) != 1 {
		t.Fatal("find API login client")
	}
	response = k.adminRequest(t, "GET", "/clients/"+clients[0].ID+"/roles/gateway:creator", nil)
	var creator map[string]any
	if json.Unmarshal(response.Body, &creator) != nil {
		t.Fatal("read creator role")
	}
	k.adminRequest(t, "POST", "/users/"+aliceID+"/role-mappings/clients/"+clients[0].ID, []any{creator})
	alice, bob, controllerToken := k.browserLogin(t, "hypershell", "alice"), k.browserLogin(t, "hypershell", "bob"), k.browserLogin(t, "hypershell", "controller")
	apiIdentity, err := auth.VerifyWithJWKS(auth.Config{Issuer: k.options.ServerURL + "/realms/workflow", Audience: "hypershell", RolesClaim: "resource_access.hypershell.roles"}, alice, jwks)
	if err != nil || apiIdentity.UserID != aliceID || apiIdentity.Username != "alice" || !equalStringSet(apiIdentity.Roles, []string{"gateway:creator"}) {
		t.Fatal("real API identity or roles are incorrect", err)
	}

	f := database(t)
	tlsIdentity := identity(t, "localhost")
	dir := filepath.Dir(tlsIdentity.config.CAFile)
	allowed, _ := json.Marshal([]string{controllerID})
	settings = append(settings, "HYPERSHELL_CONTROL_PLANE_SUBJECTS="+string(allowed), "STEGO_GRPC_TLS_CERT="+filepath.Join(dir, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(dir, "server-key.pem"))
	_, config := broker(t, identity(t, "localhost"))
	apiBinary, controllerBinary := buildApplication(t), buildProgram(t, "./cmd/gateway-identity-controller")
	stopAPI, address, grpcAddress := startBoth(t, apiBinary, f.dsn, config, settings...)
	defer stopAPI()
	root := address + "/api/hypershell/v1"
	body, err := json.Marshal(f.request("user-login"))
	if err != nil {
		t.Fatal(err)
	}
	code, body := requestJSON(t, "POST", root+"/gateways", alice, body)
	var gateway struct {
		ID string `json:"id"`
	}
	if code != 201 || json.Unmarshal(body, &gateway) != nil {
		t.Fatalf("create Gateway with provider token: %d %s", code, body)
	}
	if code, _ := requestJSON(t, "GET", root+"/gateways", bob, nil); code != 200 {
		t.Fatal("register viewer identity", code)
	}
	stopController, logs := startIdentityController(t, controllerBinary, k, grpcAddress, tlsIdentity.config.CAFile, controllerToken)
	defer stopController()
	gatewayClient, _ := keycloak.GatewayClientID(gateway.ID)
	deadline := time.Now().Add(15 * time.Second)
	for {
		code, body := requestJSON(t, "GET", root+"/gateways/"+gateway.ID, alice, nil)
		var current struct {
			OIDC *string `json:"oidc"`
		}
		if code == 200 && json.Unmarshal(body, &current) == nil && current.OIDC != nil && strings.Contains(*current.OIDC, gatewayClient) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Gateway login configuration was not published\n%s", logs())
		}
		time.Sleep(20 * time.Millisecond)
	}

	verify := func(raw string) auth.Identity {
		t.Helper()
		value, err := auth.VerifyWithJWKS(auth.Config{Issuer: k.options.ServerURL + "/realms/workflow", Audience: gatewayClient, RolesClaim: "hypershell.roles"}, raw, jwks)
		if err != nil {
			t.Fatal("verify Gateway login token", err)
		}
		return value
	}
	waitRoles := func(username, subject string, want []string) string {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for {
			raw := k.browserLogin(t, gatewayClient, username)
			identity := verify(raw)
			if identity.UserID != subject {
				t.Fatal("Gateway login changed subject")
			}
			if equalStringSet(identity.Roles, want) {
				return raw
			}
			if time.Now().After(deadline) {
				t.Fatalf("Gateway roles did not converge for %s: %v\n%s", username, identity.Roles, logs())
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	ownerGatewayToken := waitRoles("alice", aliceID, []string{keycloak.RoleAdmin, keycloak.RoleUser})
	if _, err := auth.VerifyWithJWKS(auth.Config{Issuer: k.options.ServerURL + "/realms/workflow", Audience: "hypershell", RolesClaim: "hypershell.roles"}, ownerGatewayToken, jwks); err == nil {
		t.Fatal("Gateway token carries API audience authority")
	}
	if code, _ := requestJSON(t, "GET", root+"/gateways", ownerGatewayToken, nil); code != 401 {
		t.Fatal("Gateway token reached the API", code)
	}
	waitRoles("bob", bobID, nil)
	grant := gateways.GrantRequest{GatewayID: gateway.ID, Scope: "gateway"}
	if err := f.db.QueryRow("SELECT id FROM users WHERE issuer=$1 AND subject=$2", k.options.ServerURL+"/realms/workflow", bobID).Scan(&grant.UserID); err != nil {
		t.Fatal(err)
	}
	grant.RoleID = discoverRole(t, root, alice, "gateway:viewer").ID
	body, _ = json.Marshal(grant)
	code, body = requestJSON(t, "POST", root+"/role_bindings", alice, body)
	var binding struct {
		ID string `json:"id"`
	}
	if code != 201 || json.Unmarshal(body, &binding) != nil {
		t.Fatal("create viewer grant", code)
	}
	oldToken := waitRoles("bob", bobID, []string{keycloak.RoleUser})
	// A profile change must not transfer the provider binding to another subject.
	profileResponse := k.adminRequest(t, "GET", "/users/"+bobID, nil)
	var profile map[string]any
	if json.Unmarshal(profileResponse.Body, &profile) != nil {
		t.Fatal("read provider user profile")
	}
	profile["username"] = "renamed-bob"
	profile["email"] = "renamed-bob@example.test"
	k.adminRequest(t, "PUT", "/users/"+bobID, profile)
	replacementID := k.human(t, "bob")
	waitRoles("bob", replacementID, nil)
	waitRoles("renamed-bob", bobID, []string{keycloak.RoleUser})
	// Owner and viewer grants form a union. Removing owner must retain user access.
	ownerGrant := grant
	ownerGrant.RoleID = discoverRole(t, root, alice, "gateway:owner").ID
	encoded, _ := json.Marshal(ownerGrant)
	code, body = requestJSON(t, "POST", root+"/role_bindings", alice, encoded)
	var ownerBinding struct {
		ID string `json:"id"`
	}
	if code != 201 || json.Unmarshal(body, &ownerBinding) != nil {
		t.Fatal("create second owner", code)
	}
	waitRoles("renamed-bob", bobID, []string{keycloak.RoleAdmin, keycloak.RoleUser})
	if code, _ := requestJSON(t, "DELETE", root+"/role_bindings/"+ownerBinding.ID, alice, nil); code != 204 {
		t.Fatal("remove second owner", code)
	}
	waitRoles("renamed-bob", bobID, []string{keycloak.RoleUser})
	// The controller must not remove this user's roles for the API client.
	freshAlice := k.browserLogin(t, "hypershell", "alice")
	current, err := auth.VerifyWithJWKS(auth.Config{Issuer: k.options.ServerURL + "/realms/workflow", Audience: "hypershell", RolesClaim: "resource_access.hypershell.roles"}, freshAlice, jwks)
	if err != nil || !equalStringSet(current.Roles, []string{"gateway:creator"}) {
		t.Fatal("Gateway synchronization changed API roles", err)
	}
	stopController()

	if code, _ := requestJSON(t, "DELETE", root+"/role_bindings/"+binding.ID, alice, nil); code != 204 {
		t.Fatal("remove viewer grant", code)
	}
	// Both processes restart after removal while the controller was offline.
	stopAPI()
	stopAPI, address, grpcAddress = startBoth(t, apiBinary, f.dsn, config, settings...)
	defer stopAPI()
	root = address + "/api/hypershell/v1"
	stopController, logs = startIdentityController(t, controllerBinary, k, grpcAddress, tlsIdentity.config.CAFile, controllerToken)
	defer stopController()
	waitRoles("renamed-bob", bobID, nil)
	waitRoles("bob", replacementID, nil)
	// Signature verification alone cannot invalidate an already issued token.
	if !equalStringSet(verify(oldToken).Roles, []string{keycloak.RoleUser}) {
		t.Fatal("test token unexpectedly changed")
	}
	if code, _ := requestJSON(t, "GET", root+"/gateways/"+gateway.ID, bob, nil); code != 404 {
		t.Fatal("API retained removed access", code)
	}
}
func equalStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	found := map[string]bool{}
	for _, item := range left {
		if found[item] {
			return false
		}
		found[item] = true
	}
	for _, item := range right {
		if !found[item] {
			return false
		}
	}
	return true
}
