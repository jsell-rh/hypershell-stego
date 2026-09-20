package serviceaccountkeycloak

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/segmentio/ksuid"
)

// A list and a later read are separate observations. A client can disappear
// between them. A failed list or another read error must still stop the scan.
func TestManagedInventoryHandlesConcurrentRemoval(t *testing.T) {
	for _, scope := range []string{"gateway", "global"} {
		t.Run(scope, func(t *testing.T) {
			for _, tc := range []struct {
				name        string
				count       int
				missing     map[int]bool
				readStatus  int
				listStatus  int
				malformed   bool
				foreign     bool
				wantFailure bool
			}{
				{name: "first_removed", count: 3, missing: map[int]bool{0: true}},
				{name: "middle_removed", count: 3, missing: map[int]bool{1: true}},
				{name: "last_removed", count: 3, missing: map[int]bool{2: true}},
				{name: "all_removed", count: 3, missing: map[int]bool{0: true, 1: true, 2: true}},
				{name: "full_page_removed", count: 101},
				{name: "read_unauthorized", count: 3, readStatus: 401, wantFailure: true},
				{name: "read_forbidden", count: 3, readStatus: 403, wantFailure: true},
				{name: "read_unavailable", count: 3, readStatus: 503, wantFailure: true},
				{name: "list_not_found", count: 3, listStatus: 404, wantFailure: true},
				{name: "malformed_current_client", count: 3, malformed: true, wantFailure: true},
				{name: "current_client_is_foreign", count: 3, foreign: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					gatewayID := ksuid.New().String()
					prefix := "hs-sa-" + gatewayID + "-"
					clients := make([]kcClient, tc.count)
					want := []string{}
					removed := func(i int) bool { return tc.missing[i] || tc.name == "full_page_removed" && i < 100 }
					for i := range clients {
						accountID := ksuid.New().String()
						clients[i] = kcClient{ID: fmt.Sprintf("candidate-%d", i), ClientID: prefix + accountID, Attributes: map[string]string{managedAttribute: "true", gatewayIDAttribute: gatewayID, serviceAccountIDAttribute: accountID}}
						if !removed(i) && !(tc.foreign && i == 0) {
							want = append(want, clients[i].ID)
						}
					}
					var mu sync.Mutex
					pages, reads := []int{}, []string{}
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
							first, err := strconv.Atoi(q.Get("first"))
							if err != nil || first != len(pages)*100 || q.Get("max") != "100" || first >= len(clients) {
								t.Error("inventory page bounds differ")
								w.WriteHeader(400)
								return
							}
							pages = append(pages, first)
							if scope == "gateway" && (q.Get("clientId") != prefix || q.Get("search") != "true") || scope == "global" && (q.Has("clientId") || q.Has("search")) {
								t.Error("inventory scope differs")
							}
							if tc.listStatus != 0 {
								w.WriteHeader(tc.listStatus)
								return
							}
							// List attributes cannot establish current ownership.
							page := []map[string]string{}
							for _, c := range clients[first:min(first+100, len(clients))] {
								page = append(page, map[string]string{"id": c.ID, "clientId": c.ClientID})
							}
							_ = json.NewEncoder(w).Encode(page)
							return
						}
						for i, c := range clients {
							if r.URL.Path != "/admin/realms/test/clients/"+c.ID {
								continue
							}
							reads = append(reads, c.ID)
							if removed(i) {
								w.WriteHeader(http.StatusNotFound)
							} else if tc.readStatus != 0 {
								w.WriteHeader(tc.readStatus)
							} else if tc.malformed {
								_, _ = w.Write([]byte(`{"id":`))
							} else {
								if tc.foreign && i == 0 {
									c.Attributes = map[string]string{}
								}
								_ = json.NewEncoder(w).Encode(c)
							}
							return
						}
						t.Error("unexpected inventory path")
						w.WriteHeader(500)
					}))
					defer server.Close()
					dir := t.TempDir()
					ca, secret := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "secret")
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
					query := gatewayID
					if scope == "global" {
						query = ""
					}
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					rows, err := client.ListManagedClients(ctx, query)
					if (err != nil) != tc.wantFailure {
						t.Fatalf("inventory error differs: %v", err)
					}
					mu.Lock()
					defer mu.Unlock()
					if tc.wantFailure {
						wantReads := 1
						if tc.listStatus != 0 {
							wantReads = 0
						}
						if len(rows) != 0 || len(pages) != 1 || len(reads) != wantReads {
							t.Fatal("failed inventory returned partial results or continued")
						}
						return
					}
					got := []string{}
					for _, row := range rows {
						got = append(got, row.UUID)
					}
					wantReads := make([]string, len(clients))
					for i, c := range clients {
						wantReads[i] = c.ID
					}
					if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(reads, wantReads) || len(pages) != (len(clients)+99)/100 {
						t.Fatal("inventory lost a current client, accepted a foreign client, or skipped a page")
					}
				})
			}
		})
	}
}
