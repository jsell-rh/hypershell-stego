package acceptance

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gatewaynetwork"
	"github.com/jsell-rh/hypershell-stego/out/auth"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// awaitNetworkStatus polls the public read until the reconciler settles the
// network's control-plane-owned status field.
func awaitNetworkStatus(t testing.TB, ctx context.Context, client pb.GatewayNetworkServiceClient, id, want string) *pb.GatewayNetwork {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		response, err := client.GetGatewayNetwork(ctx, &pb.GetGatewayNetworkRequest{Id: id})
		if err != nil {
			t.Fatal(err)
		}
		if response.GetGatewayNetwork().GetStatus() == want {
			return response.GetGatewayNetwork()
		}
		if time.Now().After(deadline) {
			t.Fatalf("network status did not settle to %q: %q", want, response.GetGatewayNetwork().GetStatus())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestGatewayNetworkReconciliationThroughGeneratedRuntime(t *testing.T) {
	f := database(t)
	_, config := broker(t, identity(t, "localhost"))
	key, settings := issuer(t)
	apiTLS := identity(t, "localhost")
	directory := filepath.Dir(apiTLS.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"), `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["controller","ungranted-controller"]`)
	settings = withControllerWriteGrants(t, settings, auth.Grant{Subject: "controller", Resource: "GatewayNetwork", Operation: "observe.network", Target: ""})
	binary := buildApplication(t)
	stop, _, rpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	defer func() { stop() }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	call := func(bearer string) context.Context {
		return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+bearer))
	}
	_, conn := grpcClient(t, rpcAddress, apiTLS)
	networks := pb.NewGatewayNetworkServiceClient(conn)
	gateways := pb.NewGatewayServiceClient(conn)
	identityClient := control.NewGatewayIdentityServiceClient(conn)

	// The hub reference must point at a live Gateway.
	hub, err := gateways.CreateGateway(call(token(t, key, "alice", "gateway:creator")), &pb.CreateGatewayRequest{Name: "hub", ClusterId: f.cluster, ReleaseId: f.release, SupervisorImage: proto.String("supervisor:v1"), CredentialDriver: proto.String(`{"type":"test"}`), ServerDnsNames: []string{"hub.example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	hubID := hub.GetGateway().GetMetadata().GetId()

	// Run the reconciler in-process against the generated transports. The
	// controller context carries the authenticated controller identity.
	admin := token(t, key, "operator", "platform:admin")
	readCtx := call(admin)
	work, cancelWork := context.WithCancel(call(token(t, key, "controller")))
	defer cancelWork()
	controller, err := gatewaynetwork.New(networks, gateways, identityClient)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- controller.Run(work) }()
	defer func() {
		cancelWork()
		select {
		case err := <-done:
			if err != nil && status.Code(err) != codes.Canceled {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("network controller did not join")
		}
	}()

	create := func(name string, topology, hubGatewayID string) string {
		t.Helper()
		request := &pb.CreateGatewayNetworkRequest{Name: name}
		if topology != "" {
			request.Topology = proto.String(topology)
		}
		if hubGatewayID != "" {
			request.HubGatewayId = proto.String(hubGatewayID)
		}
		created, err := networks.CreateGatewayNetwork(call(admin), request)
		if err != nil {
			t.Fatal(err)
		}
		return created.GetGatewayNetwork().GetMetadata().GetId()
	}

	// A mesh network settles to Valid.
	meshID := create("mesh", "mesh", "")
	awaitNetworkStatus(t, readCtx, networks, meshID, "Valid")

	// A hub-spoke network with a live hub settles to Valid.
	spokeID := create("spokes", "hub-spoke", hubID)
	awaitNetworkStatus(t, readCtx, networks, spokeID, "Valid")

	// Empty topology is a deterministic Invalid.
	missingID := create("missing", "", "")
	awaitNetworkStatus(t, readCtx, networks, missingID, "Invalid: topology is required")

	// An unrecognized topology is a deterministic Invalid.
	ringID := create("ring", "ring", "")
	awaitNetworkStatus(t, readCtx, networks, ringID, `Invalid: unrecognized topology "ring"`)

	// hub-spoke without a hub reference is a deterministic Invalid.
	anonymousID := create("anonymous", "hub-spoke", "")
	awaitNetworkStatus(t, readCtx, networks, anonymousID, "Invalid: hub-spoke network requires a hub_gateway_id")

	// A dangling hub reference is a deterministic Invalid, not a retry.
	danglingID := create("dangling", "hub-spoke", f.release)
	awaitNetworkStatus(t, readCtx, networks, danglingID, `Invalid: hub gateway "`+f.release+`" does not exist`)

	// Repairing the topology converges back to Valid.
	if _, err := networks.UpdateGatewayNetwork(call(admin), &pb.UpdateGatewayNetworkRequest{Id: anonymousID, Topology: proto.String("mesh")}); err != nil {
		t.Fatal(err)
	}
	awaitNetworkStatus(t, readCtx, networks, anonymousID, "Valid")

	// The reconciler writes no redundant status row or event: after the queue
	// drains, a resync cycle that finds no change must not grow the outbox.
	awaitQueueEmpty(t, f)
	events := count(t, f.db, "stego_outbox.messages")
	time.Sleep(gatewaynetwork.ResyncInterval + 2*time.Second)
	if count(t, f.db, "stego_outbox.messages") != events {
		t.Fatal("reconciler repeated an unchanged status")
	}
	var stored string
	if err := f.db.QueryRow("SELECT status FROM gateway_networks WHERE id=$1", meshID).Scan(&stored); err != nil || stored != "Valid" {
		t.Fatal("reconciler did not persist the network status", err, stored)
	}

	// A control-plane subject without the exact grant is denied. The request
	// carries a plausible revision so the check exercises the grant gate.
	denied := metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+token(t, key, "ungranted-controller"), "if-resource-version", "1"))
	if _, err := identityClient.ObserveGatewayNetworkStatus(
		denied,
		&control.ObserveGatewayNetworkStatusRequest{Id: meshID, Status: "Valid"}); status.Code(err) != codes.PermissionDenied {
		t.Fatal("ungranted control-plane subject observed a network status", err)
	}

	// A non-control-plane admin is denied on the observation path.
	adminDenied := metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+admin, "if-resource-version", "1"))
	if _, err := identityClient.ObserveGatewayNetworkStatus(
		adminDenied,
		&control.ObserveGatewayNetworkStatusRequest{Id: meshID, Status: "Valid"}); status.Code(err) != codes.PermissionDenied {
		t.Fatal("admin observed a network status", err)
	}

	// The public status write path stays closed to controllers: the catalog
	// rejects control-plane subjects on UpdateGatewayNetwork.
	if _, err := networks.UpdateGatewayNetwork(call(token(t, key, "controller")), &pb.UpdateGatewayNetworkRequest{Id: meshID, Status: proto.String("Valid")}); status.Code(err) != codes.PermissionDenied {
		t.Fatal("controller changed network status through the catalog", err)
	}
	t.Log("Network reconciliation settled all validation verdicts, converged a repair, skipped redundant writes, and denied ungranted subjects")
}
