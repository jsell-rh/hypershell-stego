package acceptance

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	web "github.com/jsell-rh/hypershell-stego/out/application/client"
)

type kubernetesBrowser struct {
	t                    *testing.T
	namespace, group, oc string
	pods                 map[string]string
	databases            map[string]string
}

func TestGeneratedKubernetesBrowserGatewayWorkflow(t *testing.T) {
	if os.Getenv("STEGO_TEST_KUBERNETES_BROWSER") != "1" {
		t.Skip("requires the bounded Kubernetes browser fixture")
	}
	p := &kubernetesBrowser{t: t, namespace: os.Getenv("STEGO_TEST_NAMESPACE"), group: os.Getenv("STEGO_TEST_FS_GROUP"), oc: os.Getenv("STEGO_TEST_OC"), pods: map[string]string{}, databases: map[string]string{}}
	if !strings.HasPrefix(p.namespace, "stego-service-") || p.group == "" || p.oc == "" || os.Getenv("STEGO_REQUIRE_BROWSER") != "1" {
		t.Fatal("require the dedicated namespace, file group, cluster client, and rendered browser")
	}
	for _, name := range []string{"STEGO_TEST_SERVICE_IMAGE", "STEGO_TEST_CONSOLE_IMAGE", "STEGO_TEST_PROVISIONER_IMAGE"} {
		if !strings.Contains(os.Getenv(name), "@sha256:") {
			t.Fatal("require a published image digest", name)
		}
	}
	runBrowserGatewayWorkflow(t, p)
	if len(p.pods) != 3 {
		t.Fatal("all three generated Deployments must run")
	}
}
func (p *kubernetesBrowser) host(name string) string { return name + "." + p.namespace + ".svc" }
func (p *kubernetesBrowser) command(input []byte, args ...string) []byte {
	p.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 190*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, p.oc, append([]string{"--namespace=" + p.namespace, "--request-timeout=180s"}, args...)...)
	var inputKind struct{ Kind string }
	if json.Unmarshal(input, &inputKind) == nil && inputKind.Kind == "Secret" {
		// Capture transport metadata, but never print the raw Secret response.
		cmd.Args = append(cmd.Args, "--v=6")
	}
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if input != nil {
			// Secret errors can contain the request body. Log only a fixed category.
			var document struct {
				Kind     string
				Metadata struct{ Name string }
				Items    []struct{ Kind string }
			}
			if json.Unmarshal(input, &document) == nil && document.Kind == "List" && len(document.Items) > 0 {
				public := true
				for _, item := range document.Items {
					switch item.Kind {
					case "Deployment", "Service", "ServiceAccount", "NetworkPolicy":
					default:
						public = false
					}
				}
				if public {
					p.t.Fatalf("Generated deployment apply failed: %v\n%s", err, output)
				}
			}
			p.t.Fatalf("Kubernetes browser fixture write failed: kind=%s name=%s category=%s: %v", document.Kind, document.Metadata.Name, kubernetesWriteFailureCategory(output), err)
		}
		p.t.Fatalf("Kubernetes browser command failed: %v\n%s", err, output)
	}
	return output
}

func kubernetesWriteFailureCategory(output []byte) string {
	for _, reason := range []string{"Conflict", "Forbidden", "Invalid", "NotFound", "AlreadyExists", "TooManyRequests", "ServiceUnavailable", "Timeout", "InternalError", "Unauthorized"} {
		if bytes.Contains(output, []byte("("+reason+")")) {
			return reason
		}
	}
	text := strings.ToLower(string(output))
	for _, phrase := range []string{"tls handshake timeout", "i/o timeout", "context deadline exceeded", "client.timeout", "request canceled", "connection refused", "connection reset", "unexpected eof", "permission denied", "failed to download openapi", "unable to retrieve the complete list of server apis", "error validating data", "unable to recognize", "no matches for kind"} {
		if strings.Contains(text, phrase) {
			return phrase
		}
	}
	if codes := regexp.MustCompile(`(?:Response Status: |status=")([1-5][0-9]{2})`).FindAllSubmatch(output, -1); len(codes) > 0 {
		return "HTTP " + string(codes[len(codes)-1][1])
	}
	return "unclassified"
}

