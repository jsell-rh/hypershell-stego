package acceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc/metadata"
)

func TestGatewayDeletionBeforeWorkloadStartup(t *testing.T) {
	k := kubernetesFixture(t)
	gatewayControllerRBAC(t, k)
	f := database(t)
	_, config := broker(t, identity(t, "localhost"))
	key, settings := issuer(t)
	apiTLS := identity(t, "localhost")
	directory := filepath.Dir(apiTLS.config.CAFile)
	settings = append(settings, "DATABASE_PROVIDER=deployment", `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["controller"]`, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"))
	binary := buildApplication(t)
	dbBinary := buildProgram(t, "./cmd/database-controller")
	workloadBinary := buildProgram(t, "./cmd/gateway-workload-controller")
	stopAPI, address, rpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	controllerToken := token(t, key, "controller")
	creator := token(t, key, "creator", "gateway:creator")
	stopDatabase, dbLogs := startDatabaseController(t, dbBinary, k, rpcAddress, apiTLS.config.CAFile, controllerToken)
	input, _ := json.Marshal(gateways.CreateRequest{Name: "deleted-before-startup", ClusterID: f.cluster, ReleaseID: f.release})
	code, body := requestJSON(t, "POST", address+"/api/hypershell/v1/gateways", creator, input)
	for attempt := 0; code == 409 && attempt < 5; attempt++ {
		time.Sleep(100 * time.Millisecond)
		code, body = requestJSON(t, "POST", address+"/api/hypershell/v1/gateways", creator, input)
	}
	var gateway httpapi.Gateway
	if code != 201 || json.Unmarshal(body, &gateway) != nil {
		t.Fatal("create Gateway", code, string(body))
	}
	namespace, err := gateways.DatabaseNamespace(gateway.DatabaseID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopDatabase()
		stopAPI()
		k.must(t, "", "delete", "namespace", namespace, "--ignore-not-found=true", "--wait=false")
	})
	_, connection := grpcClient(t, rpcAddress, apiTLS)
	databases := pb.NewManagedDatabaseServiceClient(connection)
	deadline := time.Now().Add(150 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+controllerToken)), 3*time.Second)
		response, err := databases.GetManagedDatabase(ctx, &pb.GetManagedDatabaseRequest{Id: gateway.DatabaseID})
		cancel()
		if err == nil && response.GetManagedDatabase().GetStatus() == "ready" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("database did not become ready: %v\n%s", err, dbLogs())
		}
		time.Sleep(time.Second)
	}
	stopDatabase()
	if got := k.must(t, "", "get", "namespace", gateway.Namespace, "--ignore-not-found=true", "-o", "name"); len(bytes.TrimSpace(got)) != 0 {
		t.Fatal("Gateway already has resources")
	}
	var ns struct {
		Metadata struct {
			Labels      map[string]string
			Annotations map[string]string
		}
	}
	if json.Unmarshal(k.must(t, "", "get", "namespace", namespace, "-o", "json"), &ns) != nil {
		t.Fatal("read database namespace")
	}
	if ns.Metadata.Labels["hypershell.redhat.io/gateway-id"] != "" || ns.Metadata.Annotations["hypershell.redhat.io/gateway-keys"] != "" {
		t.Fatal("database already has a Gateway link")
	}
	code, body = requestJSON(t, "DELETE", address+"/api/hypershell/v1/gateways/"+gateway.ID, creator, nil)
	if code != 204 {
		t.Fatal("delete Gateway", code, string(body))
	}
	// No queued event can supply the deleted ID after API restart.
	awaitQueueEmpty(t, f)
	connection.Close()
	stopAPI()
	stopAPI, _, rpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	stopDatabase, dbLogs = startDatabaseController(t, dbBinary, k, rpcAddress, apiTLS.config.CAFile, controllerToken)
	workloadSettings := []string{"HYPERSHELL_MANAGED_CLUSTER_ID=" + f.cluster, "HYPERSHELL_GATEWAY_CLUSTER_ISSUER=" + k.options.ClusterIssuer, "HYPERSHELL_GATEWAY_OIDC_ISSUER=https://unused.invalid/realm", "HYPERSHELL_GATEWAY_TRUST_BUNDLE=" + apiTLS.config.CAFile, "HYPERSHELL_GATEWAY_SANDBOX_IMAGE=" + sandboxImage, "HYPERSHELL_GATEWAY_SUPERVISOR_IMAGE=" + supervisorImage}
	stopWorkload, logs := startDatabaseController(t, workloadBinary, k, rpcAddress, apiTLS.config.CAFile, controllerToken, workloadSettings...)
	defer stopWorkload()
	deadline = time.Now().Add(45 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		output, err := k.command(ctx, "", "get", "namespace", namespace, "--ignore-not-found=true", "-o", "name")
		cancel()
		if err == nil && len(bytes.TrimSpace(output)) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Gateway deletion before first observation was lost: %v\n%s\n%s", err, logs(), dbLogs())
		}
		time.Sleep(time.Second)
	}
	var deleted bool
	if err := f.db.QueryRow("SELECT deleted_at IS NOT NULL FROM managed_databases WHERE id=$1", gateway.DatabaseID).Scan(&deleted); err != nil || !deleted {
		t.Fatal("database was not deleted through the API", err)
	}
	t.Log("Gateway deletion before workload startup survived API restart and removed its database")
}
