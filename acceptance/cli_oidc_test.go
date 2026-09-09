package acceptance

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/html"
)

// This browser submits forms only to the test provider. It never uses the
// password grant. The CLI owns PKCE, the callback, and the token exchange.
func providerForms(t *testing.T, k *keycloakFixture, start, username, userCode, callback string) {
	t.Helper()
	ca, err := os.ReadFile(k.options.CAFile)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("invalid provider CA")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots}, Proxy: nil}
	defer transport.CloseIdleConnections()
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Transport: transport, Jar: jar, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	origin, _ := url.Parse(k.options.ServerURL)
	address, method, body := start, "GET", ""
	attr := func(node *html.Node, name string) string {
		for _, a := range node.Attr {
			if a.Key == name {
				return a.Val
			}
		}
		return ""
	}
	for step := 0; step < 12; step++ {
		target, err := url.Parse(address)
		if err != nil {
			t.Fatal(err)
		}
		if target.Scheme != origin.Scheme || target.Host != origin.Host || target.User != nil {
			t.Fatal("browser left the provider origin")
		}
		request, err := http.NewRequest(method, address, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		// Ask for a page, as a browser does. The provider also serves JSON
		// device authorization requests on this resource.
		request.Header.Set("Accept", "text/html")
		if method == "POST" {
			request.Header.Set("Origin", origin.Scheme+"://"+origin.Host)
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		response, err := browser.Do(request)
		if err != nil {
			t.Fatal("provider browser request failed")
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, 256<<10))
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode == 302 || response.StatusCode == 303 {
			next, err := url.Parse(response.Header.Get("Location"))
			if err != nil {
				t.Fatal(err)
			}
			next = target.ResolveReference(next)
			if callback != "" {
				expected, _ := url.Parse(callback)
				if next.Scheme == expected.Scheme && next.Host == expected.Host && next.Path == expected.Path {
					if next.Query().Get("error") != "" {
						t.Fatalf("provider denied browser login: %s %s", next.Query().Get("error"), next.Query().Get("error_description"))
					}
					client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
					returned, err := client.Get(next.String())
					if err != nil {
						t.Fatal("CLI callback failed")
					}
					returned.Body.Close()
					if returned.StatusCode != 200 {
						t.Fatal("CLI rejected the provider callback")
					}
					return
				}
			}
			address, method, body = next.String(), "GET", ""
			continue
		}
		if response.StatusCode != 200 {
			document, _ := html.Parse(bytes.NewReader(data))
			var message string
			var inspect func(*html.Node, bool)
			inspect = func(n *html.Node, inside bool) {
				inside = inside || attr(n, "id") == "kc-error-message" || attr(n, "role") == "alert" || strings.Contains(attr(n, "class"), "alert__title") || strings.HasPrefix(attr(n, "id"), "input-error")
				if inside && n.Type == html.TextNode {
					message += n.Data
				}
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					inspect(c, inside)
				}
			}
			if document != nil {
				inspect(document, false)
			}
			t.Fatalf("provider browser returned HTTP %d at step %d: %s", response.StatusCode, step, strings.TrimSpace(message))
		}
		document, err := html.Parse(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		var form *html.Node
		var find func(*html.Node)
		find = func(n *html.Node) {
			if form != nil {
				return
			}
			if n.Type == html.ElementNode && n.Data == "form" {
				form = n
				return
			}
			for child := n.FirstChild; child != nil; child = child.NextSibling {
				find(child)
			}
		}
		find(document)
		if form == nil {
			if callback != "" {
				t.Fatal("provider did not return a browser callback")
			}
			return
		}
		values := url.Values{}
		var fields func(*html.Node)
		fields = func(n *html.Node) {
			if n.Type == html.ElementNode && (n.Data == "input" || n.Data == "button") {
				name := attr(n, "name")
				kind := attr(n, "type")
				if name != "" && kind != "submit" && name != "cancel" {
					values.Set(name, attr(n, "value"))
				}
				switch name {
				case "username":
					values.Set(name, username)
				case "password":
					values.Set(name, "acceptance-only-user-password")
				case "device_user_code":
					values.Set(name, userCode)
				case "accept":
					values.Set(name, "Yes")
				}
			}
			for child := n.FirstChild; child != nil; child = child.NextSibling {
				fields(child)
			}
		}
		fields(form)
		action, err := url.Parse(attr(form, "action"))
		if err != nil {
			t.Fatal(err)
		}
		address = target.ResolveReference(action).String()
		method = "POST"
		body = values.Encode()
	}
	t.Fatal("provider browser exceeded its form limit")
}