func TestKubernetesWriteFailurePrivacy(t *testing.T) {
	for input, want := range map[string]string{
		`Secret data: private-fixture-key; Error from server (Forbidden)`: "Forbidden",
		`private-fixture-key: net/http: TLS handshake timeout`:            "tls handshake timeout",
		`status="503 Service Unavailable" body=private-fixture-key`:       "HTTP 503",
		`Response Status: 500 Internal Server Error; private-fixture-key`: "HTTP 500",
		`private-fixture-key unknown error`:                               "unclassified",
	} {
		if got := kubernetesWriteFailureCategory([]byte(input)); got != want {
			t.Fatal("unsafe or incorrect write failure category")
		}
	}
}
func (p *kubernetesBrowser) apply(value any) {
	p.t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		p.t.Fatal(err)
	}
	p.command(data, "apply", "-f", "-")
}
func (p *kubernetesBrowser) read(path string) []byte {
	p.t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		p.t.Fatal("cannot read fixture file", err)
	}
	return data
}

// Each generated process gets a distinct database and a role that cannot apply
// schema changes. The fixture owner creates the schema before either rollout.
func (p *kubernetesBrowser) database(f *fixture, console bool) string {
	p.t.Helper()
	if saved := p.databases[f.dsn]; saved != "" {
		return saved
	}
	cfg, err := pgx.ParseConfig(f.dsn)
	if err != nil {
		p.t.Fatal(err)
	}
	role := "browser_" + strings.TrimPrefix(cfg.Database, "hypershell_test_")
	id := pgx.Identifier{role}.Sanitize()
	password := hex.EncodeToString(makeRandom(p.t, 24))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := f.db.ExecContext(ctx, "CREATE ROLE "+id+" LOGIN PASSWORD '"+password+"'"); err != nil {
		p.t.Fatal(err)
	}
	p.t.Cleanup(func() {
		if _, err := f.db.Exec("DROP OWNED BY " + id); err != nil {
			p.t.Error(err)
		}
		if _, err := f.db.Exec("DROP ROLE " + id); err != nil {
			p.t.Error(err)
		}
	})
	grants := []string{"GRANT CONNECT ON DATABASE " + pgx.Identifier{cfg.Database}.Sanitize() + " TO " + id, "GRANT USAGE ON SCHEMA public TO " + id}
	if console {
		grants = append(grants, "GRANT SELECT, INSERT, UPDATE, DELETE ON stego_browser_sessions TO "+id)
	} else {
		grants = append(grants, "GRANT USAGE ON SCHEMA stego_outbox TO "+id, "GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public, stego_outbox TO "+id, "GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public, stego_outbox TO "+id)
	}
	for _, statement := range grants {
		if _, err := f.db.ExecContext(ctx, statement); err != nil {
			p.t.Fatal(err)
		}
	}
	var canCreate bool
	if err := f.db.QueryRowContext(ctx, "SELECT has_schema_privilege($1,'public','CREATE')", role).Scan(&canCreate); err != nil || canCreate {
		p.t.Fatal("runtime role can change the schema", err)
	}
	if console {
		var canReadDomain bool
		if err := f.db.QueryRowContext(ctx, "SELECT has_table_privilege($1,'gateways','SELECT')", role).Scan(&canReadDomain); err != nil || canReadDomain {
			p.t.Fatal("console role can read domain data", err)
		}
	}
	dsn := &url.URL{Scheme: "postgres", Host: p.host("fixture") + ":5432", Path: "/" + cfg.Database, User: url.UserPassword(role, password)}
	dsn.RawQuery = url.Values{"sslmode": {"verify-full"}, "sslrootcert": {"/var/run/stego/database-ca.pem"}}.Encode()
	p.databases[f.dsn] = dsn.String()
	return dsn.String()
}
func (p *kubernetesBrowser) settings(entries []string, environment map[string]string, files map[string][]byte) {
	for _, entry := range entries {
		name, value, ok := strings.Cut(entry, "=")
		if !ok {
			p.t.Fatal("invalid fixture setting")
		}
		// TLS listeners are fixed by the generated Deployment.
		if name == "PORT" || name == "STEGO_GRPC_ADDR" || strings.HasPrefix(name, "STEGO_HTTP_TLS_") || strings.HasPrefix(name, "STEGO_GRPC_TLS_") {
			continue
		}
		if value != "" && (strings.HasSuffix(name, "_FILE") || name == "OTEL_EXPORTER_OTLP_CERTIFICATE") {
			file := "setting-" + strings.ToLower(strings.ReplaceAll(name, "_", "-"))
			files[file] = p.read(value)
			value = "/var/run/stego/" + file
		}
		if name == "OTEL_EXPORTER_OTLP_ENDPOINT" {
			value = "https://" + p.host("fixture") + ":19093"
		}
		environment[name] = value
	}
}
func (p *kubernetesBrowser) startAPI(f *fixture, c Config, id testIdentity, settings []string) (func(), string, string) {
	files := map[string][]byte{"database-url": []byte(p.database(f, false)), "database-ca.pem": p.read(os.Getenv("STEGO_TEST_POSTGRES_CA_FILE"))}
	env := map[string]string{"DATABASE_URL_FILE": "/var/run/stego/database-url", "DATABASE_PROVIDER": "cnpg", "STEGO_KAFKA_BROKERS": strings.Join(c.Brokers, ","), "STEGO_KAFKA_TOPIC": c.Topic, "STEGO_KAFKA_AUTHENTICATION": c.Authentication}
	p.settings(append([]string{"STEGO_KAFKA_CA_FILE=" + c.CAFile, "STEGO_KAFKA_CLIENT_CERTIFICATE_FILE=" + c.ClientCertificateFile, "STEGO_KAFKA_CLIENT_KEY_FILE=" + c.ClientKeyFile}, settings...), env, files)
	stop, _ := p.start("hypershell", "..", os.Getenv("STEGO_TEST_SERVICE_IMAGE"), id, env, files)
	p.checkDatabaseTLS(f)
	return stop, "https://" + p.host("hypershell") + ":8443", p.host("hypershell") + ":9090"
}
func (p *kubernetesBrowser) startConsole(f *fixture, origin, api, apiCA string, k *keycloakFixture, id testIdentity, secret, key string, telemetry []string) (func(), func() string) {
	files := map[string][]byte{"database-url": []byte(p.database(f, true)), "database-ca.pem": p.read(os.Getenv("STEGO_TEST_POSTGRES_CA_FILE")), "api-ca.pem": p.read(apiCA), "issuer-ca.pem": p.read(k.options.CAFile), "client-secret": p.read(secret), "session-key": p.read(key)}
	env := map[string]string{"DATABASE_URL_FILE": "/var/run/stego/database-url", "STEGO_BROWSER_ORIGIN": origin, "STEGO_BROWSER_API_URL": api, "STEGO_BROWSER_API_CA_FILE": "/var/run/stego/api-ca.pem", "STEGO_BROWSER_ISSUER": k.options.ServerURL + "/realms/workflow", "STEGO_BROWSER_ISSUER_CA_FILE": "/var/run/stego/issuer-ca.pem", "STEGO_BROWSER_CLIENT_ID": "hypershell-console", "STEGO_BROWSER_CLIENT_SECRET_FILE": "/var/run/stego/client-secret", "STEGO_BROWSER_SESSION_KEY_FILE": "/var/run/stego/session-key"}
	p.settings(telemetry, env, files)
	stop, logs := p.start("hypershell-console", "../console", os.Getenv("STEGO_TEST_CONSOLE_IMAGE"), id, env, files)
	p.checkDatabaseTLS(f)
	return stop, logs
}
func (p *kubernetesBrowser) start(name, module, image string, id testIdentity, env map[string]string, files map[string][]byte, target ...string) (func(), func() string) {
	p.t.Helper()
	dir := filepath.Dir(id.config.CAFile)
	files["tls.crt"] = p.read(filepath.Join(dir, "server.pem"))
	files["tls.key"] = p.read(filepath.Join(dir, "server-key.pem"))
	if p.pods[name] == "" {
		p.t.Cleanup(func() {
			p.command(nil, "delete", "deployment/"+name, "service/"+name, "serviceaccount/"+name, "networkpolicy/"+name, "secret/"+name+"-files", "secret/"+name+"-runtime", "--ignore-not-found")
		})
	}
	p.apply(map[string]any{"apiVersion": "v1", "kind": "Secret", "metadata": map[string]string{"name": name + "-files", "namespace": p.namespace}, "data": files})
	p.apply(map[string]any{"apiVersion": "v1", "kind": "Secret", "metadata": map[string]string{"name": name + "-runtime", "namespace": p.namespace}, "stringData": env})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	render := exec.CommandContext(ctx, "go", "run", "-mod=readonly", "./out/deploy/render", "--image", image, "--namespace", p.namespace, "--fs-group", p.group)
	render.Args = append(render.Args, target...)
	render.Dir = module
	manifest, err := render.Output()
	cancel()
	if err != nil {
		p.t.Fatal("browser deployment renderer failed", err)
	}
	p.command(manifest, "apply", "-f", "-")
	p.command(nil, "rollout", "status", "deployment/"+name, "--timeout=180s")
	var list struct {
		Items []struct {
			Metadata struct {
				UID               string
				DeletionTimestamp *string
			}
			Spec struct {
				ServiceAccountName           string
				AutomountServiceAccountToken bool
				Containers                   []struct {
					Image           string
					SecurityContext struct {
						ReadOnlyRootFilesystem   bool
						AllowPrivilegeEscalation bool
					}
				}
			}
			Status struct{ ContainerStatuses []struct{ RestartCount int } }
		}
	}
	if err := json.Unmarshal(p.command(nil, "get", "pods", "-l", "app.kubernetes.io/name="+name, "-o", "json"), &list); err != nil {
		p.t.Fatal(err)
	}
	uid := ""
	for _, pod := range list.Items {
		if pod.Metadata.DeletionTimestamp != nil {
			continue
		}
		if pod.Spec.ServiceAccountName != name || pod.Spec.AutomountServiceAccountToken || len(pod.Spec.Containers) != 1 || pod.Spec.Containers[0].Image != image || !pod.Spec.Containers[0].SecurityContext.ReadOnlyRootFilesystem || pod.Spec.Containers[0].SecurityContext.AllowPrivilegeEscalation || len(pod.Status.ContainerStatuses) != 1 || pod.Status.ContainerStatuses[0].RestartCount != 0 {
			p.t.Fatal("live browser workload differs from generated restrictions")
		}
		if uid != "" {
			p.t.Fatal("unexpected concurrent fixture Pod")
		}
		uid = pod.Metadata.UID
	}
	if uid == "" || uid == p.pods[name] {
		p.t.Fatal("rollout did not create a new Pod")
	}
	if p.pods[name] != "" {
		p.t.Logf("%s replaced Pod %s with %s", name, p.pods[name], uid)
	}
	p.pods[name] = uid
	stopped := false
	retained := ""
	logs := func() string {
		if stopped {
			return retained
		}
		return string(p.command(nil, "logs", "deployment/"+name, "--tail=10000"))
	}
	stop := func() {
		if stopped {
			return
		}
		retained = logs()
		p.command(nil, "scale", "deployment/"+name, "--replicas=0")
		p.command(nil, "wait", "--for=delete", "pods", "-l", "app.kubernetes.io/name="+name, "--timeout=60s")
		stopped = true
	}
	p.t.Cleanup(stop)
	p.t.Cleanup(func() {
		if p.t.Failed() {
			p.t.Logf("%s process diagnostics: %s", name, logs())
		}
	})
	if len(target) != 0 {
		return stop, logs
	}
	client, err := web.New(web.Options{BaseURL: "https://" + p.host(name) + ":8443", CAFile: id.config.CAFile})
	if err != nil {
		p.t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for {
		response, err := client.Do(ctx, "GET", "/readyz", nil, nil)
		if err == nil && response.StatusCode == 200 {
			break
		}
		select {
		case <-ctx.Done():
			p.t.Fatal("generated service failed verified readiness", err)
		case <-time.After(100 * time.Millisecond):
		}
	}
	return stop, logs
}

func (p *kubernetesBrowser) checkDatabaseTLS(f *fixture) {
	p.t.Helper()
	dsn, err := url.Parse(p.databases[f.dsn])
	if err != nil {
		p.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var count int
	var secure bool
	if err := f.db.QueryRowContext(ctx, "SELECT count(*),coalesce(bool_and(ssl),false) FROM pg_stat_activity JOIN pg_stat_ssl USING(pid) WHERE usename=$1", dsn.User.Username()).Scan(&count, &secure); err != nil || count == 0 || !secure {
		p.t.Fatal("runtime database connection did not use TLS", err)
	}
}
