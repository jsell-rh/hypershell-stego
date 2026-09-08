package acceptance

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jsell-rh/hypershell-stego/contracts"
	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	"github.com/segmentio/ksuid"
)

func issuer(t testing.TB) (*rsa.PrivateKey, []string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	public, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(t.TempDir(), "issuer.pem")
	if err := os.WriteFile(name, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: public}), 0600); err != nil {
		t.Fatal(err)
	}
	return key, []string{"STEGO_AUTH_PUBLIC_KEY_FILE=" + name, "STEGO_AUTH_ISSUER=https://issuer.example", "STEGO_AUTH_AUDIENCE=hypershell", "STEGO_AUTH_ROLES_CLAIM=realm_access.roles"}
}
func token(t testing.TB, key *rsa.PrivateKey, user string, roles ...string) string {
	t.Helper()
	claims := jwt.MapClaims{"iss": "https://issuer.example", "aud": "hypershell", "sub": user, "preferred_username": user, "email": user + "@example.test", "given_name": user, "iat": time.Now().Add(-time.Minute).Unix(), "exp": time.Now().Add(time.Hour).Unix(), "realm_access": map[string]any{"roles": roles}}
	if roles == nil {
		claims["realm_access"] = map[string]any{"roles": []string{}}
	}
	value, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func requestJSON(t testing.TB, method, address, bearer string, body []byte) (int, []byte) {
	t.Helper()
	request, err := http.NewRequest(method, address, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := (&http.Client{Timeout: 12 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		t.Fatal(err)
	}
	if (response.StatusCode != 204 && response.Header.Get("Content-Type") != "application/json") || (response.StatusCode == 204 && (response.Header.Get("Content-Type") != "" || len(data) != 0)) || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("response headers: %v", response.Header)
	}
	return response.StatusCode, data
}
func buildApplication(t testing.TB) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "hypershell")
	arguments := []string{"build", "-mod=readonly", "-o", binary}
	if raceEnabled {
		arguments = append(arguments, "-race")
	}
	command := exec.Command("go", append(arguments, "./out")...)
	command.Dir = ".."
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build generated application: %v\n%s", err, output)
	}
	return binary
}