type cliSessionRecord struct {
	OAuth struct {
		AccessToken  string    `json:"access_token"`
		RefreshToken string    `json:"refresh_token"`
		ExpiresAt    time.Time `json:"expires_at"`
		Subject      string    `json:"subject"`
		Pending      bool      `json:"refresh_pending"`
	} `json:"oauth"`
}

func TestGeneratedCLIOIDCWorkflow(t *testing.T) {
	k := startKeycloakConfigured(t, func(realm map[string]any) {
		realm["revokeRefreshToken"] = true
		realm["refreshTokenMaxReuse"] = 0
		realm["oauth2DevicePollingInterval"] = 1
	})
	settings, _ := k.apiLoginSetup(t)
	aliceID, bobID := k.human(t, "alice"), k.human(t, "bob")
	response := k.adminRequest(t, "GET", "/clients?clientId=hypershell", nil)
	var clients []map[string]any
	if json.Unmarshal(response.Body, &clients) != nil || len(clients) != 1 {
		t.Fatal("missing login client")
	}
	client := clients[0]
	clientID := client["id"].(string)
	client["redirectUris"] = []string{"http://127.0.0.1/callback"}
	client["attributes"] = map[string]string{"pkce.code.challenge.method": "S256", "oauth2.device.authorization.grant.enabled": "true", "access.token.lifespan": "10"}
	k.adminRequest(t, "PUT", "/clients/"+clientID, client)
	response = k.adminRequest(t, "GET", "/clients/"+clientID+"/roles/gateway:creator", nil)
	var creator map[string]any
	if json.Unmarshal(response.Body, &creator) != nil {
		t.Fatal("missing creator role")
	}
	k.adminRequest(t, "POST", "/users/"+aliceID+"/role-mappings/clients/"+clientID, []any{creator})
	f := database(t)
	_, brokerConfig := broker(t, identity(t, "localhost"))
	binary := buildApplication(t)
	stop, address := startApplication(t, binary, f.dsn, brokerConfig, settings...)
	defer func() { stop() }()
	var backend atomic.Value
	backend.Store(address)
	proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	directory := t.TempDir()
	apiCA := filepath.Join(directory, "api-ca.pem")
	if err := os.WriteFile(apiCA, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: proxy.Certificate().Raw}), 0644); err != nil {
		t.Fatal(err)
	}
	// Avoid a desktop browser on the test runner. The test performs the real
	// provider form interaction after it reads the CLI's authorization URL.
	opener := filepath.Join(directory, "xdg-open")
	if err := os.WriteFile(opener, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	cli := buildProgram(t, "./out/cli/cmd")
	command := func(ctx context.Context, config string, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, cli, args...)
		cmd.Env = append(os.Environ(), "HYPERSHELL_CONFIG="+config, "PATH="+directory+string(os.PathListSeparator)+os.Getenv("PATH"), "GORACE=atexit_sleep_ms=0")
		return cmd
	}
	run := func(config string, args ...string) ([]byte, string, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		cmd := command(ctx, config, args...)
		var output, problem bytes.Buffer
		cmd.Stdout = &output
		cmd.Stderr = &problem
		err := cmd.Run()
		if strings.Contains(problem.String(), "DATA RACE") {
			t.Fatal("CLI data race")
		}
		return output.Bytes(), problem.String(), err
	}
	success := func(config string, args ...string) []byte {
		t.Helper()
		data, problem, err := run(config, args...)
		if err != nil {
			t.Fatalf("CLI request failed: %v %s", err, problem)
		}
		return data
	}
	session := func(config string) cliSessionRecord {
		t.Helper()
		data, err := os.ReadFile(config)
		if err != nil {
			t.Fatal(err)
		}
		var value cliSessionRecord
		if json.Unmarshal(data, &value) != nil || value.OAuth.AccessToken == "" || value.OAuth.RefreshToken == "" || value.OAuth.Pending {
			t.Fatal("invalid saved OAuth session")
		}
		info, err := os.Stat(config)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("OAuth session is not private")
		}
		return value
	}
	login := func(config, username string, device bool) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		args := []string{"login", "--url", proxy.URL, "--ca-file", apiCA, "--issuer-url", k.options.ServerURL + "/realms/workflow", "--issuer-ca-file", k.options.CAFile, "--client-id", "hypershell"}
		if device {
			args = append(args, "--no-browser")
		}
		cmd := command(ctx, config, args...)
		var output, problem runtimeOutput
		cmd.Stdout = &output
		cmd.Stderr = &problem
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		stopped := false
		defer func() {
			if !stopped {
				cancel()
				<-done
			}
		}()
		deadline := time.Now().Add(15 * time.Second)
		for {
			var address, code, callback string
			for _, line := range strings.Split(output.String(), "\n") {
				if device && strings.HasPrefix(line, "Open ") {
					words := strings.Fields(line)
					if len(words) == 6 {
						address, code = words[1], words[5]
					}
				}
				if !device && strings.HasPrefix(line, "https://") {
					address = line
					parsed, err := url.Parse(line)
					if err != nil {
						t.Fatal(err)
					}
					callback = parsed.Query().Get("redirect_uri")
				}
			}
			if address != "" {
				providerForms(t, k, address, username, code, callback)
				break
			}
			select {
			case err := <-done:
				stopped = true
				t.Fatalf("CLI login stopped: %v %s", err, problem.String())
			default:
			}
			if time.Now().After(deadline) {
				t.Fatal("CLI did not display authorization instructions")
			}
			time.Sleep(20 * time.Millisecond)
		}
		err := <-done
		stopped = true
		if err != nil {
			t.Fatalf("CLI login failed: %v %s", err, problem.String())
		}
		saved := session(config)
		for _, token := range []string{saved.OAuth.AccessToken, saved.OAuth.RefreshToken} {
			if strings.Contains(output.String(), token) || strings.Contains(problem.String(), token) {
				t.Fatal("login displayed a token")
			}
		}
	}
	ownerConfig := filepath.Join(directory, "alice.json")
	login(ownerConfig, "alice", false)
	before := session(ownerConfig)
	if before.OAuth.Subject != aliceID {
		t.Fatal("browser login selected another subject")
	}
	payload, err := json.Marshal(f.request("oidc-cli"))
	if err != nil {
		t.Fatal(err)
	}
	bodyFile := filepath.Join(directory, "gateway.json")
	if err := os.WriteFile(bodyFile, payload, 0600); err != nil {
		t.Fatal(err)
	}
	data := success(ownerConfig, "create", "gateway", "--body", bodyFile)
	var gateway struct{ ID string }
	if json.Unmarshal(data, &gateway) != nil || gateway.ID == "" {
		t.Fatal("OIDC login could not create a Gateway")
	}
	stop()
	stop, address = startApplication(t, binary, f.dsn, brokerConfig, settings...)
	backend.Store(address)
	if delay := time.Until(before.OAuth.ExpiresAt) + 100*time.Millisecond; delay > 0 {
		if delay > 15*time.Second {
			t.Fatal("fixture token lifetime exceeds test bound")
		}
		time.Sleep(delay)
	}
	var commands []*exec.Cmd
	for i := 0; i < 3; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		cmd := command(ctx, ownerConfig, "get", "gateway", gateway.ID)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		commands = append(commands, cmd)
	}
	for _, cmd := range commands {
		if err := cmd.Wait(); err != nil {
			t.Fatal("concurrent CLI refresh failed")
		}
	}
	after := session(ownerConfig)
	if after.OAuth.AccessToken == before.OAuth.AccessToken || after.OAuth.RefreshToken == before.OAuth.RefreshToken || after.OAuth.Subject != aliceID {
		t.Fatal("expired session was not refreshed and saved")
	}
	success(ownerConfig, "get", "gateway", gateway.ID)
	viewerConfig := filepath.Join(directory, "bob.json")
	login(viewerConfig, "bob", true)
	viewer := session(viewerConfig)
	if viewer.OAuth.Subject != bobID {
		t.Fatal("device login selected another subject")
	}
	var list struct {
		Total int
		Items []any
	}
	data = success(viewerConfig, "list", "gateways")
	if json.Unmarshal(data, &list) != nil || list.Total != 0 || len(list.Items) != 0 {
		t.Fatal("device login bypassed filtered access")
	}
	if data, problem, err := run(viewerConfig, "get", "gateway", gateway.ID); err == nil || len(data) != 0 || !strings.Contains(problem, "HTTP 404") {
		t.Fatal("device login read another owner's Gateway")
	}
	for _, config := range []string{ownerConfig, viewerConfig} {
		stored := session(config)
		success(config, "logout")
		if _, err := os.Stat(config); !os.IsNotExist(err) {
			t.Fatal("logout retained OAuth credentials")
		}
		result, err := k.http.Do(context.Background(), "POST", "/realms/workflow/protocol/openid-connect/token", http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}, []byte(url.Values{"grant_type": {"refresh_token"}, "client_id": {"hypershell"}, "refresh_token": {stored.OAuth.RefreshToken}}.Encode()))
		if err != nil || (result.StatusCode != 400 && result.StatusCode != 401) {
			t.Fatal("logout did not revoke the provider refresh token")
		}
	}
	t.Log("The generated CLI completed browser and device login, Gateway access, API restart, concurrent refresh, and provider logout")
}
