package acceptance

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/out/auth"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"github.com/segmentio/ksuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// TestClusterIdentityScopesClusterBoundReads proves the fork-native cluster
// binding through the generated runtime: a control-plane subject with a
// Gateway controller-write grant that has a non-empty target must scope
// ListGateways, WatchGateways, and ListGatewayReconcileIDs to that cluster.
// Subjects without such grants keep the cluster ID as an optional filter.
func TestClusterIdentityScopesClusterBoundReads(t *testing.T) {
	f := database(t)
	_, config := broker(t, identity(t, "localhost"))
	key, settings := issuer(t)
	apiTLS := identity(t, "localhost")
	directory := filepath.Dir(apiTLS.config.CAFile)
	// "bound" holds one Gateway write grant scoped to f.cluster. "fleet"
	// holds a configure grant with an empty target, so it stays fleet-wide.
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"), `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["bound","fleet"]`)
	settings = withControllerWriteGrants(t, settings,
		writeGrant("bound", "configure.workload", f.cluster),
		auth.Grant{Subject: "fleet", Resource: "Gateway", Operation: "configure.identity", Target: ""},
	)
	binary := buildApplication(t)
	stop, _, rpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	defer func() { stop() }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	call := func(bearer string) context.Context {
		return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+bearer))
	}
	_, conn := grpcClient(t, rpcAddress, apiTLS)
	gateways := pb.NewGatewayServiceClient(conn)
	identityClient := control.NewGatewayIdentityServiceClient(conn)

	creator := token(t, key, "alice", "gateway:creator")
	foreignCluster := ksuid.New().String()
	if err := f.storage.Create(ctx, "ManagedCluster", model.ManagedCluster{Meta: model.Meta{ID: foreignCluster}, Name: "foreign", Provider: "kubernetes", KubeconfigSecret: "unused"}); err != nil {
		t.Fatal(err)
	}
	create := func(name, cluster string) string {
		t.Helper()
		response, err := gateways.CreateGateway(call(creator), &pb.CreateGatewayRequest{Name: name, ClusterId: cluster, ReleaseId: f.release, SupervisorImage: proto.String("supervisor:v1"), CredentialDriver: proto.String(`{"type":"test"}`), ServerDnsNames: []string{name + ".example.test"}})
		if err != nil {
			t.Fatal(err)
		}
		return response.GetGateway().GetMetadata().GetId()
	}
	homeID := create("home", f.cluster)
	foreignID := create("foreign", foreignCluster)
	// A bound subject without a cluster ID must not list the fleet.
	if _, err := gateways.ListGateways(call(token(t, key, "bound")), &pb.ListGatewaysRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("bound subject listed without a cluster ID: %v", err)
	}
	// A bound subject with a foreign cluster must be denied.
	if _, err := gateways.ListGateways(call(token(t, key, "bound")), &pb.ListGatewaysRequest{ClusterId: proto.String(foreignCluster)}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("bound subject listed a foreign cluster: %v", err)
	}
	// A bound subject with its own cluster sees only that cluster.
	page, err := gateways.ListGateways(call(token(t, key, "bound")), &pb.ListGatewaysRequest{ClusterId: proto.String(f.cluster)})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].GetMetadata().GetId() != homeID {
		t.Fatalf("bound cluster page: %v", page.Items)
	}
	// A fleet-wide subject narrows the same way, but may also list all.
	narrowed, err := gateways.ListGateways(call(token(t, key, "fleet")), &pb.ListGatewaysRequest{ClusterId: proto.String(foreignCluster)})
	if err != nil {
		t.Fatal(err)
	}
	if len(narrowed.Items) != 1 || narrowed.Items[0].GetMetadata().GetId() != foreignID {
		t.Fatalf("narrowed fleet page: %v", narrowed.Items)
	}
	// A bound subject with an unknown cluster is denied; a fleet-wide
	// subject sees no rows.
	if _, err := gateways.ListGateways(call(token(t, key, "bound")), &pb.ListGatewaysRequest{ClusterId: proto.String(ksuid.New().String())}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("bound subject listed an unknown cluster: %v", err)
	}
	empty, err := gateways.ListGateways(call(token(t, key, "fleet")), &pb.ListGatewaysRequest{ClusterId: proto.String(ksuid.New().String())})
	if err != nil || len(empty.GetItems()) != 0 {
		t.Fatalf("unknown cluster page for fleet subject: %v %v", empty.GetItems(), err)
	}

	// The watch must enforce the same rule on the server side. A stream
	// RPC reports the handler error on the header or the first receive.
	watchDenied := func(request *pb.WatchGatewaysRequest, want codes.Code) {
		t.Helper()
		stream, err := gateways.WatchGateways(call(token(t, key, "bound")), request)
		if err == nil {
			_, err = stream.Recv()
		}
		if status.Code(err) != want {
			t.Fatalf("bound watch: %v, want %v", err, want)
		}
	}
	watchDenied(&pb.WatchGatewaysRequest{}, codes.InvalidArgument)
	watchDenied(&pb.WatchGatewaysRequest{ClusterId: proto.String(foreignCluster)}, codes.PermissionDenied)
	watch, err := gateways.WatchGateways(call(token(t, key, "bound")), &pb.WatchGatewaysRequest{ClusterId: proto.String(f.cluster)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := watch.Header(); err != nil {
		t.Fatal(err)
	}
	// A Gateway created in the other cluster must not reach the stream.
	if _, err := gateways.CreateGateway(call(creator), &pb.CreateGatewayRequest{Name: "unrelated", ClusterId: foreignCluster, ReleaseId: f.release, SupervisorImage: proto.String("supervisor:v1"), CredentialDriver: proto.String(`{"type":"test"}`), ServerDnsNames: []string{"unrelated.example.test"}}); err != nil {
		t.Fatal(err)
	}
	// A Gateway created in the bound cluster must reach the stream.
	if _, err := gateways.CreateGateway(call(creator), &pb.CreateGatewayRequest{Name: "watched", ClusterId: f.cluster, ReleaseId: f.release, SupervisorImage: proto.String("supervisor:v1"), CredentialDriver: proto.String(`{"type":"test"}`), ServerDnsNames: []string{"watched.example.test"}}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		event, err := watch.Recv()
		if err != nil {
			t.Fatal(err)
		}
		if event.GetGateway().GetName() == "unrelated" {
			t.Fatal("cluster-scoped watch delivered a foreign-cluster Gateway")
		}
		if event.GetGateway().GetName() == "watched" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cluster-scoped watch did not deliver the bound-cluster Gateway")
		}
	}

	// ReconcileIDs follows the same binding.
	if _, err := identityClient.ListGatewayReconcileIDs(call(token(t, key, "bound")), &control.ListGatewayReconcileIDsRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("bound subject scanned recovery without a cluster ID: %v", err)
	}
	if _, err := identityClient.ListGatewayReconcileIDs(call(token(t, key, "bound")), &control.ListGatewayReconcileIDsRequest{ClusterId: foreignCluster}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("bound subject scanned a foreign cluster: %v", err)
	}
	scoped, err := identityClient.ListGatewayReconcileIDs(call(token(t, key, "bound")), &control.ListGatewayReconcileIDsRequest{ClusterId: f.cluster})
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped.GetIds()) != 2 || !containsID(scoped.GetIds(), homeID) {
		t.Fatalf("scoped recovery page: %v", scoped.GetIds())
	}
	fleetPage, err := identityClient.ListGatewayReconcileIDs(call(token(t, key, "fleet")), &control.ListGatewayReconcileIDsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	// The fleet page includes the two watch-stage Gateways created above.
	if len(fleetPage.GetIds()) != 4 || !containsID(fleetPage.GetIds(), foreignID) {
		t.Fatalf("fleet recovery page: %v", fleetPage.GetIds())
	}
	// A non-control-plane subject keeps the cluster ID as a visibility
	// narrowing filter over its RoleBinding-owned rows. Alice owns all four
	// Gateways she created: two in each cluster.
	owned, err := gateways.ListGateways(call(creator), &pb.ListGatewaysRequest{ClusterId: proto.String(foreignCluster)})
	if err != nil || len(owned.GetItems()) != 2 {
		t.Fatalf("user foreign cluster page: %v %v", owned.GetItems(), err)
	}
	owned, err = gateways.ListGateways(call(creator), &pb.ListGatewaysRequest{ClusterId: proto.String(f.cluster)})
	if err != nil || len(owned.GetItems()) != 2 {
		t.Fatalf("user owned cluster page: %v %v", owned.GetItems(), err)
	}
	for _, item := range owned.GetItems() {
		if item.GetClusterId() != f.cluster {
			t.Fatalf("user owned cluster page leaked a foreign row: %v", item)
		}
	}
}

func containsID(ids []string, id string) bool {
	for _, item := range ids {
		if item == id {
			return true
		}
	}
	return false
}
