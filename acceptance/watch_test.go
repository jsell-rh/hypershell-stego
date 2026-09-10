package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"github.com/segmentio/ksuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type watchResult struct {
	event *pb.WatchGatewaysResponse
	err   error
}
type gatewayWatch struct {
	results chan watchResult
	cancel  context.CancelFunc
}

func watchGateways(t testing.TB, client pb.GatewayServiceClient, ctx context.Context) *gatewayWatch {
	t.Helper()
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	stream, err := client.WatchGateways(ctx, &pb.WatchGatewaysRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Header(); err != nil {
		t.Fatal(err)
	}
	watch := &gatewayWatch{results: make(chan watchResult, 16), cancel: cancel}
	go func() {
		for {
			event, err := stream.Recv()
			select {
			case watch.results <- watchResult{event, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	return watch
}
func (w *gatewayWatch) next(t testing.TB) watchResult {
	t.Helper()
	select {
	case result := <-w.results:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("watch produced no result")
		return watchResult{}
	}
}
func (w *gatewayWatch) expect(t testing.TB, kind pb.EventType, id, name string) *pb.Gateway {
	t.Helper()
	result := w.next(t)
	if result.err != nil || result.event.GetType() != kind || result.event.GetResourceId() != id || result.event.GetGateway().GetName() != name {
		t.Fatalf("watch result: %v %v, want %v %s %s", result.event, result.err, kind, id, name)
	}
	row := result.event.Gateway
	if row.GetMetadata().GetId() != id || row.Metadata.Kind != "Gateway" || row.Metadata.Href != "/api/hypershell/v1/gateways/"+id || row.Metadata.CreatedAt.CheckValid() != nil || row.Metadata.UpdatedAt.CheckValid() != nil || row.Namespace == "" {
		t.Fatalf("watch shape: %v", row)
	}
	return row
}

func TestGatewayWatchThroughGeneratedRuntime(t *testing.T) {
	f := database(t)
	// This resource gives the viewer a visible barrier after access is revoked.
	anchor, err := f.service.Create(context.Background(), principal("mallory", "gateway:creator"), f.request("viewer-anchor"))
	if err != nil {
		t.Fatal(err)
	}
	_, config := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, config)
	key, settings := issuer(t)
	tlsIdentity := identity(t, "localhost")
	directory := filepath.Dir(tlsIdentity.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"), `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["controller"]`)
	binary := buildApplication(t)
	stop, httpAddress, grpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	readEvent(t, consumer, anchor.ID)
	awaitQueueEmpty(t, f)
	client, connection := grpcClient(t, grpcAddress, tlsIdentity)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	call := func(bearer string) context.Context {
		return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+bearer))
	}
	owner := token(t, key, "alice")
	ownerCtx := call(owner)
	viewerCtx := call(token(t, key, "mallory"))
	adminCtx := call(token(t, key, "admin", "platform:admin"))
	controllerCtx := call(token(t, key, "controller"))
	a := watchGateways(t, client, ownerCtx)
	a2 := watchGateways(t, client, ownerCtx)
	viewer := watchGateways(t, client, viewerCtx)
	admin := watchGateways(t, client, adminCtx)
	controller := watchGateways(t, client, controllerCtx)
	// Header completion establishes the subscription before the initial list.
	list, err := client.ListGateways(ownerCtx, &pb.ListGatewaysRequest{})
	if err != nil || list.Metadata.Total != 0 {
		t.Fatalf("initial list: %v %v", list, err)
	}
	path := httpAddress + "/api/hypershell/v1/gateways"
	body := []byte(fmt.Sprintf(`{"name":"watch-create","cluster_id":%q,"release_id":%q,"database_id":"ignored"}`, f.cluster, f.release))
	code, data := requestJSON(t, "POST", path, token(t, key, "alice", "gateway:creator"), body)
	var rest httpapi.Gateway
	if code != 201 || json.Unmarshal(data, &rest) != nil {
		t.Fatalf("watch REST create: %d %s", code, data)
	}
	id := rest.ID
	expect := func(watches []*gatewayWatch, kind pb.EventType, id, name string) {
		for _, watch := range watches {
			watch.expect(t, kind, id, name)
		}
	}
	owners := []*gatewayWatch{a, a2, admin, controller}
	all := []*gatewayWatch{a, a2, viewer, admin, controller}
	expect(owners, pb.EventType_EVENT_TYPE_CREATED, id, "watch-create")
	got, err := client.GetGateway(ownerCtx, &pb.GetGatewayRequest{Id: id})
	if err != nil || got.Gateway.DatabaseId != f.database {
		t.Fatalf("watch creation lacks committed grant or placement: %v %v", got, err)
	}
	// A database notice is not authority to return a deleted resource.
	notice, _ := json.Marshal(map[string]string{"id": uuid.NewString(), "destination": "kafka", "resource_key": id, "kind": "gateway.deleted"})
	if _, err := f.db.Exec(`SELECT pg_notify('stego_resource_events_v1',$1)`, string(notice)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO role_bindings(id,user_id,role_id,gateway_id,scope) SELECT $1,u.id,r.id,$2,'gateway' FROM users u,roles r WHERE u.username='mallory' AND r.name='gateway:viewer'`, ksuid.New().String(), id); err != nil {
		t.Fatal(err)
	}
	updated, err := client.UpdateGateway(ownerCtx, &pb.UpdateGatewayRequest{Id: id, Name: proto.String("watch-update")})
	if err != nil {
		t.Fatal(err)
	}
	for _, watch := range all {
		row := watch.expect(t, pb.EventType_EVENT_TYPE_UPDATED, id, "watch-update")
		if !proto.Equal(row, updated.Gateway) {
			t.Fatalf("watch differs from update response: %v %v", row, updated.Gateway)
		}
	}
	// Hidden events must not precede the next visible event on either owner stream.
	hidden, err := client.CreateGateway(call(token(t, key, "bob", "gateway:creator")), &pb.CreateGatewayRequest{Name: "hidden", ClusterId: f.cluster, ReleaseId: f.release})
	if err != nil {
		t.Fatal(err)
	}
	expect([]*gatewayWatch{admin, controller}, pb.EventType_EVENT_TYPE_CREATED, hidden.Gateway.Metadata.Id, "hidden")
	code, data = requestJSON(t, "PATCH", path+"/"+id, owner, []byte(`{"name":"rest-update"}`))
	if code != 200 {
		t.Fatalf("watch patch: %d %s", code, data)
	}
	expect(all, pb.EventType_EVENT_TYPE_UPDATED, id, "rest-update")
	// Revocation applies to the next event, without reconnecting the viewer.
	if _, err := f.db.Exec(`UPDATE role_bindings SET deleted_at=now() WHERE user_id=(SELECT id FROM users WHERE username='mallory') AND gateway_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	// Writes from this independent process and SQL pool must also reach the stream.
	if _, err := f.service.Update(ctx, principal("alice"), id, gateways.PatchRequest{Name: proto.String("external-writer")}); err != nil {
		t.Fatal(err)
	}
	expect(owners, pb.EventType_EVENT_TYPE_UPDATED, id, "external-writer")
	if _, err := client.UpdateGateway(viewerCtx, &pb.UpdateGatewayRequest{Id: anchor.ID, Name: proto.String("viewer-barrier")}); err != nil {
		t.Fatal(err)
	}
	expect([]*gatewayWatch{viewer, admin, controller}, pb.EventType_EVENT_TYPE_UPDATED, anchor.ID, "viewer-barrier")
	// A failed outbox insert must produce neither a state change nor a watch event.
	if _, err := f.db.Exec(`ALTER TABLE stego_outbox.messages ADD CONSTRAINT reject_watch CHECK(false) NOT VALID`); err != nil {
		t.Fatal(err)
	}
	if _, err := client.UpdateGateway(ownerCtx, &pb.UpdateGatewayRequest{Id: id, Name: proto.String("rolled-back")}); status.Code(err) != codes.Internal {
		t.Fatalf("rollback: %v", err)
	}
	if _, err := f.db.Exec(`ALTER TABLE stego_outbox.messages DROP CONSTRAINT reject_watch`); err != nil {
		t.Fatal(err)
	}
	counted, err := client.AdjustActiveSandboxCount(controllerCtx, &pb.AdjustActiveSandboxCountRequest{Namespace: rest.Namespace, Delta: 3})
	if err != nil || counted.ActiveSandboxCount != 3 {
		t.Fatalf("watch count: %v %v", counted, err)
	}
	for _, watch := range owners {
		row := watch.expect(t, pb.EventType_EVENT_TYPE_UPDATED, id, "external-writer")
		if row.GetActiveSandboxCount() != 3 {
			t.Fatalf("watch count missing: %v", row)
		}
	}
	code, data = requestJSON(t, "DELETE", path+"/"+id, owner, nil)
	if code != 204 {
		t.Fatalf("watch delete: %d %s", code, data)
	}
	expect(owners, pb.EventType_EVENT_TYPE_DELETED, id, "external-writer")
	if _, err := client.GetGateway(ownerCtx, &pb.GetGatewayRequest{Id: id}); status.Code(err) != codes.NotFound {
		t.Fatalf("deleted Gateway readable: %v", err)
	}
	if _, err := client.UpdateGateway(viewerCtx, &pb.UpdateGatewayRequest{Id: anchor.ID, Name: proto.String("after-delete")}); err != nil {
		t.Fatal(err)
	}
	expect([]*gatewayWatch{viewer, admin, controller}, pb.EventType_EVENT_TYPE_UPDATED, anchor.ID, "after-delete")
	// Kafka remains the durable event path while watch streams receive live state.
	readEvent(t, consumer, id)
	for range 4 {
		readGatewayEvent(t, consumer, id, "Update", "gateway.updated")
	}
	readGatewayEvent(t, consumer, id, "Delete", "gateway.deleted")
	awaitQueueEmpty(t, f)
	stop()
	for _, watch := range all {
		if result := watch.next(t); result.err == nil {
			t.Fatalf("watch survived runtime stop or emitted extra data: %v", result.event)
		}
	}
	connection.Close()
	offline, err := f.service.Create(ctx, principal("alice", "gateway:creator"), f.request("offline"))
	if err != nil {
		t.Fatal(err)
	}
	stop, _, grpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	client, _ = grpcClient(t, grpcAddress, tlsIdentity)
	recovered := watchGateways(t, client, ownerCtx)
	list, err = client.ListGateways(ownerCtx, &pb.ListGatewaysRequest{})
	if err != nil || list.Metadata.Total != 1 || len(list.Items) != 1 || list.Items[0].Metadata.Id != offline.ID {
		t.Fatalf("recovery list: %v %v", list, err)
	}
	if _, err := client.UpdateGateway(ownerCtx, &pb.UpdateGatewayRequest{Id: offline.ID, Name: proto.String("after-restart")}); err != nil {
		t.Fatal(err)
	}
	recovered.expect(t, pb.EventType_EVENT_TYPE_UPDATED, offline.ID, "after-restart")
	readEvent(t, consumer, offline.ID)
	readGatewayEvent(t, consumer, offline.ID, "Update", "gateway.updated")
	awaitQueueEmpty(t, f)
	if _, err := client.DeleteGateway(ownerCtx, &pb.DeleteGatewayRequest{Id: offline.ID}); err != nil {
		t.Fatal(err)
	}
	recovered.expect(t, pb.EventType_EVENT_TYPE_DELETED, offline.ID, "after-restart")
	readGatewayEvent(t, consumer, offline.ID, "Delete", "gateway.deleted")
	awaitQueueEmpty(t, f)
	recovered.cancel()
	stop()
}

func TestGatewayWatchExpiresThroughGeneratedRuntime(t *testing.T) {
	f := database(t)
	_, config := broker(t, identity(t, "localhost"))
	key, settings := issuer(t)
	tlsIdentity := identity(t, "localhost")
	directory := filepath.Dir(tlsIdentity.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"), "STEGO_GRPC_STREAM_TIMEOUT=3s")
	stop, _, address := startBoth(t, buildApplication(t), f.dsn, config, settings...)
	defer stop()
	client, _ := grpcClient(t, address, tlsIdentity)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, raw := range []string{"", "forged"} {
		call := metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+raw))
		stream, err := client.WatchGateways(call, &pb.WatchGatewaysRequest{})
		if err == nil {
			_, err = stream.Recv()
		}
		if status.Code(err) != codes.Unauthenticated {
			t.Fatalf("watch authentication: %v", err)
		}
	}
	expiry := time.Now().Add(2 * time.Second).Unix()
	raw, err := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": "https://issuer.example", "aud": "hypershell", "sub": "alice", "preferred_username": "alice", "email": "alice@example.test", "given_name": "alice", "iat": time.Now().Add(-time.Minute).Unix(), "exp": expiry, "realm_access": map[string]any{"roles": []string{}}}).SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	expired := watchGateways(t, client, metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+raw)))
	result := expired.next(t)
	if status.Code(result.err) != codes.Unauthenticated {
		t.Fatalf("token expiry: %v %v", result.event, result.err)
	}
	bounded := watchGateways(t, client, metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+token(t, key, "alice"))))
	result = bounded.next(t)
	if status.Code(result.err) != codes.DeadlineExceeded {
		t.Fatalf("stream lifetime: %v %v", result.event, result.err)
	}
}

func TestGatewayWatchSourceFailureStopsGeneratedRuntime(t *testing.T) {
	f := database(t)
	row, err := f.service.Create(context.Background(), principal("alice", "gateway:creator"), f.request("before-failure"))
	if err != nil {
		t.Fatal(err)
	}
	_, config := broker(t, identity(t, "localhost"))
	key, settings := issuer(t)
	tlsIdentity := identity(t, "localhost")
	directory := filepath.Dir(tlsIdentity.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"))
	binary := buildApplication(t)
	_, httpAddress, address, waitFailure := startBothManaged(t, binary, f.dsn, config, settings...)
	client, _ := grpcClient(t, address, tlsIdentity)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	call := metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+token(t, key, "alice")))
	watch := watchGateways(t, client, call)
	// Select the target first. Do not place a termination function in a WHERE
	// clause: SQL does not guarantee the order in which predicates run.
	var pid int
	if err := f.db.QueryRowContext(ctx, `SELECT pid FROM pg_stat_activity WHERE datname=current_database() AND query='LISTEN stego_resource_events_v1'`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	var terminated bool
	if err := f.db.QueryRowContext(ctx, `SELECT pg_terminate_backend($1)`, pid).Scan(&terminated); err != nil || !terminated {
		t.Fatalf("listener termination: %t %v", terminated, err)
	}
	if result := watch.next(t); result.err == nil {
		t.Fatalf("source failure left watch open: %v", result.event)
	}
	output := waitFailure()
	if !strings.Contains(output, `"event.name":"service.failed"`) || !strings.Contains(output, `"stage":"service.run"`) || !strings.Contains(output, "grpc-application[0]") {
		t.Fatalf("source failure not reported by supervisor: %s", output)
	}
	for _, address := range []string{address, strings.TrimPrefix(httpAddress, "http://")} {
		connection, err := net.DialTimeout("tcp", address, time.Second)
		if err == nil {
			connection.Close()
			t.Fatal("failed runtime kept a listener open")
		}
	}
	if _, err := f.service.Update(ctx, principal("alice"), row.ID, gateways.PatchRequest{Name: proto.String("offline-change")}); err != nil {
		t.Fatal(err)
	}
	stop, _, address := startBoth(t, binary, f.dsn, config, settings...)
	defer stop()
	client, _ = grpcClient(t, address, tlsIdentity)
	recovered := watchGateways(t, client, call)
	list, err := client.ListGateways(call, &pb.ListGatewaysRequest{})
	if err != nil || list.Metadata.Total != 1 || list.Items[0].Name != "offline-change" {
		t.Fatalf("source failure recovery: %v %v", list, err)
	}
	if _, err := client.UpdateGateway(call, &pb.UpdateGatewayRequest{Id: row.ID, Name: proto.String("live-again")}); err != nil {
		t.Fatal(err)
	}
	recovered.expect(t, pb.EventType_EVENT_TYPE_UPDATED, row.ID, "live-again")
	recovered.cancel()
}
