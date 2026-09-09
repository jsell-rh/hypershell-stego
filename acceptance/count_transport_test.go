package acceptance

import (
	"context"
	"encoding/json"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestSandboxCountWorkflowThroughGeneratedRuntime(t *testing.T) {
	f := database(t)
	domain, controller := controllerService(t, f)
	_, config := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, config)
	key, settings := issuer(t)
	tlsIdentity := identity(t, "localhost")
	directory := filepath.Dir(tlsIdentity.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"), `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["controller"]`)
	binary := buildApplication(t)
	stop, httpAddress, grpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	client, connection := grpcClient(t, grpcAddress, tlsIdentity)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	call := func(token string) context.Context {
		return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+token))
	}
	owner := token(t, key, "alice")
	control := call(token(t, key, "controller"))
	created, err := client.CreateGateway(call(token(t, key, "alice", "gateway:creator")), &pb.CreateGatewayRequest{Name: "counts", ClusterId: f.cluster, ReleaseId: f.release})
	if err != nil {
		t.Fatal(err)
	}
	id, namespace := created.Gateway.Metadata.Id, created.Gateway.Namespace
	readEvent(t, consumer, id)
	bearers := []string{owner, token(t, key, "admin", "platform:admin"), token(t, key, "creator", "gateway:creator"), token(t, key, "viewer", "gateway:viewer")}
	// Prepare caller roles before measuring sandbox-count events. Role projection
	// has its own events and commits before domain authorization.
	for _, bearer := range bearers {
		if code, _ := requestJSON(t, "GET", httpAddress+"/api/hypershell/v1/roles", bearer, nil); code != 200 {
			t.Fatal("prepare count caller", code)
		}
	}
	awaitQueueEmpty(t, f)
	eventIDs := map[string]bool{}
	updateEvent := func() {
		eventID := readGatewayEvent(t, consumer, id, "Update", "gateway.updated")
		if eventID == "" || eventIDs[eventID] {
			t.Fatal("count event ID is missing or repeated")
		}
		eventIDs[eventID] = true
	}
	readCount := func(want int32) {
		response, err := client.GetGateway(call(owner), &pb.GetGatewayRequest{Id: id})
		if err != nil || response.Gateway.ActiveSandboxCount == nil || response.Gateway.GetActiveSandboxCount() != want {
			t.Fatalf("gRPC count: %v %v", response, err)
		}
		code, data := requestJSON(t, "GET", httpAddress+"/api/hypershell/v1/gateways/"+id, owner, nil)
		var row httpapi.Gateway
		if code != 200 || json.Unmarshal(data, &row) != nil || row.ActiveSandboxCount == nil || *row.ActiveSandboxCount != want {
			t.Fatalf("REST count: %d %s", code, data)
		}
	}
	for _, bearer := range bearers {
		if _, err := client.AdjustActiveSandboxCount(call(bearer), &pb.AdjustActiveSandboxCountRequest{Namespace: namespace, Delta: 1}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("count access: %v", err)
		}
		if _, err := client.SetActiveSandboxCount(call(bearer), &pb.SetActiveSandboxCountRequest{Namespace: namespace, Count: 1}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("count set access: %v", err)
		}
	}
	if _, err := client.AdjustActiveSandboxCount(ctx, &pb.AdjustActiveSandboxCountRequest{Namespace: namespace, Delta: 1}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("unauthenticated count: %v", err)
	}
	if _, err := client.SetActiveSandboxCount(ctx, &pb.SetActiveSandboxCountRequest{Namespace: namespace, Count: 1}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("unauthenticated count set: %v", err)
	}
	for _, invalid := range []string{"", " ", "bad\x00", strings.Repeat("x", 254)} {
		if _, err := client.AdjustActiveSandboxCount(control, &pb.AdjustActiveSandboxCountRequest{Namespace: invalid, Delta: 1}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("invalid namespace: %v", err)
		}
		if _, err := client.SetActiveSandboxCount(control, &pb.SetActiveSandboxCountRequest{Namespace: invalid, Count: 1}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("invalid set namespace: %v", err)
		}
	}
	for _, missing := range []string{"missing", "' OR true --"} {
		if got, err := client.AdjustActiveSandboxCount(control, &pb.AdjustActiveSandboxCountRequest{Namespace: missing, Delta: 1}); err != nil || got.ActiveSandboxCount != 0 {
			t.Fatalf("missing namespace: %v %v", got, err)
		}
	}
	if count(t, f.db, "stego_outbox.messages") != 0 {
		t.Fatal("denied or absent count emitted an event")
	}
	// NULL becomes zero. Repeated zero writes do not change the row.
	if got, err := client.SetActiveSandboxCount(control, &pb.SetActiveSandboxCountRequest{Namespace: namespace, Count: 0}); err != nil || got.ActiveSandboxCount != 0 {
		t.Fatalf("initial zero: %v %v", got, err)
	}
	updateEvent()
	awaitQueueEmpty(t, f)
	readCount(0)
	before, err := client.GetGateway(call(owner), &pb.GetGatewayRequest{Id: id})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.SetActiveSandboxCount(control, &pb.SetActiveSandboxCountRequest{Namespace: namespace, Count: 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.AdjustActiveSandboxCount(control, &pb.AdjustActiveSandboxCountRequest{Namespace: namespace, Delta: -1}); err != nil {
		t.Fatal(err)
	}
	after, err := client.GetGateway(call(owner), &pb.GetGatewayRequest{Id: id})
	if err != nil {
		t.Fatal(err)
	}
	if !before.Gateway.Metadata.UpdatedAt.AsTime().Equal(after.Gateway.Metadata.UpdatedAt.AsTime()) || count(t, f.db, "stego_outbox.messages") != 0 {
		t.Fatal("equal count changed state")
	}
	const workers = 32
	start := make(chan struct{})
	values := make(chan int32, workers)
	failures := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Go(func() {
			<-start
			got, err := client.AdjustActiveSandboxCount(control, &pb.AdjustActiveSandboxCountRequest{Namespace: namespace, Delta: 1})
			failures <- err
			if err == nil {
				values <- got.ActiveSandboxCount
			}
		})
	}
	patchResult := make(chan error, 1)
	go func() {
		<-start
		_, err := client.UpdateGateway(call(owner), &pb.UpdateGatewayRequest{Id: id, Name: pointer("patched-with-counts")})
		patchResult <- err
	}()
	close(start)
	wait.Wait()
	close(values)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	seen := map[int32]bool{}
	for value := range values {
		if value < 1 || value > workers || seen[value] {
			t.Fatalf("lost increment: %d", value)
		}
		seen[value] = true
	}
	patchErr := <-patchResult
	if status.Code(patchErr) == codes.Aborted {
		// The caller explicitly retries the complete patch after the count writes.
		_, patchErr = client.UpdateGateway(call(owner), &pb.UpdateGatewayRequest{Id: id, Name: pointer("patched-with-counts")})
	}
	if patchErr != nil {
		t.Fatal(patchErr)
	}
	for range workers + 1 {
		updateEvent()
	}
	awaitQueueEmpty(t, f)
	readCount(workers)
	patched, err := client.GetGateway(call(owner), &pb.GetGatewayRequest{Id: id})
	if err != nil || patched.Gateway.Name != "patched-with-counts" {
		t.Fatalf("count lost patch: %v %v", patched, err)
	}
	if got, err := client.SetActiveSandboxCount(control, &pb.SetActiveSandboxCountRequest{Namespace: namespace, Count: math.MaxInt32}); err != nil || got.ActiveSandboxCount != math.MaxInt32 {
		t.Fatalf("maximum count: %v %v", got, err)
	}
	updateEvent()
	awaitQueueEmpty(t, f)
	if _, err := client.AdjustActiveSandboxCount(control, &pb.AdjustActiveSandboxCountRequest{Namespace: namespace, Delta: 1}); status.Code(err) != codes.OutOfRange {
		t.Fatalf("overflow: %v", err)
	}
	readCount(math.MaxInt32)
	if got, err := client.AdjustActiveSandboxCount(control, &pb.AdjustActiveSandboxCountRequest{Namespace: namespace, Delta: math.MinInt32}); err != nil || got.ActiveSandboxCount != 0 {
		t.Fatalf("count floor: %v %v", got, err)
	}
	updateEvent()
	awaitQueueEmpty(t, f)
	readCount(0)
	if _, err := f.db.Exec(`ALTER TABLE stego_outbox.messages ADD CONSTRAINT reject_count CHECK(false) NOT VALID`); err != nil {
		t.Fatal(err)
	}
	if _, err := client.AdjustActiveSandboxCount(control, &pb.AdjustActiveSandboxCountRequest{Namespace: namespace, Delta: 1}); status.Code(err) != codes.Internal || strings.Contains(err.Error(), "reject_count") {
		t.Fatalf("count event failure: %v", err)
	}
	if _, err := client.SetActiveSandboxCount(control, &pb.SetActiveSandboxCountRequest{Namespace: namespace, Count: 5}); status.Code(err) != codes.Internal || strings.Contains(err.Error(), "reject_count") {
		t.Fatalf("set event failure: %v", err)
	}
	readCount(0)
	if count(t, f.db, "stego_outbox.messages") != 0 {
		t.Fatal("failed event changed the queue")
	}
	if _, err := f.db.Exec(`ALTER TABLE stego_outbox.messages DROP CONSTRAINT reject_count`); err != nil {
		t.Fatal(err)
	}
	connection.Close()
	stop()
	if got, err := domain.SetActiveSandboxCount(ctx, controller, namespace, 7); err != nil || got != 7 {
		t.Fatalf("offline count: %d %v", got, err)
	}
	if count(t, f.db, "stego_outbox.messages") != 1 {
		t.Fatal("offline count event was lost")
	}
	stop, httpAddress, grpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	client, connection = grpcClient(t, grpcAddress, tlsIdentity)
	updateEvent()
	awaitQueueEmpty(t, f)
	readCount(7)
	if _, err := client.DeleteGateway(call(owner), &pb.DeleteGatewayRequest{Id: id}); err != nil {
		t.Fatal(err)
	}
	readGatewayEvent(t, consumer, id, "Delete", "gateway.deleted")
	awaitQueueEmpty(t, f)
	if got, err := client.AdjustActiveSandboxCount(control, &pb.AdjustActiveSandboxCountRequest{Namespace: namespace, Delta: 1}); err != nil || got.ActiveSandboxCount != 0 {
		t.Fatalf("deleted namespace: %v %v", got, err)
	}
	if got, err := client.SetActiveSandboxCount(control, &pb.SetActiveSandboxCountRequest{Namespace: namespace, Count: 3}); err != nil || got.ActiveSandboxCount != 0 {
		t.Fatalf("deleted namespace set: %v %v", got, err)
	}
	if count(t, f.db, "stego_outbox.messages") != 0 {
		t.Fatal("deleted Gateway emitted a count event")
	}
	connection.Close()
	stop()
}