func TestGatewayWorkflowThroughGeneratedRESTProcess(t *testing.T) {
	f := database(t)
	_, config := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, config)
	key, settings := issuer(t)
	binary := buildApplication(t)
	stop, address := startApplication(t, binary, f.dsn, config, settings...)
	path := address + "/api/hypershell/v1/gateways"
	creator := token(t, key, "alice", "gateway:creator")
	owner := token(t, key, "alice")
	body := []byte(fmt.Sprintf(`{"name":"gateway","cluster_id":%q,"release_id":%q,"database_id":"ignored","server_dns_names":["gw.example.test"],"supervisor_image":"supervisor:v1","oidc":"{\"issuer\":\"https://issuer.example\"}"}`, f.cluster, f.release))
	status, data := requestJSON(t, "POST", path, creator, body)
	if status != http.StatusCreated {
		t.Fatalf("create status %d: %s", status, data)
	}
	var gateway httpapi.Gateway
	if err := json.Unmarshal(data, &gateway); err != nil {
		t.Fatal(err)
	}
	if _, err := ksuid.Parse(gateway.ID); err != nil {
		t.Fatalf("invalid Gateway ID: %s", gateway.ID)
	}
	if gateway.Kind != "Gateway" || gateway.Href != "/api/hypershell/v1/gateways/"+gateway.ID || gateway.DatabaseID != f.database || gateway.CreatedBy != "alice" || gateway.CreatedAt.IsZero() || len(gateway.ServerDNSNames) != 1 {
		t.Fatalf("Gateway response shape: %s", data)
	}
	if strings.Contains(string(data), "created_time") || strings.Contains(string(data), "updated_time") || strings.Contains(string(data), "metadata") {
		t.Fatalf("storage or gRPC shape escaped into REST: %s", data)
	}
	if readEvent(t, consumer, gateway.ID) == "" {
		t.Fatal("REST creation lost its durable event ID")
	}
	awaitQueueEmpty(t, f)
	reference, err := contracts.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	schema := reference.OpenAPI.Paths.Value("/api/hypershell/v1/gateways").Post.Responses.Status(201).Value.Content.Get("application/json").Schema.Value
	if err := schema.VisitJSON(value); err != nil {
		t.Fatalf("response violates the pinned API contract: %v", err)
	}
	status, read := requestJSON(t, "GET", path+"/"+gateway.ID, owner, nil)
	if status != 200 || !bytes.Equal(data, read) {
		t.Fatalf("retrieval changed the Gateway: %d %s", status, read)
	}
	status, hidden := requestJSON(t, "POST", path, token(t, key, "bob", "gateway:creator"), body)
	if status != http.StatusCreated {
		t.Fatalf("second creator: %d %s", status, hidden)
	}
	for _, bearer := range []string{"", token(t, key, "bob", "platform:admin")} {
		status, denied := requestJSON(t, "POST", path, bearer, body)
		expected := 403
		if bearer == "" {
			expected = 401
		}
		if status != expected {
			t.Fatalf("create denial: %d %s", status, denied)
		}
	}
	status, denied := requestJSON(t, "POST", path, owner, body)
	if status != 403 {
		t.Fatalf("owner created without creator role: %d %s", status, denied)
	}
	for _, id := range []string{gateway.ID, ksuid.New().String()} {
		status, denied = requestJSON(t, "GET", path+"/"+id, token(t, key, "stranger"), nil)
		if status != 404 {
			t.Fatalf("denied read: %d %s", status, denied)
		}
		var missing map[string]any
		if err := json.Unmarshal(denied, &missing); err != nil {
			t.Fatal(err)
		}
		if missing["id"] != "7" || missing["code"] != "hypershell-7" || missing["href"] != "/api/hypershell/v1/errors/7" {
			t.Fatalf("error shape changed: %s", denied)
		}
	}
	viewer := token(t, key, "stranger", "gateway:owner")
	status, denied = requestJSON(t, "GET", path+"/"+gateway.ID, viewer, nil)
	if status != 404 {
		t.Fatal("a token role created resource ownership")
	}
	if _, err := f.db.Exec(`INSERT INTO role_bindings (id,user_id,role_id,gateway_id,scope) SELECT $1,u.id,r.id,$2,'gateway' FROM users u,roles r WHERE u.username='stranger' AND r.name='gateway:viewer'`, ksuid.New().String(), gateway.ID); err != nil {
		t.Fatal(err)
	}
	status, data = requestJSON(t, "GET", path+"/"+gateway.ID, viewer, nil)
	if status != 200 {
		t.Fatalf("viewer read: %d %s", status, data)
	}
	if _, err := f.db.Exec(`UPDATE role_bindings SET deleted_at=now() WHERE user_id=(SELECT id FROM users WHERE username='stranger')`); err != nil {
		t.Fatal(err)
	}
	status, denied = requestJSON(t, "GET", path+"/"+gateway.ID, viewer, nil)
	if status != 404 {
		t.Fatal("REST accepted a removed viewer grant")
	}
	status, data = requestJSON(t, "GET", path+"?page=1&size=1", owner, nil)
	var list httpapi.GatewayList
	if err := json.Unmarshal(data, &list); err != nil {
		t.Fatal(err)
	}
	if status != 200 || list.Total != 1 || len(list.Items) != 1 || list.Items[0].ID != gateway.ID || list.Items[0].CreatedBy != "alice" {
		t.Fatalf("filtered list: %d %s", status, data)
	}
	status, data = requestJSON(t, "GET", path+"?size=0", owner, nil)
	if err := json.Unmarshal(data, &list); err != nil {
		t.Fatal(err)
	}
	if status != 200 || list.Total != 1 || list.Size != 0 || len(list.Items) != 0 {
		t.Fatalf("count-only list: %d %s", status, data)
	}
	status, data = requestJSON(t, "GET", path, owner, nil)
	if err := json.Unmarshal(data, &list); err != nil {
		t.Fatal(err)
	}
	if status != 200 || list.Size != 1 {
		t.Fatalf("list size must count returned rows: %d %s", status, data)
	}
	before := count(t, f.db, "gateways")
	for _, invalid := range []string{
		strings.TrimSuffix(string(body), "}") + `,"namespace":"client-owned"}`,
		strings.TrimSuffix(string(body), "}") + `,"name":"duplicate"}`,
		strings.Replace(string(body), `"name":"gateway"`, `"Name":"gateway"`, 1),
		strings.Replace(string(body), `"name":"gateway"`, `"name":"\ud800"`, 1),
		strings.Replace(string(body), `"name":"gateway"`, `"name":"\u0000"`, 1),
		string(body) + ` {}`,
	} {
		status, data = requestJSON(t, "POST", path, creator, []byte(invalid))
		if status != 400 {
			t.Fatalf("invalid request accepted: %d %s", status, data)
		}
	}
	if count(t, f.db, "gateways") != before {
		t.Fatal("invalid requests wrote Gateways")
	}
	grants := count(t, f.db, "role_bindings")
	for _, table := range []string{"role_bindings", "stego_outbox.messages"} {
		if _, err := f.db.Exec("ALTER TABLE " + table + " ADD CONSTRAINT reject_new_row CHECK (false) NOT VALID"); err != nil {
			t.Fatal(err)
		}
		status, data = requestJSON(t, "POST", path, creator, body)
		if status != 500 || bytes.Contains(data, []byte("reject_new_row")) {
			t.Fatalf("transaction failure: %d %s", status, data)
		}
		if count(t, f.db, "gateways") != before || count(t, f.db, "role_bindings") != grants {
			t.Fatal("failed REST transaction left a Gateway or grant")
		}
		if _, err := f.db.Exec("ALTER TABLE " + table + " DROP CONSTRAINT reject_new_row"); err != nil {
			t.Fatal(err)
		}
	}
	stop()
	stop, address = startApplication(t, binary, f.dsn, config, settings...)
	status, data = requestJSON(t, "GET", address+gateway.Href, owner, nil)
	if status != 200 {
		t.Fatalf("REST restart lost Gateway access: %d %s", status, data)
	}
	stop()
}
