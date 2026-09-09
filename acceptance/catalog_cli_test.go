package acceptance

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/pem"
	"net/http"
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

	"github.com/jsell-rh/hypershell-stego/internal/catalog"
	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"github.com/segmentio/ksuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestGeneratedCLICatalogWorkflow(t *testing.T) {
	f := databaseSetup(t, false)
	_, brokerConfig := broker(t, identity(t, "localhost"))
	key, settings := issuer(t)
	rpcIdentity := identity(t, "localhost")
	tlsDir := filepath.Dir(rpcIdentity.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(tlsDir, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(tlsDir, "server-key.pem"))
	api, cli := buildApplication(t), buildProgram(t, "./out/cli/cmd")
	stop, address, rpcAddress := startBoth(t, api, f.dsn, brokerConfig, settings...)
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
	ca := filepath.Join(directory, "api-ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: proxy.Certificate().Raw}), 0644); err != nil {
		t.Fatal(err)
	}
	tokens := map[string]string{"alice": token(t, key, "alice", "gateway:creator"), "bob": token(t, key, "bob"), "admin": token(t, key, "operator", "platform:admin")}
	configs := map[string]string{}
	run := func(user string, args ...string) ([]byte, string, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, cli, args...)
		cmd.Env = append(os.Environ(), "HYPERSHELL_CONFIG="+configs[user], "GORACE=atexit_sleep_ms=0")
		var output, problem bytes.Buffer
		cmd.Stdout, cmd.Stderr = &output, &problem
		err := cmd.Run()
		if strings.Contains(problem.String(), "DATA RACE") {
			t.Fatal("CLI data race")
		}
		for _, token := range tokens {
			if bytes.Contains(output.Bytes(), []byte(token)) || bytes.Contains(problem.Bytes(), []byte(token)) {
				t.Fatal("CLI exposed a token")
			}
		}
		return output.Bytes(), problem.String(), err
	}
	success := func(user string, args ...string) []byte {
		t.Helper()
		data, problem, err := run(user, args...)
		if err != nil {
			t.Fatalf("CLI request failed: %v %s", err, problem)
		}
		return data
	}
	denied := func(user, code string, args ...string) {
		t.Helper()
		data, problem, err := run(user, args...)
		if err == nil || len(data) != 0 || !strings.Contains(problem, "HTTP "+code) {
			t.Fatalf("CLI request did not return HTTP %s: %v %s", code, err, problem)
		}
	}
	for _, name := range []string{"admin", "alice", "bob"} {
		configs[name] = filepath.Join(directory, name+".json")
		file := filepath.Join(directory, name+".token")
		if err := os.WriteFile(file, []byte(tokens[name]), 0600); err != nil {
			t.Fatal(err)
		}
		success(name, "login", "--url", proxy.URL, "--token-file", file, "--ca-file", ca)
		success(name, "get", "current-user")
	}
	entries := []struct {
		name, alias, path, kind, source, event, id string
		args                                       []string
	}{
		{name: "managedCluster", alias: "managed-cluster", path: "managed_clusters", kind: "ManagedCluster", source: "ManagedClusters", event: "managedcluster", args: []string{"--name", "cli-cluster", "--provider", "kubernetes", "--region", "east", "--kubeconfig-secret", "cluster-access", "--status", "ready", "--api-server-url", "https://cluster.example.test:6443"}},
		{name: "gatewayRelease", alias: "gateway-release", path: "gateway_releases", kind: "GatewayRelease", source: "GatewayReleases", event: "gatewayrelease", args: []string{"--name", "cli-release", "--image", "registry.example/gateway:v1", "--rollout-strategy", "canary", "--canary-percent", "0", "--canary-duration", "5m", "--status", "ready"}},
		{name: "managedDatabase", alias: "managed-database", path: "managed_databases", kind: "ManagedDatabase", source: "ManagedDatabases", event: "manageddatabase", args: []string{"--name", "cli-database", "--provider", "cnpg", "--region", "east", "--engine", "postgresql", "--engine-version", "18", "--instance-class", "small", "--connection-secret", "database-access", "--status", "ready"}},
	}
	for i := range entries {
		entry := &entries[i]
		if count(t, f.db, entry.path) != 0 {
			t.Fatal("catalog fixture was not empty")
		}
		args := append([]string{"create", entry.name}, entry.args...)
		denied("alice", "403", args...)
		var record httpapi.Reference
		body := success("admin", args...)
		if json.Unmarshal(body, &record) != nil || record.Kind != entry.kind || record.Href != "/api/hypershell/v1/"+entry.path+"/"+record.ID || record.CreatedAt.IsZero() || record.UpdatedAt.IsZero() {
			t.Fatal("catalog CLI response shape differs from the API")
		}
		if _, err := ksuid.Parse(record.ID); err != nil {
			t.Fatal(err)
		}
		entry.id = record.ID
		readCatalogEvent(t, kafkaConsumer(t, brokerConfig), entry.id, entry.source, "Create", entry.event+".created")
		success("alice", "get", entry.name+"s", entry.id)
		denied("bob", "403", "get", entry.alias, entry.id)
		denied("bob", "403", "list", entry.alias+"s")
		var list struct {
			Total int
			Items []httpapi.Reference
		}
		if json.Unmarshal(success("alice", "list", entry.alias+"s", "--size", "1", "--search", "id = '"+entry.id+"'", "--order-by", "name asc"), &list) != nil || list.Total != 1 || len(list.Items) != 1 || list.Items[0].ID != entry.id {
			t.Fatal("CLI catalog filtering differs")
		}
		denied("alice", "403", "delete", entry.name, entry.id, "--yes")
	}
	var release httpapi.GatewayRelease
	if json.Unmarshal(success("alice", "get", "gateway-release", entries[1].id), &release) != nil || release.CanaryPercent == nil || *release.CanaryPercent != 0 || release.Image != "registry.example/gateway:v1" {
		t.Fatal("CLI lost the zero canary percentage")
	}
	var database httpapi.ManagedDatabase
	namespace, err := catalog.DatabaseNamespace(entries[2].id)
	if err != nil || json.Unmarshal(success("alice", "get", "managedDatabase", entries[2].id), &database) != nil || database.Namespace != namespace || database.ConnectionSecret == nil || *database.ConnectionSecret != "database-access" {
		t.Fatal("CLI database namespace or secret reference differs")
	}
	denied("admin", "400", "create", "gatewayRelease", "--name", "bad-range", "--image", "registry.example/gateway:v2", "--canary-percent", "101")
	denied("admin", "400", "create", "gatewayRelease", "--name", "bad-width", "--image", "registry.example/gateway:v2", "--canary-percent", "2147483648")
	if _, _, err := run("admin", "create", "managedDatabase", "--name", "bad-namespace", "--provider", "cnpg", "--namespace", "chosen"); err == nil {
		t.Fatal("CLI accepted a server-owned namespace")
	}
	awaitQueueEmpty(t, f)
	if _, err := f.db.Exec("ALTER TABLE stego_outbox.messages ADD CONSTRAINT reject_cli_catalog_create CHECK (kind <> 'gatewayrelease.created') NOT VALID"); err != nil {
		t.Fatal(err)
	}
	denied("admin", "500", "create", "gatewayRelease", "--name", "rollback", "--image", "registry.example/gateway:v2")
	if count(t, f.db, "gateway_releases") != 1 || count(t, f.db, "stego_outbox.messages") != 0 {
		t.Fatal("CLI catalog creation did not roll back with its event")
	}
	if _, err := f.db.Exec("ALTER TABLE stego_outbox.messages DROP CONSTRAINT reject_cli_catalog_create"); err != nil {
		t.Fatal(err)
	}
	bodyFile := filepath.Join(directory, "release.json")
	if err := os.WriteFile(bodyFile, []byte(`{"name":"nullable","image":"registry.example/gateway:v2","canary_percent":null,"status":null}`), 0600); err != nil {
		t.Fatal(err)
	}
	var nullable httpapi.GatewayRelease
	if json.Unmarshal(success("admin", "create", "gateway-release", "--body", bodyFile), &nullable) != nil || nullable.ID == "" || nullable.CanaryPercent != nil || nullable.Status != nil {
		t.Fatal("CLI rejected or changed nullable catalog fields")
	}
	success("admin", "delete", "gateway-release", nullable.ID, "--yes")
	var gateway httpapi.Gateway
	data := success("alice", "create", "gateway", "--name", "catalog-cli", "--cluster-id", entries[0].id, "--release-id", entries[1].id, "--database-id", entries[2].id)
	if json.Unmarshal(data, &gateway) != nil || gateway.ID == "" || gateway.ClusterID != entries[0].id || gateway.ReleaseID != entries[1].id || gateway.DatabaseID != entries[2].id {
		t.Fatal("CLI Gateway did not use the returned catalog IDs")
	}
	gatewayEvents := kafkaConsumer(t, brokerConfig)
	readEvent(t, gatewayEvents, gateway.ID)
	for _, entry := range entries {
		denied("admin", "409", "delete", entry.alias, entry.id, "--yes")
	}
	stop()
	stop, address, rpcAddress = startBoth(t, api, f.dsn, brokerConfig, settings...)
	backend.Store(address)
	rpc, connection := grpcClient(t, rpcAddress, rpcIdentity)
	defer connection.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+tokens["alice"]))
	got, err := rpc.GetGateway(ctx, &pb.GetGatewayRequest{Id: gateway.ID})
	if err != nil || got.GetGateway().GetClusterId() != entries[0].id || got.GetGateway().GetReleaseId() != entries[1].id || got.GetGateway().GetDatabaseId() != entries[2].id {
		t.Fatal("gRPC placement differs after CLI creation and restart", err)
	}
	cluster, err := pb.NewManagedClusterServiceClient(connection).GetManagedCluster(ctx, &pb.GetManagedClusterRequest{Id: entries[0].id})
	if err != nil || cluster.GetManagedCluster().GetKubeconfigSecret() != "cluster-access" {
		t.Fatal("gRPC catalog differs after restart", err)
	}
	for _, entry := range entries {
		success("alice", "get", entry.name, entry.id)
	}
	success("alice", "delete", "gateway", gateway.ID, "--yes")
	readGatewayEvent(t, gatewayEvents, gateway.ID, "Delete", "gateway.deleted")
	if _, err := rpc.GetGateway(ctx, &pb.GetGatewayRequest{Id: gateway.ID}); status.Code(err) != codes.NotFound {
		t.Fatal("gRPC retained a deleted Gateway", err)
	}
	// The default creates a dedicated database. The client ID is a placeholder.
	connection.Close()
	stop()
	defaultSettings := append(append([]string{}, settings...), "DATABASE_PROVIDER=")
	stop, address, rpcAddress = startBoth(t, api, f.dsn, brokerConfig, defaultSettings...)
	backend.Store(address)
	var dedicated httpapi.Gateway
	data = success("alice", "create", "gateway", "--name", "default-catalog-cli", "--cluster-id", entries[0].id, "--release-id", entries[1].id, "--database-id", entries[2].id)
	if json.Unmarshal(data, &dedicated) != nil || dedicated.ID == "" || dedicated.DatabaseID == "" || dedicated.DatabaseID == entries[2].id {
		t.Fatal("default CLI creation did not select a dedicated database")
	}
	var placement httpapi.ManagedDatabase
	if json.Unmarshal(success("alice", "get", "managed-database", dedicated.DatabaseID), &placement) != nil || placement.Provider != "deployment" {
		t.Fatal("default CLI database provider differs")
	}
	expectedNamespace, err := catalog.DatabaseNamespace(dedicated.DatabaseID)
	if err != nil || placement.Namespace != expectedNamespace {
		t.Fatal("default database namespace differs")
	}
	readEvent(t, kafkaConsumer(t, brokerConfig), dedicated.ID)
	readCatalogEvent(t, kafkaConsumer(t, brokerConfig), dedicated.DatabaseID, "ManagedDatabases", "Create", "manageddatabase.created")
	denied("admin", "409", "delete", "managedDatabase", dedicated.DatabaseID, "--yes")
	defaultRPC, defaultConnection := grpcClient(t, rpcAddress, rpcIdentity)
	defaultCtx, defaultCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defaultCtx = metadata.NewOutgoingContext(defaultCtx, metadata.Pairs("authorization", "Bearer "+tokens["alice"]))
	defaultGateway, err := defaultRPC.GetGateway(defaultCtx, &pb.GetGatewayRequest{Id: dedicated.ID})
	defaultCancel()
	defaultConnection.Close()
	if err != nil || defaultGateway.GetGateway().GetDatabaseId() != dedicated.DatabaseID {
		t.Fatal("gRPC default placement differs", err)
	}
	success("alice", "delete", "gateway", dedicated.ID, "--yes")
	success("admin", "delete", "managed-database", dedicated.DatabaseID, "--yes")
	readCatalogEvent(t, kafkaConsumer(t, brokerConfig), dedicated.DatabaseID, "ManagedDatabases", "Delete", "manageddatabase.deleted")
	for _, entry := range entries {
		if _, _, err := run("admin", "delete", entry.name, entry.id); err == nil {
			t.Fatal("catalog deletion did not require confirmation")
		}
		success("admin", "delete", entry.name, entry.id, "--yes")
		readCatalogEvent(t, kafkaConsumer(t, brokerConfig), entry.id, entry.source, "Delete", entry.event+".deleted")
		denied("alice", "404", "get", entry.alias, entry.id)
		var list struct{ Total int }
		if json.Unmarshal(success("alice", "list", entry.name+"s"), &list) != nil || list.Total != 0 {
			t.Fatal("CLI retained a deleted catalog record")
		}
	}
	awaitQueueEmpty(t, f)
	for _, name := range []string{"admin", "alice", "bob"} {
		success(name, "logout")
	}
	t.Log("The generated CLI created placement catalogs and a Gateway, enforced access and references, delivered events, survived restart, and removed the resources")
}
