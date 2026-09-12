package acceptance

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/pem"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/contracts"
	"github.com/jsell-rh/hypershell-stego/internal/catalog"
	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"github.com/segmentio/ksuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
)

func TestGatewayNetworkDescriptorMatchesReference(t *testing.T) {
	reference, err := contracts.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	descriptor := pb.File_hypershell_v1_gateway_networks_proto
	expected := protodesc.ToFileDescriptorProto(reference.Proto.FindFileByPath(descriptor.Path()))
	actual := protodesc.ToFileDescriptorProto(descriptor)
	actual.Options.GoPackage, expected.Options.GoPackage = nil, nil
	actual.SourceCodeInfo, expected.SourceCodeInfo = nil, nil
	if !proto.Equal(actual, expected) {
		t.Fatal("network wire contract changed")
	}
}

func TestGatewayNetworkWorkflowThroughGeneratedRuntime(t *testing.T) {
	f := database(t)
	// Recreate only the added table through the deployment migration. This checks
	// an upgrade from the previous schema as well as the generated fresh schema.
	if _, err := f.db.Exec("DROP TABLE gateway_networks; UPDATE roles SET permissions=permissions - 'gateway_networks'"); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../migrations/000008_gateway_networks.sql")
	if err != nil {
		t.Fatal(err)
	}
	var roleID string
	if err := f.db.QueryRow("SELECT id FROM roles WHERE name='platform:admin'").Scan(&roleID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(string(migration)); err != nil {
		t.Fatal(err)
	}
	var firstTime, repeatTime time.Time
	if err := f.db.QueryRow("SELECT updated_time FROM roles WHERE id=$1", roleID).Scan(&firstTime); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(string(migration)); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow("SELECT updated_time FROM roles WHERE id=$1", roleID).Scan(&repeatTime); err != nil || !firstTime.Equal(repeatTime) {
		t.Fatal("network migration changed an existing role on repeat", err)
	}
	if _, err := f.db.Exec("INSERT INTO gateway_networks(id,name) VALUES($1,'')", ksuid.New().String()); err == nil {
		t.Fatal("network migration omitted the name constraint")
	}
	_, config := broker(t, identity(t, "localhost"))
	key, settings := issuer(t)
	tlsIdentity := identity(t, "localhost")
	directory := filepath.Dir(tlsIdentity.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"), `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["controller"]`)
	binary, cli := buildApplication(t), buildProgram(t, "./out/cli/cmd")
	stop, address, rpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	defer func() { stop() }()
	base := address + "/api/hypershell/v1"
	path := base + "/gateway_networks"
	admin, creator, outsider, controller := token(t, key, "operator", "platform:admin"), token(t, key, "alice", "gateway:creator"), token(t, key, "bob"), token(t, key, "controller")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	call := func(bearer string) context.Context {
		return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+bearer))
	}
	_, conn := grpcClient(t, rpcAddress, tlsIdentity)
	client := pb.NewGatewayNetworkServiceClient(conn)
	for _, bearer := range []string{admin, creator, outsider, controller} {
		currentUser(t, base, bearer)
	}
	for _, policy := range []struct {
		name    string
		actions []string
	}{
		{"platform:admin", []string{"create", "read", "update", "delete", "list"}},
		{"gateway:creator", []string{"read", "list"}},
	} {
		role := discoverRole(t, base, creator, policy.name)
		var permissions map[string][]string
		if json.Unmarshal(role.Permissions, &permissions) != nil || !slices.Equal(permissions["gateway_networks"], policy.actions) {
			t.Fatal("network role metadata differs")
		}
	}
	// A Gateway owner without the creator role cannot read shared network records.
	ownerCreate := token(t, key, "bob", "gateway:creator")
	request, _ := json.Marshal(f.request("owned-gateway"))
	if code, _ := requestJSON(t, "POST", base+"/gateways", ownerCreate, request); code != 201 {
		t.Fatal("owner fixture creation", code)
	}
	watch, err := client.WatchGatewayNetworks(call(creator), &pb.WatchGatewayNetworksRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := watch.Header(); err != nil {
		t.Fatal(err)
	}
	deniedWatch, err := client.WatchGatewayNetworks(call(outsider), &pb.WatchGatewayNetworksRequest{})
	if err == nil {
		_, err = deniedWatch.Recv()
	}
	if status.Code(err) != codes.PermissionDenied {
		t.Fatal("network watch allowed an outsider", err)
	}
	for _, bearer := range []string{creator, outsider} {
		if code, _ := requestJSON(t, "POST", path, bearer, []byte(`{"name":"denied"}`)); code != 403 {
			t.Fatal("network create policy", code)
		}
		if _, err := client.CreateGatewayNetwork(call(bearer), &pb.CreateGatewayNetworkRequest{Name: "denied"}); status.Code(err) != codes.PermissionDenied {
			t.Fatal("gRPC network create policy", err)
		}
	}
	if code, _ := requestJSON(t, "GET", path, "", nil); code != 401 {
		t.Fatal("network accepted an unsigned caller", code)
	}
	if code, _ := requestJSON(t, "GET", path, outsider, nil); code != 403 {
		t.Fatal("network list allowed an outsider", code)
	}
	if _, err := client.ListGatewayNetworks(call(outsider), &pb.ListGatewayNetworksRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatal("gRPC network list policy", err)
	}
	body := []byte(`{"name":"regional","topology":"hub-spoke","tunnel_mode":"wireguard","hub_gateway_id":"reference-only","status":"planned"}`)
	code, data := requestJSON(t, "POST", path, admin, body)
	var network httpapi.GatewayNetwork
	if code != 201 || json.Unmarshal(data, &network) != nil {
		t.Fatal("network creation", code, string(data))
	}
	id := network.ID
	if key, err := ksuid.Parse(id); err != nil || key == ksuid.Nil || network.Kind != "GatewayNetwork" || network.Href != "/api/hypershell/v1/gateway_networks/"+id || network.CreatedAt.IsZero() || network.UpdatedAt.IsZero() {
		t.Fatal("network metadata differs")
	}
	reference, err := contracts.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var document any
	schema := reference.OpenAPI.Paths.Value("/api/hypershell/v1/gateway_networks").Post.Responses.Status(201).Value.Content.Get("application/json").Schema.Value
	if json.Unmarshal(data, &document) != nil || schema.VisitJSON(document) != nil {
		t.Fatal("network response differs from reference")
	}
	notice, err := watch.Recv()
	if err != nil || notice.GetResourceId() != id || notice.GetType() != pb.EventType_EVENT_TYPE_CREATED || notice.GetGatewayNetwork().GetHubGatewayId() != "reference-only" {
		t.Fatal("network create watch", err)
	}
	readCatalogEvent(t, kafkaConsumer(t, config), id, "GatewayNetworks", "Create", "gatewaynetwork.created")
	got, err := client.GetGatewayNetwork(call(creator), &pb.GetGatewayNetworkRequest{Id: id})
	if err != nil || got.GetGatewayNetwork().GetTopology() != "hub-spoke" || !got.GetGatewayNetwork().GetMetadata().GetCreatedAt().AsTime().Equal(network.CreatedAt) {
		t.Fatal("network cross-transport read", err)
	}
	if code, _ := requestJSON(t, "GET", path+"/"+id, outsider, nil); code != 403 {
		t.Fatal("network read policy", code)
	}
	if _, err := client.GetGatewayNetwork(call(outsider), &pb.GetGatewayNetworkRequest{Id: id}); status.Code(err) != codes.PermissionDenied {
		t.Fatal("gRPC network read policy", err)
	}
	for _, bearer := range []string{creator, outsider} {
		if code, _ := requestJSON(t, "PATCH", path+"/"+id, bearer, []byte(`{"name":"denied"}`)); code != 403 {
			t.Fatal("network update policy", code)
		}
		if code, _ := requestJSON(t, "DELETE", path+"/"+id, bearer, nil); code != 403 {
			t.Fatal("network delete policy", code)
		}
		if _, err := client.UpdateGatewayNetwork(call(bearer), &pb.UpdateGatewayNetworkRequest{Id: id, Name: proto.String("denied")}); status.Code(err) != codes.PermissionDenied {
			t.Fatal("gRPC network update policy", err)
		}
		if _, err := client.DeleteGatewayNetwork(call(bearer), &pb.DeleteGatewayNetworkRequest{Id: id}); status.Code(err) != codes.PermissionDenied {
			t.Fatal("gRPC network delete policy", err)
		}
	}
	for _, bad := range []string{`{"name":""}`, `{"name":"bad","id":"chosen"}`, `{"name":"bad","fleet_id":"removed"}`, `{"name":"bad","topology":"a","topology":"b"}`, `{"name":"bad","status":"\u0000"}`} {
		if code, _ := requestJSON(t, "POST", path, admin, []byte(bad)); code != 400 {
			t.Fatal("invalid network input accepted", code)
		}
	}
	if _, err := client.CreateGatewayNetwork(call(admin), &pb.CreateGatewayNetworkRequest{Name: "bad", Topology: proto.String(strings.Repeat("x", 65))}); status.Code(err) != codes.InvalidArgument {
		t.Fatal("gRPC network validation", err)
	}
	// Both transports preserve absent patch fields. An explicit empty value is kept.
	if code, _ := requestJSON(t, "PATCH", path+"/"+id, admin, []byte(`{"topology":null,"status":"ready"}`)); code != 200 {
		t.Fatal("network REST patch", code)
	}
	notice, err = watch.Recv()
	if err != nil || notice.GetType() != pb.EventType_EVENT_TYPE_UPDATED || notice.GetGatewayNetwork().GetStatus() != "ready" || notice.GetGatewayNetwork().GetTopology() != "hub-spoke" {
		t.Fatal("network REST patch watch", err)
	}
	if _, err := client.UpdateGatewayNetwork(call(controller), &pb.UpdateGatewayNetworkRequest{Id: id, TunnelMode: proto.String("")}); status.Code(err) != codes.PermissionDenied {
		t.Fatal("controller changed network configuration", err)
	}
	if _, err := client.UpdateGatewayNetwork(call(admin), &pb.UpdateGatewayNetworkRequest{Id: id, TunnelMode: proto.String("")}); err != nil {
		t.Fatal(err)
	}
	notice, err = watch.Recv()
	if err != nil || notice.GetGatewayNetwork().TunnelMode == nil || notice.GetGatewayNetwork().GetTunnelMode() != "" {
		t.Fatal("network empty field lost", err)
	}
	// Every mutation must roll back when its outbox insert fails.
	for _, operation := range []struct{ method, suffix, body string }{{"POST", "created", `{"name":"rollback"}`}, {"PATCH", "updated", `{"name":"rollback"}`}, {"DELETE", "deleted", ""}} {
		awaitQueueEmpty(t, f)
		if _, err := f.db.Exec("ALTER TABLE stego_outbox.messages ADD CONSTRAINT reject_network_event CHECK(kind <> 'gatewaynetwork." + operation.suffix + "') NOT VALID"); err != nil {
			t.Fatal(err)
		}
		target := path + "/" + id
		if operation.method == "POST" {
			target = path
		}
		if code, _ := requestJSON(t, operation.method, target, admin, []byte(operation.body)); code != 500 {
			t.Fatal("network event failure response", operation.method, code)
		}
		var name string
		if err := f.db.QueryRow("SELECT name FROM gateway_networks WHERE id=$1 AND deleted_at IS NULL", id).Scan(&name); err != nil || name != "regional" || count(t, f.db, "gateway_networks") != 1 || count(t, f.db, "stego_outbox.messages") != 0 {
			t.Fatal("network event failure did not roll back", operation.method, err)
		}
		if _, err := f.db.Exec("ALTER TABLE stego_outbox.messages DROP CONSTRAINT reject_network_event"); err != nil {
			t.Fatal(err)
		}
	}
	// The CLI runs against verified TLS and uses the same generated API.
	target, _ := url.Parse(address)
	proxy := httptest.NewUnstartedServer(httputil.NewSingleHostReverseProxy(target))
	proxy.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
	proxy.StartTLS()
	defer proxy.Close()
	cliDir := t.TempDir()
	configPath := filepath.Join(cliDir, "config.json")
	caPath, tokenPath := filepath.Join(cliDir, "ca.pem"), filepath.Join(cliDir, "token")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: proxy.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenPath, []byte(admin), 0600); err != nil {
		t.Fatal(err)
	}
	cliRun := func(args ...string) []byte {
		t.Helper()
		callCtx, done := context.WithTimeout(ctx, 15*time.Second)
		defer done()
		command := exec.CommandContext(callCtx, cli, args...)
		command.Env = append(os.Environ(), "HYPERSHELL_CONFIG="+configPath, "GORACE=atexit_sleep_ms=0")
		var diagnostics bytes.Buffer
		command.Stderr = &diagnostics
		output, err := command.Output()
		if strings.Contains(string(output), admin) || strings.Contains(diagnostics.String(), admin) {
			t.Fatal("CLI exposed its token")
		}
		if err != nil {
			t.Fatalf("network CLI: %v %s", err, output)
		}
		if strings.Count(diagnostics.String(), `"event.name":"cli.command.completed"`) != 1 {
			t.Fatal("network CLI has no common completion record")
		}
		return output
	}
	cliRun("login", "--url", proxy.URL, "--token-file", tokenPath, "--ca-file", caPath)
	var cliNetwork httpapi.GatewayNetwork
	if json.Unmarshal(cliRun("create", "gatewayNetwork", "--name", "from-cli", "--topology", "mesh"), &cliNetwork) != nil || cliNetwork.ID == "" {
		t.Fatal("CLI network create")
	}
	cliRun("get", "gateway-network", cliNetwork.ID)
	var list struct {
		Total int
		Items []httpapi.GatewayNetwork
	}
	if json.Unmarshal(cliRun("list", "gatewayNetworks", "--search", "id = '"+cliNetwork.ID+"'", "--size", "1", "--order-by", "name asc"), &list) != nil || list.Total != 1 || len(list.Items) != 1 || list.Items[0].ID != cliNetwork.ID {
		t.Fatal("network filtered list")
	}
	cliRun("delete", "gateway-network", cliNetwork.ID, "--yes")
	cliRun("logout")
	readCatalogEvent(t, kafkaConsumer(t, config), cliNetwork.ID, "GatewayNetworks", "Delete", "gatewaynetwork.deleted")
	// A second network created over gRPC must have the same REST contract.
	if _, err := client.CreateGatewayNetwork(call(controller), &pb.CreateGatewayNetworkRequest{Name: "denied"}); status.Code(err) != codes.PermissionDenied {
		t.Fatal("controller created network configuration", err)
	}
	second, err := client.CreateGatewayNetwork(call(admin), &pb.CreateGatewayNetworkRequest{Name: "from-grpc", Topology: proto.String("mesh")})
	if err != nil {
		t.Fatal(err)
	}
	secondID := second.GetGatewayNetwork().GetMetadata().GetId()
	if code, data := requestJSON(t, "GET", path+"/"+secondID, creator, nil); code != 200 || !strings.Contains(string(data), `"name":"from-grpc"`) {
		t.Fatal("gRPC-created network REST read", code)
	}
	page, err := client.ListGatewayNetworks(call(creator), &pb.ListGatewayNetworksRequest{Size: 1})
	if err != nil || page.GetMetadata().GetTotal() != 2 || len(page.GetItems()) != 1 {
		t.Fatal("network gRPC page totals", err)
	}
	awaitQueueEmpty(t, f)
	conn.Close()
	stop()
	service, err := catalog.New(f.storage, f.service)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Networks.Update(ctx, principal("operator", "platform:admin"), id, catalog.NetworkPatch{Status: proto.String("offline")}); err != nil {
		t.Fatal(err)
	}
	if count(t, f.db, "stego_outbox.messages") != 1 {
		t.Fatal("network offline event was not retained")
	}
	var messageID string
	if err := f.db.QueryRow("SELECT id::text FROM stego_outbox.messages").Scan(&messageID); err != nil {
		t.Fatal(err)
	}
	stop, address, rpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	path = address + "/api/hypershell/v1/gateway_networks"
	readCatalogEvent(t, kafkaConsumer(t, config), id, "GatewayNetworks", "Update", "gatewaynetwork.updated", messageID)
	_, conn = grpcClient(t, rpcAddress, tlsIdentity)
	client = pb.NewGatewayNetworkServiceClient(conn)
	got, err = client.GetGatewayNetwork(call(creator), &pb.GetGatewayNetworkRequest{Id: id})
	if err != nil || got.GetGatewayNetwork().GetStatus() != "offline" {
		t.Fatal("network did not survive restart", err)
	}
	if code, data := requestJSON(t, "GET", path+"/"+id, creator, nil); code != 200 || !strings.Contains(string(data), `"status":"offline"`) {
		t.Fatal("network REST restart read", code)
	}
	watch, err = client.WatchGatewayNetworks(call(creator), &pb.WatchGatewayNetworksRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := watch.Header(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.DeleteGatewayNetwork(call(admin), &pb.DeleteGatewayNetworkRequest{Id: id}); err != nil {
		t.Fatal(err)
	}
	notice, err = watch.Recv()
	if err != nil || notice.GetType() != pb.EventType_EVENT_TYPE_DELETED || notice.GetResourceId() != id || notice.GetGatewayNetwork().GetName() != "regional" {
		t.Fatal("network delete watch", err)
	}
	readCatalogEvent(t, kafkaConsumer(t, config), id, "GatewayNetworks", "Delete", "gatewaynetwork.deleted")
	if code, _ := requestJSON(t, "GET", path+"/"+id, creator, nil); code != 404 {
		t.Fatal("deleted network remains visible", code)
	}
	if _, err := client.GetGatewayNetwork(call(creator), &pb.GetGatewayNetworkRequest{Id: id}); status.Code(err) != codes.NotFound {
		t.Fatal("gRPC deleted network remains visible", err)
	}
	if code, data := requestJSON(t, "DELETE", path+"/"+secondID, admin, nil); code != 204 || len(data) != 0 {
		t.Fatal("REST network deletion", code)
	}
	page, err = client.ListGatewayNetworks(call(creator), &pb.ListGatewayNetworksRequest{})
	if err != nil || page.GetMetadata().GetTotal() != 0 || len(page.GetItems()) != 0 {
		t.Fatal("network deletion list", err)
	}
	awaitQueueEmpty(t, f)
	t.Log("Network CRUD, access, events, rollback, CLI, watch, restart, and schema upgrade passed")
}
