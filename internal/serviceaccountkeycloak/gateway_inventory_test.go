package serviceaccountkeycloak

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	provider "github.com/jsell-rh/hypershell-stego/out/keycloak"
	"github.com/segmentio/ksuid"
)

func TestGatewayIdentityInventory(t *testing.T) {
	for _, mode := range []string{"owned current reads", "search denied", "read missing", "read failed", "name changed", "repeated page ID", "scan limit", "parent cancelled"} {
		t.Run(mode, func(t *testing.T) {
			ids := []string{ksuid.New().String(), ksuid.New().String(), ksuid.New().String(), ksuid.New().String()}
			native := func(uuid, id string, attrs map[string]string) provider.ClientRepresentation {
				return provider.ClientRepresentation{ID: uuid, ClientID: "hs-gateway-" + id, Attributes: attrs}
			}
			current := map[string]provider.ClientRepresentation{
				"owned":        native("owned", ids[0], map[string]string{managedGatewayAttribute: "true", managedGatewayIDAttribute: ids[0]}),
				"legacy":       native("legacy", ids[1], map[string]string{gatewayAttribute: "true", gatewayIDAttribute: ids[1]}),
				"foreign":      native("foreign", ids[2], map[string]string{managedGatewayAttribute: "true", managedGatewayIDAttribute: ids[3]}),
				"mixed":        native("mixed", ids[3], map[string]string{managedGatewayAttribute: "true", managedGatewayIDAttribute: ids[3], gatewayAttribute: "true", gatewayIDAttribute: ids[3]}),
				"console":      {ID: "console", ClientID: "hs-console-" + ids[2], Attributes: map[string]string{managedConsoleAttribute: "true", managedGatewayIDAttribute: ids[2]}},
				"same-gateway": {ID: "same-gateway", ClientID: "hs-console-" + ids[0], Attributes: map[string]string{managedConsoleAttribute: "true", managedGatewayIDAttribute: ids[0]}},
			}
			project := func(value provider.ClientRepresentation) provider.ClientRepresentation {
				return provider.ClientRepresentation{ID: value.ID, ClientID: value.ClientID}
			}
			var mu sync.Mutex
			queries := []string{}
			reads := map[string]int{}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.URL.Path == "/realms/test/protocol/openid-connect/token" {
					_, _ = w.Write([]byte(`{"access_token":"test-token","expires_in":300,"token_type":"Bearer"}`))
					return
				}
				if r.Method != http.MethodGet {
					t.Error("inventory attempted a mutation")
					w.WriteHeader(500)
					return
				}
				if r.URL.Path == "/admin/realms/test/clients" {
					q := r.URL.Query()
					prefix := q.Get("clientId")
					first, err := strconv.Atoi(q.Get("first"))
					if err != nil || q.Get("max") != "100" || q.Get("search") != "true" || (prefix != "hs-gateway-" && prefix != "hs-console-") || len(q) != 4 {
						t.Error("inventory requested an unbounded or unrelated query")
						w.WriteHeader(400)
						return
					}
					queries = append(queries, prefix+":"+strconv.Itoa(first))
					if mode == "search denied" {
						w.WriteHeader(403)
						return
					}
					page := []provider.ClientRepresentation{}
					if mode == "scan limit" {
						for i := 0; i < 100; i++ {
							page = append(page, provider.ClientRepresentation{ID: fmt.Sprintf("limit-%d", first+i), ClientID: fmt.Sprintf("other-hs-gateway-%d", first+i)})
						}
					} else if prefix == "hs-console-" {
						page = append(page, project(current["same-gateway"]), project(current["console"]))
					} else if first == 0 {
						page = append(page, project(current["owned"]), project(current["legacy"]), project(current["foreign"]), project(current["mixed"]))
						// A list can contain false ownership data. The current read must decide.
						page[2].Attributes = map[string]string{managedGatewayAttribute: "true", managedGatewayIDAttribute: ids[2]}
						for i := 4; i < 100; i++ {
							page = append(page, provider.ClientRepresentation{ID: fmt.Sprintf("other-%d", i), ClientID: fmt.Sprintf("other-hs-gateway-%d", i)})
						}
					} else if first == 100 {
						if mode == "repeated page ID" {
							page = append(page, project(current["owned"]))
						}
					} else {
						t.Error("unexpected page offset")
						w.WriteHeader(400)
						return
					}
					_ = json.NewEncoder(w).Encode(page)
					return
				}
				key := strings.TrimPrefix(r.URL.Path, "/admin/realms/test/clients/")
				reads[key]++
				value, ok := current[key]
				if !ok {
					t.Error("read an unrelated client")
					w.WriteHeader(404)
					return
				}
				if key == "owned" {
					switch mode {
					case "read missing":
						w.WriteHeader(404)
						return
					case "read failed":
						w.WriteHeader(503)
						return
					case "name changed":
						value.ClientID = "hs-gateway-" + ids[3]
					}
				}
				_ = json.NewEncoder(w).Encode(value)
			}))
			defer server.Close()
			dir := t.TempDir()
			ca, secret := filepath.Join(dir, "ca"), filepath.Join(dir, "secret")
			if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(secret, []byte("test-secret"), 0600); err != nil {
				t.Fatal(err)
			}
			client, err := NewClient(Options{ServerURL: server.URL, Realm: "test", ClientID: "admin", SecretFile: secret, CAFile: ca})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if mode == "parent cancelled" {
				cancel()
			}
			result, err := client.GatewayIDs(ctx)
			mu.Lock()
			defer mu.Unlock()
			if mode == "owned current reads" {
				if err != nil || !reflect.DeepEqual(result, ids[:3]) {
					t.Fatalf("owned inventory differs: %v %v", result, err)
				}
				if !reflect.DeepEqual(queries, []string{"hs-gateway-:0", "hs-gateway-:100", "hs-console-:0"}) {
					t.Fatal("query sequence differs", queries)
				}
				if len(reads) != len(current) {
					t.Fatal("current candidate reads differ", reads)
				}
				for _, count := range reads {
					if count != 1 {
						t.Fatal("candidate was read more than once")
					}
				}
				return
			}
			if err == nil || result != nil {
				t.Fatal("incomplete inventory returned success or partial IDs", result, err)
			}
			switch mode {
			case "scan limit":
				if !errors.Is(err, provider.ErrClientInventoryLimit) || len(queries) != 100 || len(reads) != 0 {
					t.Fatal("scan limit became completion or read unrelated clients", len(queries), len(reads), err)
				}
			case "parent cancelled":
				if !errors.Is(err, context.Canceled) || len(queries) != 0 || len(reads) != 0 {
					t.Fatal("cancelled scan reached provider", err)
				}
			case "repeated page ID":
				if len(queries) != 2 || reads["owned"] != 1 {
					t.Fatal("repeated ID was read or scan continued", queries, reads)
				}
			default:
				if len(queries) != 1 {
					t.Fatal("failed scan retried another inventory path", queries)
				}
			}
		})
	}
}
