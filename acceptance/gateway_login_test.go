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
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	"google.golang.org/grpc/metadata"
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
	return k.browserLoginAt(t, clientID, username, "http://127.0.0.1:7777/callback", "")
}
func (k *keycloakFixture) browserLoginAt(t *testing.T, clientID, username, callback, clientSecret string) string {
	t.Helper()
	expectedCallback, err := url.Parse(callback)
	if err != nil || expectedCallback.User != nil || expectedCallback.RawQuery != "" || expectedCallback.Fragment != "" {
		t.Fatal("invalid fixture callback")
	}
	verifier := base64.RawURLEncoding.EncodeToString(makeRandom(t, 32))
	challenge := sha256.Sum256([]byte(verifier))
	state := base64.RawURLEncoding.EncodeToString(makeRandom(t, 24))
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
	if response.StatusCode != 302 || err != nil || redirect.Scheme != expectedCallback.Scheme || redirect.Host != expectedCallback.Host || redirect.Path != expectedCallback.Path || redirect.User != nil || redirect.Fragment != "" || redirect.Query().Get("state") != state || redirect.Query().Get("code") == "" {
		t.Fatalf("browser login did not return an authorization code: %d", response.StatusCode)
	}
	exchange := url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "redirect_uri": {callback}, "code": {redirect.Query().Get("code")}, "code_verifier": {verifier}}
	if clientSecret != "" {
		exchange.Set("client_secret", clientSecret)
	}
	response = request("POST", "/realms/workflow/protocol/openid-connect/token", []byte(exchange.Encode()))
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
	response := k.adminRequest(t, "POST", "/clients", map[string]any{"clientId": "hypershell", "protocol": "openid-connect", "publicClient": true, "enabled": true, "standardFlowEnabled": true, "directAccessGrantsEnabled": false, "fullScopeAllowed": false, "redirectUris": []string{"http://127.0.0.1:7777/callback"}, "defaultClientScopes": []string{"basic", "profile", "roles"}, "attributes": map[string]string{"pkce.code.challenge.method": "S256"}, "protocolMappers": []any{audience}})
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
	credentialReaderID := k.human(t, "credential-reader")
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
	allowed, _ := json.Marshal([]string{controllerID, credentialReaderID})
	settings = append(settings, "HYPERSHELL_CONTROL_PLANE_SUBJECTS="+string(allowed), "STEGO_GRPC_TLS_CERT="+filepath.Join(dir, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(dir, "server-key.pem"))
	settings = withCleanupGrants(t, settings, cleanupGrant(controllerID, "Gateway", "identity", ""))
	settings = withControllerWriteGrants(t, settings, writeGrant(controllerID, "configure.identity", ""))
	readerGrants, _ := json.Marshal([]auth.Grant{{Issuer: k.options.ServerURL + "/realms/workflow", Subject: credentialReaderID, Resource: "Gateway", Operation: "read.console-client"}})
	settings = append(settings, "HYPERSHELL_PROVIDER_STATE_GRANTS="+string(readerGrants))
	_, config := broker(t, identity(t, "localhost"))
	apiBinary, controllerBinary := buildApplication(t), buildProgram(t, "./out/deploy/workers/gateway-identity")
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
	if gateway.ID == "" {
		t.Fatal("real login did not create a Gateway")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil {
		t.Fatal("invalid Gateway response")
	}
	if _, found := fields["database_id"]; found {
		t.Fatal("real login returned a database catalog field")
	}
	recipient := currentUser(t, root, bob)
	domainPolicy, err := json.Marshal(map[string]string{f.cluster: "console.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	startConsoleController := func() (func(), func() string) {
		return startIdentityControllerWithExit(t, controllerBinary, k, grpcAddress, tlsIdentity.config.CAFile, controllerToken, 0, "HYPERSHELL_GATEWAY_CONSOLE_DOMAINS="+string(domainPolicy))
	}
	stopController, logs := startConsoleController()
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

	_, syncConnection := grpcClient(t, grpcAddress, tlsIdentity)
	syncClient := control.NewGatewayIdentityServiceClient(syncConnection)
	readSync := func() *control.GetGatewayIdentityStateResponse {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		call := metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+controllerToken))
		state, err := observationRead(call, func(ctx context.Context) (*control.GetGatewayIdentityStateResponse, error) {
			return syncClient.GetGatewayIdentityState(ctx, &control.GetGatewayIdentityStateRequest{Id: gateway.ID})
		})
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	waitSync := func() {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for {
			state := readSync()
			condition := state.GetConditions()["identity_users"].GetConditions()["GrantsSynchronized"]
			if condition.GetStatus() == "True" && condition.GetCurrent() && condition.GetObservedGeneration() == state.ResourceGeneration {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("real grant synchronization has no positive condition", condition)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	verify := func(raw string) auth.Identity {
		t.Helper()
		value, err := auth.VerifyWithJWKS(auth.Config{Issuer: k.options.ServerURL + "/realms/workflow", Audience: gatewayClient, RolesClaim: "hypershell.roles"}, raw, jwks)
		if err != nil {
			t.Fatal("verify Gateway login token", err)
		}
		return value
	}
	consoleClient := k.namedIdentityClient(t, "hs-console-"+gateway.ID)
	if consoleClient == nil {
		t.Fatal("console client is absent")
	}
	waitSync()
	readerToken := k.browserLogin(t, "hypershell", "credential-reader")
	consoleSecret := readConsoleCredentialThroughRuntime(t, k, grpcAddress, tlsIdentity.config.CAFile, readerToken, controllerToken, controllerID, settings, gateway.ID, f.cluster)

	consoleOrigin, err := keycloak.GatewayConsoleOrigin(gateway.ID, "console.example.com")
	if err != nil {
		t.Fatal(err)
	}
	consoleLogin := func(username string) string {
		t.Helper()
		return k.browserLoginAt(t, "hs-console-"+gateway.ID, username, consoleOrigin+"/auth/callback", consoleSecret)
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
			consoleToken := consoleLogin(username)
			consoleIdentity := verify(consoleToken)
			if consoleIdentity.UserID != subject {
				t.Fatal("console login changed subject")
			}
			if _, err := auth.VerifyWithJWKS(auth.Config{Issuer: k.options.ServerURL + "/realms/workflow", Audience: "hypershell"}, consoleToken, jwks); err == nil {
				t.Fatal("console token acquired API audience")
			}
			if equalStringSet(identity.Roles, want) && equalStringSet(consoleIdentity.Roles, want) {
				waitSync()
				return raw
			}
			if time.Now().After(deadline) {
				t.Fatalf("Gateway roles did not converge for %s: %v\n%s", username, identity.Roles, logs())
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	ownerGatewayToken := waitRoles("alice", aliceID, []string{keycloak.RoleAdmin, keycloak.RoleUser})
	if code, _ := requestJSON(t, "GET", root+"/gateways", consoleLogin("alice"), nil); code != 401 {
		t.Fatal("console token reached control-plane API", code)
	}
	// Grant synchronization must not add Gateway audiences to a new API token.
	freshAPIToken := k.browserLogin(t, "hypershell", "alice")
	if _, err := auth.VerifyWithJWKS(auth.Config{Issuer: k.options.ServerURL + "/realms/workflow", Audience: "hypershell", RolesClaim: "resource_access.hypershell.roles"}, freshAPIToken, jwks); err != nil {
		t.Fatal("fresh API token is invalid")
	}
	if _, err := auth.VerifyWithJWKS(auth.Config{Issuer: k.options.ServerURL + "/realms/workflow", Audience: gatewayClient, RolesClaim: "hypershell.roles"}, freshAPIToken, jwks); err == nil {
		t.Fatal("API login fixture added Gateway audience authority")
	}
	if _, err := auth.VerifyWithJWKS(auth.Config{Issuer: k.options.ServerURL + "/realms/workflow", Audience: "hypershell", RolesClaim: "hypershell.roles"}, ownerGatewayToken, jwks); err == nil {
		t.Fatal("Gateway token carries API audience authority")
	}
	if code, _ := requestJSON(t, "GET", root+"/gateways", ownerGatewayToken, nil); code != 401 {
		t.Fatal("Gateway token reached the API", code)
	}
	waitRoles("bob", bobID, nil)
	grant := gateways.GrantRequest{GatewayID: gateway.ID, Scope: "gateway"}
	grant.UserID = recipient.ID
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
	// A registered API identity can receive the same explicit Gateway grants.
	// Its API token does not receive a Gateway audience or global roles.
	createdAutomation := k.adminRequest(t, "POST", "/clients", map[string]any{
		"clientId": "gateway-api-automation", "protocol": "openid-connect", "enabled": true,
		"publicClient": false, "clientAuthenticatorType": "client-secret",
		"secret": "acceptance-only-automation-secret", "serviceAccountsEnabled": true,
		"standardFlowEnabled": false, "directAccessGrantsEnabled": false, "fullScopeAllowed": false,
		"defaultClientScopes": []string{"basic", "profile"}, "optionalClientScopes": []string{},
		"protocolMappers": []any{map[string]any{
			"name": "api-audience", "protocol": "openid-connect", "protocolMapper": "oidc-audience-mapper",
			"config": map[string]string{"included.client.audience": "hypershell", "access.token.claim": "true", "id.token.claim": "false"},
		}},
	})
	automationLocation, err := url.Parse(createdAutomation.Header.Get("Location"))
	if err != nil {
		t.Fatal("invalid automation client location")
	}
	automationPath := strings.TrimPrefix(automationLocation.Path, "/admin/realms/workflow")
	automationSubjectResponse := k.adminRequest(t, "GET", automationPath+"/service-account-user", nil)
	var automationSubject struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(automationSubjectResponse.Body, &automationSubject) != nil || automationSubject.ID == "" {
		t.Fatal("automation has no provider subject")
	}
	automationResponse, automationGrant := k.issue(t, "gateway-api-automation", "acceptance-only-automation-secret")
	automationToken, ok := automationGrant["access_token"].(string)
	if automationResponse.StatusCode != 200 || !ok || automationToken == "" {
		t.Fatal("automation API login failed")
	}
	automation := currentUser(t, root, automationToken)
	if automation.Subject != automationSubject.ID || automation.Issuer != k.options.ServerURL+"/realms/workflow" {
		t.Fatal("automation registration changed its provider identity")
	}
	if code, _ := requestJSON(t, "GET", root+"/gateways/"+gateway.ID, automationToken, nil); code != 404 {
		t.Fatal("ungranted automation can read the Gateway", code)
	}
	gatewayLookup := k.adminRequest(t, "GET", "/clients?clientId="+gatewayClient, nil)
	var gatewayClients []struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(gatewayLookup.Body, &gatewayClients) != nil || len(gatewayClients) != 1 || gatewayClients[0].ID == "" {
		t.Fatal("Gateway has no unique provider client")
	}
	checkAutomationRoles := func(want []string) {
		t.Helper()
		waitSync()
		for _, suffix := range []string{"", "/composite"} {
			response := k.adminRequest(t, "GET", "/users/"+automationSubject.ID+"/role-mappings/clients/"+gatewayClients[0].ID+suffix, nil)
			var roles []struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(response.Body, &roles) != nil {
				t.Fatal("invalid automation roles")
			}
			names := make([]string, 0, len(roles))
			for _, role := range roles {
				names = append(names, role.Name)
			}
			if !equalStringSet(names, want) {
				t.Fatalf("automation roles %v; want %v", names, want)
			}
		}
	}
	automationBindings := make([]string, 0, 2)
	for _, role := range []string{"gateway:viewer", "gateway:owner"} {
		encoded, _ := json.Marshal(gateways.GrantRequest{GatewayID: gateway.ID, Scope: "gateway", UserID: automation.ID, RoleID: discoverRole(t, root, alice, role).ID})
		if code, _ := requestJSON(t, "POST", root+"/role_bindings", automationToken, encoded); code != 404 {
			t.Fatal("automation added its own owner or viewer grant", code)
		}
		code, body := requestJSON(t, "POST", root+"/role_bindings", alice, encoded)
		var granted struct {
			ID string `json:"id"`
		}
		if code != 201 || json.Unmarshal(body, &granted) != nil || granted.ID == "" {
			t.Fatal("owner could not grant automation access", code)
		}
		automationBindings = append(automationBindings, granted.ID)
		want := []string{keycloak.RoleUser}
		if role == "gateway:owner" {
			want = append(want, keycloak.RoleAdmin)
		}
		checkAutomationRoles(want)
		if code, _ := requestJSON(t, "GET", root+"/gateways/"+gateway.ID, automationToken, nil); code != 200 {
			t.Fatal("authorized automation cannot read the Gateway", code)
		}
	}
	stopController()
	for _, id := range automationBindings {
		if code, _ := requestJSON(t, "DELETE", root+"/role_bindings/"+id, alice, nil); code != 204 {
			t.Fatal("remove automation grant while controller is stopped", code)
		}
	}

	if code, _ := requestJSON(t, "DELETE", root+"/role_bindings/"+binding.ID, alice, nil); code != 204 {
		t.Fatal("remove viewer grant", code)
	}
	revoked := readSync()
	revokedCondition := revoked.GetConditions()["identity_users"].GetConditions()["GrantsSynchronized"]
	if revokedCondition.GetStatus() != "Unknown" || revokedCondition.GetReason() != "GrantsChanged" || !revokedCondition.GetCurrent() {
		t.Fatal("offline revocation kept grant success", revokedCondition)
	}
	if value := revoked.GetConditions()["identity"].GetConditions()["ClientReady"]; value.GetStatus() != "True" || !value.GetCurrent() {
		t.Fatal("grant invalidation changed client condition", value)
	}
	// Both processes restart after removal while the controller was offline.
	stopAPI()
	stopAPI, address, grpcAddress = startBoth(t, apiBinary, f.dsn, config, settings...)
	defer stopAPI()
	root = address + "/api/hypershell/v1"
	_, syncConnection = grpcClient(t, grpcAddress, tlsIdentity)
	syncClient = control.NewGatewayIdentityServiceClient(syncConnection)
	if value := readSync().GetConditions()["identity_users"].GetConditions()["GrantsSynchronized"]; value.GetStatus() != "Unknown" || value.GetLastTransitionTime() != revokedCondition.GetLastTransitionTime() {
		t.Fatal("API restart lost invalidated grant condition", value)
	}
	// A console placement fault must not block revoked native or console grants.
	goodDomains := domainPolicy
	domainPolicy, err = json.Marshal(map[string]string{f.release: "console.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	stopController, logs = startConsoleController()
	defer stopController()
	waitRoles("renamed-bob", bobID, nil)
	waitRoles("bob", replacementID, nil)
	checkAutomationRoles(nil)
	faultDeadline := time.Now().Add(15 * time.Second)
	for {
		condition := readSync().GetConditions()["identity"].GetConditions()["ClientReady"]
		if condition.GetStatus() == "Unknown" && condition.GetReason() == "IdentityProviderUnavailable" {
			break
		}
		if time.Now().After(faultDeadline) {
			t.Fatal("console placement fault reported client readiness")
		}
		time.Sleep(20 * time.Millisecond)
	}
	stopController()
	domainPolicy = goodDomains
	stopController, logs = startConsoleController()
	defer stopController()
	waitRoles("alice", aliceID, []string{keycloak.RoleAdmin, keycloak.RoleUser})
	if code, _ := requestJSON(t, "GET", root+"/gateways/"+gateway.ID, automationToken, nil); code != 404 {
		t.Fatal("API retained removed automation access after restart", code)
	}
	// Signature verification alone cannot invalidate an already issued token.
	if !equalStringSet(verify(oldToken).Roles, []string{keycloak.RoleUser}) {
		t.Fatal("test token unexpectedly changed")
	}
	if code, _ := requestJSON(t, "GET", root+"/gateways/"+gateway.ID, bob, nil); code != 404 {
		t.Fatal("API retained removed access", code)
	}

	// Global role records follow fresh API claims, including an empty role set.
	readGlobals := func(bearer string, want int) []grantResponse {
		t.Helper()
		code, body := requestJSON(t, "GET", root+"/role_bindings?search="+url.QueryEscape("scope = 'global'"), bearer, nil)
		var result grantListResponse
		if code != 200 || json.Unmarshal(body, &result) != nil || result.Total != int64(want) || len(result.Items) != want {
			t.Fatal("provider global role projection", code, string(body))
		}
		return result.Items
	}
	originalGlobal := readGlobals(freshAlice, 1)[0]
	k.adminRequest(t, "DELETE", "/users/"+aliceID+"/role-mappings/clients/"+clients[0].ID, []any{creator})
	revokedAlice := k.browserLogin(t, "hypershell", "alice")
	request, _ := json.Marshal(f.request("revoked-creator"))
	if code, _ := requestJSON(t, "POST", root+"/gateways", revokedAlice, request); code != 403 {
		t.Fatal("provider role removal retained creation", code)
	}
	readGlobals(revokedAlice, 0)
	if code, _ := requestJSON(t, "GET", root+"/gateways/"+gateway.ID, revokedAlice, nil); code != 200 {
		t.Fatal("provider global removal lost Gateway ownership", code)
	}
	k.adminRequest(t, "POST", "/users/"+aliceID+"/role-mappings/clients/"+clients[0].ID, []any{creator})
	restoredAlice := k.browserLogin(t, "hypershell", "alice")
	restoredGlobal := readGlobals(restoredAlice, 1)[0]
	if restoredGlobal.ID == originalGlobal.ID || restoredGlobal.UserID != originalGlobal.UserID {
		t.Fatal("provider role re-grant changed identity or restored history")
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
