package acceptance

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/pem"
	"fmt"
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

	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	command "github.com/jsell-rh/hypershell-stego/out/cli/command"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"github.com/segmentio/ksuid"
	"google.golang.org/grpc/metadata"
)

func TestGeneratedCLIApplyWorkflow(t *testing.T) {
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
	var requests atomic.Int64
	proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
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
	ca := filepath.Join(directory, "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: proxy.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	tokens := map[string]string{"admin": token(t, key, "operator", "platform:admin"), "alice": token(t, key, "alice", "gateway:creator"), "owner": token(t, key, "alice"), "bob": token(t, key, "bob")}
	configs := map[string]string{"offline": filepath.Join(directory, "absent.json")}
	for name := range tokens {
		configs[name] = filepath.Join(directory, name+".json")
	}
	run := func(user, input string, args ...string) ([]byte, string, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, cli, args...)
		cmd.Env = append(os.Environ(), "HYPERSHELL_CONFIG="+configs[user], "GORACE=atexit_sleep_ms=0")
		cmd.Stdin = strings.NewReader(input)
		var output, problem bytes.Buffer
		cmd.Stdout = &output
		cmd.Stderr = &problem
		err := cmd.Run()
		for _, value := range tokens {
			if bytes.Contains(output.Bytes(), []byte(value)) || bytes.Contains(problem.Bytes(), []byte(value)) {
				t.Fatal("apply exposed a token")
			}
		}
		if strings.Contains(problem.String(), "DATA RACE") {
			t.Fatal("CLI data race")
		}
		return output.Bytes(), problem.String(), err
	}
	success := func(user string, args ...string) []byte {
		t.Helper()
		data, problem, err := run(user, "", args...)
		if err != nil {
			t.Fatalf("CLI failed: %v %s", err, problem)
		}
		return data
	}
	apply := func(user, input string) ([]command.ApplyResult, string, error) {
		t.Helper()
		data, problem, err := run(user, input, "apply", "-f", "-", "-o", "json")
		var results []command.ApplyResult
		if len(data) > 0 && json.Unmarshal(data, &results) != nil {
			t.Fatal("invalid apply result")
		}
		return results, problem, err
	}
	catalogs := `kind: ManagedCluster
metadata: {name: apply-cluster}
spec: {provider: kubernetes, kubeconfig_secret: cluster-access}
---
kind: GatewayRelease
metadata: {name: apply-release}
spec: {image: registry.example/gateway:v1, canary_percent: 0}
---
kind: ManagedDatabase
metadata: {name: apply-database}
spec: {provider: cnpg, connection_secret: database-access}
---
kind: GatewayNetwork
metadata: {name: apply-network}
spec: {topology: mesh, status: planned}
`
	data, problem, err := run("offline", catalogs, "apply", "-f", "-", "--dry-run", "-o", "json")
	var dry []command.ApplyResult
	if err != nil || json.Unmarshal(data, &dry) != nil || len(dry) != 4 || requests.Load() != 0 || count(t, f.db, "managed_clusters") != 0 || count(t, f.db, "stego_outbox.messages") != 0 {
		t.Fatal("dry run wrote or required login", err, problem)
	}
	for _, result := range dry {
		if result.Status != "validated" {
			t.Fatal("dry run claimed a server operation")
		}
	}
	for _, name := range []string{"admin", "alice", "owner", "bob"} {
		file := filepath.Join(directory, name+".token")
		if err := os.WriteFile(file, []byte(tokens[name]), 0600); err != nil {
			t.Fatal(err)
		}
		success(name, "login", "--url", proxy.URL, "--token-file", file, "--ca-file", ca)
	}
	created, problem, err := apply("admin", catalogs)
	if err != nil || len(created) != 4 {
		t.Fatal("apply catalogs", err, problem)
	}
	ids := map[string]string{}
	for _, result := range created {
		if result.Status != "created" {
			t.Fatal("catalog not created")
		}
		if _, err := ksuid.Parse(result.ID); err != nil {
			t.Fatal(err)
		}
		ids[result.Kind] = result.ID
	}
	for _, event := range []struct{ kind, source, prefix string }{{"ManagedCluster", "ManagedClusters", "managedcluster"}, {"GatewayRelease", "GatewayReleases", "gatewayrelease"}, {"ManagedDatabase", "ManagedDatabases", "manageddatabase"}, {"GatewayNetwork", "GatewayNetworks", "gatewaynetwork"}} {
		readCatalogEvent(t, kafkaConsumer(t, brokerConfig), ids[event.kind], event.source, "Create", event.prefix+".created")
	}
	again, problem, err := apply("admin", catalogs)
	if err != nil || len(again) != 4 {
		t.Fatal("repeat catalog apply", err, problem)
	}
	for _, result := range again {
		if result.Status != "configured" || result.ID != ids[result.Kind] {
			t.Fatal("catalog apply did not retain identity")
		}
	}
	gatewayName := "apply' OR name = 'other"
	gatewayDocument := func(name, id, image, cluster string) string {
		identifier := ""
		if id != "" {
			identifier = "  id: " + id + "\n"
		}
		return fmt.Sprintf("apiVersion: hypershell/v1\nkind: Gateway\nmetadata:\n  name: %q\n%sspec:\n  cluster_id: %s\n  release_id: %s\n  database_id: ignored-placeholder\n  image: %s\n", name, identifier, cluster, ids["GatewayRelease"], image)
	}
	gatewayFile := gatewayDocument(gatewayName, "", "registry.example/gateway:v1", ids["ManagedCluster"])
	results, problem, err := apply("alice", gatewayFile)
	if err != nil || len(results) != 1 || results[0].Status != "created" {
		t.Fatal("apply Gateway", err, problem)
	}
	gatewayID := results[0].ID
	readEvent(t, kafkaConsumer(t, brokerConfig), gatewayID)
	var grants int
	if err := f.db.QueryRow("SELECT count(*) FROM role_bindings WHERE gateway_id=$1 AND deleted_at IS NULL", gatewayID).Scan(&grants); err != nil || grants != 1 {
		t.Fatal("apply did not commit the owner grant", err)
	}
	var gateway httpapi.Gateway
	if json.Unmarshal(success("owner", "get", "gateway", gatewayID), &gateway) != nil || gateway.Name != gatewayName || gateway.ClusterID != ids["ManagedCluster"] || gateway.ReleaseID != ids["GatewayRelease"] || gateway.DatabaseID != ids["ManagedDatabase"] {
		t.Fatal("apply Gateway placement differs")
	}
	rpc, connection := grpcClient(t, rpcAddress, rpcIdentity)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+tokens["owner"]))
	read, err := rpc.GetGateway(ctx, &pb.GetGatewayRequest{Id: gatewayID})
	if err != nil || read.GetGateway().GetName() != gatewayName || read.GetGateway().GetDatabaseId() != ids["ManagedDatabase"] {
		t.Fatal("gRPC apply read differs", err)
	}
	updated := gatewayDocument(gatewayName, "", "registry.example/gateway:v2", ids["ManagedCluster"])
	results, problem, err = apply("owner", updated)
	if err != nil || len(results) != 1 || results[0].ID != gatewayID || results[0].Status != "configured" {
		t.Fatal("owner apply patch", err, problem)
	}
	readCatalogEvent(t, kafkaConsumer(t, brokerConfig), gatewayID, "Gateways", "Update", "gateway.updated")
	awaitQueueEmpty(t, f)
	if _, err := f.db.Exec("ALTER TABLE stego_outbox.messages ADD CONSTRAINT reject_apply_create CHECK(kind <> 'gateway.created') NOT VALID"); err != nil {
		t.Fatal(err)
	}
	rejected, problem, err := apply("alice", gatewayDocument("reject-event", "", "registry.example/gateway:v1", ids["ManagedCluster"]))
	if err == nil || len(rejected) != 1 || rejected[0].Status != "unknown" || !strings.Contains(problem, "HTTP 500") {
		t.Fatal("apply event failure result", rejected, err, problem)
	}
	if count(t, f.db, "gateways") != 1 {
		t.Fatal("apply event failure did not roll back Gateway creation")
	}
	if _, err := f.db.Exec("ALTER TABLE stego_outbox.messages DROP CONSTRAINT reject_apply_create"); err != nil {
		t.Fatal(err)
	}
	// A later invalid document must stop the complete input before any API request.
	before := requests.Load()
	if _, _, err := apply("owner", updated+"---\nkind: Gateway\nmetadata: {name: invalid}\nspec: {misspelled: true}\n"); err == nil || requests.Load() != before {
		t.Fatal("invalid later document reached API")
	}
	// An invalid create contract found after lookup must stop all resource writes.
	if _, _, err := apply("alice", gatewayDocument(gatewayName, "", "registry.example/gateway:unwanted", ids["ManagedCluster"])+"---\nkind: Gateway\nmetadata: {name: missing-fields}\n"); err == nil {
		t.Fatal("missing create fields were accepted")
	}
	if json.Unmarshal(success("owner", "get", "gateway", gatewayID), &gateway) != nil || gateway.Image == nil || *gateway.Image != "registry.example/gateway:v2" {
		t.Fatal("preflight failure changed the existing Gateway")
	}
	if _, problem, err := apply("bob", gatewayDocument(gatewayName, gatewayID, "registry.example/gateway:denied", ids["ManagedCluster"])); err == nil || !strings.Contains(problem, "HTTP 404") {
		t.Fatal("apply exposed or changed an inaccessible Gateway", err, problem)
	}
	// Domain reference checks can still fail after an earlier resource committed.
	batch := gatewayDocument("first-applied", "", "registry.example/gateway:v1", ids["ManagedCluster"]) + "---\n" + gatewayDocument("reject-reference", "", "registry.example/gateway:v1", "missing-cluster") + "---\n" + gatewayDocument("not-attempted", "", "registry.example/gateway:v1", ids["ManagedCluster"])
	partial, problem, err := apply("alice", batch)
	if err == nil || len(partial) != 3 || partial[0].Status != "created" || partial[1].Status != "failed" || partial[2].Status != "not_attempted" || !strings.Contains(problem, "HTTP 400") {
		t.Fatal("partial apply result differs", partial, err, problem)
	}
	if count(t, f.db, "gateways") != 2 {
		t.Fatal("partial apply committed a rejected or unattempted Gateway")
	}
	firstID := partial[0].ID
	readEvent(t, kafkaConsumer(t, brokerConfig), firstID)
	// The API permits duplicate names. Apply must refuse to choose one by accident.
	var duplicate httpapi.Gateway
	data = success("alice", "create", "gateway", "--name", gatewayName, "--cluster-id", ids["ManagedCluster"], "--release-id", ids["GatewayRelease"], "--database-id", "placeholder")
	if json.Unmarshal(data, &duplicate) != nil || duplicate.ID == "" {
		t.Fatal("duplicate-name fixture")
	}
	if _, problem, err := apply("owner", updated); err == nil || !strings.Contains(problem, "ambiguous") {
		t.Fatal("apply accepted an ambiguous name", err, problem)
	}
	explicit := gatewayDocument(gatewayName, gatewayID, "registry.example/gateway:v3", ids["ManagedCluster"])
	results, problem, err = apply("owner", explicit)
	if err != nil || results[0].ID != gatewayID {
		t.Fatal("explicit apply target", err, problem)
	}
	awaitQueueEmpty(t, f)
	connection.Close()
	stop()
	stop, address, rpcAddress = startBoth(t, api, f.dsn, brokerConfig, settings...)
	backend.Store(address)
	rpc, connection = grpcClient(t, rpcAddress, rpcIdentity)
	read, err = rpc.GetGateway(ctx, &pb.GetGatewayRequest{Id: gatewayID})
	if err != nil || read.GetGateway().GetImage() != "registry.example/gateway:v3" {
		t.Fatal("applied state did not survive restart", err)
	}
	afterRestart := gatewayDocument(gatewayName, gatewayID, "registry.example/gateway:v4", ids["ManagedCluster"])
	results, problem, err = apply("owner", afterRestart)
	if err != nil || results[0].Status != "configured" || results[0].ID != gatewayID {
		t.Fatal("CLI apply after restart", err, problem)
	}
	read, err = rpc.GetGateway(ctx, &pb.GetGatewayRequest{Id: gatewayID})
	if err != nil || read.GetGateway().GetImage() != "registry.example/gateway:v4" {
		t.Fatal("gRPC differs after restarted apply", err)
	}
	for _, id := range []string{gatewayID, firstID, duplicate.ID} {
		success("owner", "delete", "gateway", id, "--yes")
	}
	// The default mode creates a dedicated database. Reapply must keep that ID.
	connection.Close()
	stop()
	defaults := append(append([]string{}, settings...), "DATABASE_PROVIDER=")
	stop, address, rpcAddress = startBoth(t, api, f.dsn, brokerConfig, defaults...)
	backend.Store(address)
	defaultInput := gatewayDocument("default-applied", "", "registry.example/gateway:v1", ids["ManagedCluster"])
	results, problem, err = apply("alice", defaultInput)
	if err != nil || len(results) != 1 || results[0].Status != "created" {
		t.Fatal("default apply creation", err, problem)
	}
	defaultID := results[0].ID
	var dedicated httpapi.Gateway
	if json.Unmarshal(success("owner", "get", "gateway", defaultID), &dedicated) != nil || dedicated.DatabaseID == "" || dedicated.DatabaseID == ids["ManagedDatabase"] {
		t.Fatal("default apply did not create a dedicated database")
	}
	databaseID := dedicated.DatabaseID
	readEvent(t, kafkaConsumer(t, brokerConfig), defaultID)
	readCatalogEvent(t, kafkaConsumer(t, brokerConfig), databaseID, "ManagedDatabases", "Create", "manageddatabase.created")
	results, problem, err = apply("owner", defaultInput)
	if err != nil || results[0].Status != "configured" || results[0].ID != defaultID {
		t.Fatal("default repeat apply", err, problem)
	}
	if json.Unmarshal(success("owner", "get", "gateway", defaultID), &dedicated) != nil || dedicated.DatabaseID != databaseID || count(t, f.db, "managed_databases") != 2 {
		t.Fatal("default apply changed database placement")
	}
	rpc, connection = grpcClient(t, rpcAddress, rpcIdentity)
	read, err = rpc.GetGateway(ctx, &pb.GetGatewayRequest{Id: defaultID})
	if err != nil || read.GetGateway().GetDatabaseId() != databaseID {
		t.Fatal("gRPC default apply placement", err)
	}
	success("owner", "delete", "gateway", defaultID, "--yes")
	success("admin", "delete", "managedDatabase", databaseID, "--yes")
	for kind, path := range map[string]string{"ManagedCluster": "managedCluster", "GatewayRelease": "gatewayRelease", "ManagedDatabase": "managedDatabase", "GatewayNetwork": "gatewayNetwork"} {
		success("admin", "delete", path, ids[kind], "--yes")
	}
	awaitQueueEmpty(t, f)
	for _, name := range []string{"admin", "alice", "owner", "bob"} {
		success(name, "logout")
	}
	t.Log("Apply created catalogs and a Gateway, preserved owner access, reported partial failure, rejected ambiguous names, delivered events, and survived restart")
}
