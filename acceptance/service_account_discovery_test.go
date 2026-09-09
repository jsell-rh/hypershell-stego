package acceptance

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/contracts"
	"github.com/jsell-rh/hypershell-stego/internal/serviceaccounts"
)

func TestServiceAccountDiscoveryThroughGeneratedRuntime(t *testing.T) {
	f := database(t)
	provider := newAccountProvider()
	_, gateway := accountService(t, f, provider)
	grantViewer(t, f, gateway.ID, "bob")
	foreignService, foreign := accountService(t, f, provider)
	if _, err := foreignService.Create(context.Background(), principal("alice"), foreign.ID, accountInput("Build elsewhere")); err != nil {
		t.Fatal(err)
	}
	key, settings := issuer(t)
	rpcSettings, _ := startAccountProvisioner(t, provider, key, settings)
	settings = append(settings, rpcSettings...)
	_, config := broker(t, identity(t, "localhost"))
	binary := buildApplication(t)
	stop, address := startApplication(t, binary, f.dsn, config, settings...)
	defer func() { stop() }()
	path := "/api/hypershell/v1/gateways/" + gateway.ID + "/service_accounts"
	owner, viewer := token(t, key, "alice"), token(t, key, "bob")
	type item struct {
		ID, Name, Status string
		ClientID         string `json:"client_id"`
		Subject          string
	}
	create := func(bearer, name string) item {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"name": name})
		code, data := requestJSON(t, "POST", address+path, bearer, body)
		if code != 201 {
			t.Fatalf("create: %d %s", code, data)
		}
		var row item
		if err := json.Unmarshal(data, &row); err != nil {
			t.Fatal(err)
		}
		return row
	}
	first := create(owner, "Build%_!\\Night")
	second := create(owner, "Build plain")
	other := create(viewer, "Build viewer")
	list := func(bearer, query string, want int, ids ...string) {
		t.Helper()
		code, data := requestJSON(t, "GET", address+path+"?"+query, bearer, nil)
		if code != 200 {
			t.Fatalf("list: %d %s", code, data)
		}
		if strings.Contains(string(data), `"client_secret":`) || strings.Contains(string(data), "one-time-private") {
			t.Fatal("list exposed a secret")
		}
		var got struct {
			Total int
			Items []item
		}
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatal(err)
		}
		if executable := os.Getenv("STEGO_TEST_REFERENCE_CLI"); executable != "" {
			configPath := filepath.Join(t.TempDir(), "config.json")
			configuration, _ := json.Marshal(map[string]string{"url": address, "access_token": bearer})
			if err := os.WriteFile(configPath, configuration, 0600); err != nil {
				t.Fatal(err)
			}
			args := []string{"list", "serviceAccounts", "--gateway-id", gateway.ID}
			values, err := url.ParseQuery(query)
			if err != nil {
				t.Fatal(err)
			}
			for name, values := range values {
				args = append(args, "--"+name, values[0])
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			command := exec.CommandContext(ctx, executable, args...)
			command.Env = append(os.Environ(), "HYPERSHELL_CONFIG="+configPath)
			output, err := command.CombinedOutput()
			cancel()
			if err != nil {
				t.Fatalf("reference CLI failed: %v %s", err, output)
			}
			var cli struct {
				Total int
				Items []item
			}
			if err := json.Unmarshal(output, &cli); err != nil {
				t.Fatal(err)
			}
			if cli.Total != got.Total || len(cli.Items) != len(got.Items) {
				t.Fatal("reference CLI list differs")
			}
			for i, row := range cli.Items {
				if row.ID != got.Items[i].ID {
					t.Fatal("reference CLI order differs")
				}
			}
		}
		if got.Total != want || len(got.Items) != len(ids) {
			t.Fatalf("list total/items: %+v", got)
		}
		for i, id := range ids {
			if got.Items[i].ID != id {
				t.Fatalf("list order: %+v", got)
			}
		}
	}
	list(owner, "", 3, other.ID, second.ID, first.ID)
	reference, err := contracts.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, parameter := range reference.OpenAPI.Paths.Value("/api/hypershell/v1/gateways/{gateway_id}/service_accounts").Get.Parameters {
		if parameter.Value.Name != "status" {
			continue
		}
		for _, value := range parameter.Value.Schema.Value.Enum {
			status := value.(string)
			if status == "ready" {
				list(owner, "status="+status, 3, other.ID, second.ID, first.ID)
			} else {
				list(owner, "status="+status, 0)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("status contract was not checked")
	}

	tied := []string{first.ID, second.ID, other.ID}
	slices.Sort(tied)
	list(owner, "sort=role&order=asc", 3, tied...)
	slices.Reverse(tied)
	list(owner, "sort=status&order=desc", 3, tied...)
	list(owner, "sort=expires_at&order=asc", 3, first.ID, second.ID, other.ID)
	// The reference CLI sends these sort parameters even without optional filters.
	list(owner, "page=1&size=20&sort=name&order=asc", 3, second.ID, other.ID, first.ID)
	list(owner, "page=1&size=1&sort=name&order=asc", 3, second.ID)
	list(owner, "page=2&size=1&sort=name&order=asc", 3, other.ID)
	list(owner, "page=3&size=1&sort=name&order=asc", 3, first.ID)
	list(owner, "page=4&size=1&sort=name&order=asc", 3)
	list(viewer, "sort=name&order=asc&search=BUILD", 1, other.ID)
	list(viewer, "search="+url.QueryEscape(first.ClientID), 0)
	for _, search := range []string{"%_!\\nIgHt", first.ClientID, first.Subject} {
		list(owner, "search="+url.QueryEscape(search), 1, first.ID)
	}
	list(owner, "search="+url.QueryEscape("' OR true --"), 0)
	code, data := requestJSON(t, "POST", address+path+"/"+first.ID+"/revoke", owner, nil)
	if code != 200 {
		t.Fatalf("revoke: %d %s", code, data)
	}
	list(owner, "status=revoked&search=BUILD", 1, first.ID)
	list(owner, "status=ready&sort=name&order=desc", 2, other.ID, second.ID)
	for _, query := range []string{"status=unknown", "sort=client_secret", "order=ASC", "size=0", "page=0", "search=%00", "search=%ff", "sort=name&sort=status", "search=a&search=b", "unknown=x", "sort=name%3BDELETE", "search=" + strings.Repeat("x", 4097)} {
		code, data := requestJSON(t, "GET", address+path+"?"+query, owner, nil)
		if code != 400 {
			t.Fatalf("invalid query %q: %d %s", query, code, data)
		}
	}
	for _, bearer := range []string{token(t, key, "stranger"), token(t, key, "admin", "platform:admin")} {
		code, _ := requestJSON(t, "GET", address+path+"?search=BUILD", bearer, nil)
		if code != 404 {
			t.Fatalf("denied list: %d", code)
		}
	}
	stop()
	stop, address = startApplication(t, binary, f.dsn, config, settings...)
	list(owner, "status=revoked&search=BUILD", 1, first.ID)
	code, data = requestJSON(t, "DELETE", address+path+"/"+first.ID, owner, nil)
	if code != 204 {
		t.Fatalf("delete: %d %s", code, data)
	}
	list(owner, "status=revoked&search=BUILD", 0)
	list(owner, "status=ready&sort=name&order=asc", 2, second.ID, other.ID)
}

// This benchmark includes domain authorization and PostgreSQL count and paging.
// It excludes transport and provider calls. The rows model revoked history.
func BenchmarkServiceAccountDiscovery(b *testing.B) {
	f := database(b)
	service, gateway := accountService(b, f, newAccountProvider())
	_, foreign := accountService(b, f, newAccountProvider())
	_, err := f.db.Exec(`INSERT INTO service_accounts
 (id,created_time,updated_time,gateway_id,name,credential_type,role,status,created_by_user_id,client_id,client_uuid,subject,expires_at,revoked_at,active)
 SELECT lpad(n::text,27,'0'),now(),now(),CASE WHEN n<=1000 THEN $1 ELSE $2 END,
 CASE WHEN n%10=0 THEN 'Batch%_!' ELSE 'noise-'||n END,'client_secret','openshell-user','revoked',u.id,
 'history-'||n,'provider-'||n,'subject-'||n,now()+interval '1 day',now(),false
 FROM generate_series(1,5000) n CROSS JOIN users u WHERE u.username='alice'`, gateway.ID, foreign.ID)
	if err != nil {
		b.Fatal(err)
	}
	options := serviceaccounts.ListOptions{Page: 1, Size: 20, Status: "revoked", Search: "BATCH%_!", Sort: "created_at", Order: "desc"}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		result, _, err := service.ListQuery(context.Background(), principal("alice"), gateway.ID, options)
		if err != nil || result.Total != 100 {
			b.Fatalf("discovery benchmark: %d %v", result.Total, err)
		}
	}
	b.StopTimer()
}
