package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc/metadata"
)

func TestRESTFieldSelectionPreservesAccessAcrossRestart(t *testing.T) {
	f := database(t)
	_, config := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, config)
	key, settings := issuer(t)
	tlsIdentity := identity(t, "localhost")
	directory := filepath.Dir(tlsIdentity.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"))
	binary := buildApplication(t)
	stop, address, grpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	defer func() { stop() }()
	root := "/api/hypershell/v1/gateways"
	ids := map[string]string{}
	for _, item := range []struct{ name, user string }{{"a-visible", "alice"}, {"z-visible", "alice"}, {"b-hidden", "bob"}} {
		body := []byte(fmt.Sprintf(`{"name":%q,"cluster_id":%q,"release_id":%q,"database_id":"ignored","server_dns_names":["one.example","two.example"]}`, item.name, f.cluster, f.release))
		code, data := requestJSON(t, "POST", address+root, token(t, key, item.user, "gateway:creator"), body)
		var row httpapi.Gateway
		if code != 201 || json.Unmarshal(data, &row) != nil {
			t.Fatal("create Gateway", code)
		}
		ids[item.name] = row.ID
		readGatewayEvent(t, consumer, row.ID, "Create", "gateway.created")
	}
	awaitQueueEmpty(t, f)
	owner := token(t, key, "alice")
	list := func(query url.Values, bearer string, total, size int, keys ...string) []map[string]json.RawMessage {
		t.Helper()
		code, data := requestJSON(t, "GET", address+root+"?"+query.Encode(), bearer, nil)
		var response struct {
			Kind, Href        string
			Page, Size, Total int
			Items             []map[string]json.RawMessage
		}
		wantPage := 1
		if query.Get("page") != "" {
			wantPage, _ = strconv.Atoi(query.Get("page"))
		}
		if code != 200 || json.Unmarshal(data, &response) != nil || response.Kind != "GatewayList" || response.Href != root || response.Page != wantPage || response.Total != total || response.Size != size || len(response.Items) != size || response.Items == nil {
			t.Fatalf("selected list lost its envelope or access filter: status %d", code)
		}
		for _, item := range response.Items {
			if len(item) != len(keys) {
				t.Fatal("selected list returned extra fields", item)
			}
			for _, key := range keys {
				if _, ok := item[key]; !ok {
					t.Fatal("selected field missing", key)
				}
			}
		}
		return response.Items
	}
	for page, name := range []string{"a-visible", "z-visible"} {
		items := list(url.Values{"fields": {"id,name"}, "size": {"1"}, "page": {fmt.Sprint(page + 1)}, "orderBy": {"name asc"}}, owner, 2, 1, "id", "name")
		if string(items[0]["name"]) != fmt.Sprintf("%q", name) || string(items[0]["id"]) != fmt.Sprintf("%q", ids[name]) {
			t.Fatal("projection changed page order")
		}
	}
	list(url.Values{"fields": {"name"}, "size": {"0"}}, owner, 2, 0)
	list(url.Values{"fields": {"name"}}, token(t, key, "outsider"), 0, 0)
	list(url.Values{"fields": {"name"}, "search": {"name = 'b-hidden' or name = 'a-visible'"}}, owner, 1, 1, "name")
	items := list(url.Values{"fields": {"server_dns_names"}, "size": {"1"}}, owner, 2, 1, "server_dns_names")
	if string(items[0]["server_dns_names"]) != `["one.example","two.example"]` {
		t.Fatal("array field changed")
	}
	var complete map[string]any
	for _, suffix := range []string{"", "?fields=", "?fields=*"} {
		code, data := requestJSON(t, "GET", address+root+suffix, owner, nil)
		var value map[string]any
		if code != 200 || json.Unmarshal(data, &value) != nil {
			t.Fatal("complete field selection", code)
		}
		if complete == nil {
			complete = value
		} else if !reflect.DeepEqual(complete, value) {
			t.Fatal("wildcard changed the full response")
		}
	}
	for _, query := range []url.Values{{"fields": {"unknown"}}, {"fields": {"unknown"}, "size": {"0"}}, {"fields": {"name,,id"}}, {"fields": {"name", "id"}}, {"fields": {"name,NAME"}}, {"fields": {"name.*"}}, {"fields": {"id;SELECT 1"}}, {"fields": {"stego_revision"}}, {"fields": {"client_secret"}}} {
		for _, bearer := range []string{owner, token(t, key, "outsider")} {
			if code, _ := requestJSON(t, "GET", address+root+"?"+query.Encode(), bearer, nil); code != 400 {
				t.Fatal("invalid selector accepted", query, code)
			}
		}
	}
	admin := token(t, key, "admin", "platform:admin")
	if code, _ := requestJSON(t, "POST", address+"/api/hypershell/v1/gateway_networks", admin, []byte(`{"name":"selected-network"}`)); code != 201 {
		t.Fatal("create network", code)
	}
	for _, collection := range []string{"managed_clusters", "managed_databases", "gateway_releases", "gateway_networks", "roles", "role_bindings"} {
		path := address + "/api/hypershell/v1/" + collection
		code, data := requestJSON(t, "GET", path+"?fields=id", admin, nil)
		var response struct{ Items []map[string]json.RawMessage }
		if code != 200 || json.Unmarshal(data, &response) != nil || len(response.Items) == 0 {
			t.Fatal("catalog selection failed", collection, code)
		}
		for _, item := range response.Items {
			if len(item) != 1 || len(item["id"]) == 0 {
				t.Fatal("catalog selection exposed extra fields", collection)
			}
		}
		if code, _ := requestJSON(t, "GET", path+"?fields=client_secret", admin, nil); code != 400 {
			t.Fatal("catalog accepted a private field", collection, code)
		}
	}
	client, connection := grpcClient(t, grpcAddress, tlsIdentity)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	full, err := client.GetGateway(metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+owner)), &pb.GetGatewayRequest{Id: ids["a-visible"]})
	if err != nil || full.GetGateway().GetClusterId() != f.cluster {
		t.Fatal("projection changed stored or gRPC fields", err)
	}
	connection.Close()
	stop()
	stop, address, _ = startBoth(t, binary, f.dsn, config, settings...)
	list(url.Values{"fields": {"id,name"}}, owner, 2, 2, "id", "name")
	if code, _ := requestJSON(t, "GET", address+root+"/"+ids["b-hidden"], owner, nil); code != 404 {
		t.Fatal("projection changed hidden read", code)
	}
	if code, _ := requestJSON(t, "DELETE", address+root+"/"+ids["a-visible"], owner, nil); code != 204 {
		t.Fatal("delete", code)
	}
	readGatewayEvent(t, consumer, ids["a-visible"], "Delete", "gateway.deleted")
	list(url.Values{"fields": {"name"}}, owner, 1, 1, "name")
}
