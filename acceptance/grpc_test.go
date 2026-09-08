package acceptance

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/contracts"
	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"github.com/segmentio/ksuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
)

func TestGeneratedGatewayDescriptorsMatchReference(t *testing.T) {
	reference, err := contracts.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, actual := range []interface{ Path() string }{pb.File_hypershell_v1_common_proto, pb.File_hypershell_v1_gateways_proto} {
		expected := protodesc.ToFileDescriptorProto(reference.Proto.FindFileByPath(actual.Path()))
		descriptor := pb.File_hypershell_v1_gateways_proto
		if actual.Path() == "hypershell/v1/common.proto" {
			descriptor = pb.File_hypershell_v1_common_proto
		}
		got := protodesc.ToFileDescriptorProto(descriptor)
		// The application module owns Go import paths. Wire contracts stay fixed.
		got.Options.GoPackage = nil
		expected.Options.GoPackage = nil
		got.SourceCodeInfo = nil
		expected.SourceCodeInfo = nil
		if !proto.Equal(got, expected) {
			t.Fatalf("generated wire descriptor differs: %s", actual.Path())
		}
	}
}

func grpcClient(t *testing.T, address string, identity testIdentity) (pb.GatewayServiceClient, *grpc.ClientConn) {
	t.Helper()
	ca, err := os.ReadFile(identity.config.CAFile)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("invalid test CA")
	}
	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS13})))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connection.Close() })
	return pb.NewGatewayServiceClient(connection), connection
}

func TestGatewayWorkflowAcrossRESTAndGRPC(t *testing.T) {
	f := database(t)
	_, config := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, config)
	key, settings := issuer(t)
	tlsIdentity := identity(t, "localhost")
	directory := filepath.Dir(tlsIdentity.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"))
	binary := buildApplication(t)
	stop, httpAddress, grpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	client, connection := grpcClient(t, grpcAddress, tlsIdentity)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	creator := token(t, key, "alice", "gateway:creator")
	owner := token(t, key, "alice")
	bob := token(t, key, "bob", "gateway:creator")
	call := func(token string) context.Context {
		return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+token))
	}
	request := &pb.CreateGatewayRequest{Name: "grpc-created", ClusterId: f.cluster, ReleaseId: f.release, SupervisorImage: proto.String("supervisor:v1"), CredentialDriver: proto.String("{\"type\":\"test\"}"), ServerDnsNames: []string{"gw.example.test"}}
	created, err := client.CreateGateway(call(creator), request)
	if err != nil {
		t.Fatal(err)
	}
	gateway := created.Gateway
	id := gateway.GetMetadata().GetId()
	if _, err := ksuid.Parse(id); err != nil {
		t.Fatal(err)
	}
	if gateway.Namespace == "" || gateway.DatabaseId != f.database || gateway.Metadata.Kind != "Gateway" || gateway.Metadata.Href != "/api/hypershell/v1/gateways/"+id || gateway.Metadata.CreatedAt.CheckValid() != nil || gateway.SupervisorImage == nil || gateway.CredentialDriver == nil || len(gateway.ServerDnsNames) != 1 {
		t.Fatalf("wrong gRPC response: %v", gateway)
	}
	if readEvent(t, consumer, id) == "" {
		t.Fatal("gRPC event ID was lost")
	}
	awaitQueueEmpty(t, f)
	path := httpAddress + "/api/hypershell/v1/gateways"
	code, data := requestJSON(t, "GET", path+"/"+id, owner, nil)
	var rest httpapi.Gateway
	if json.Unmarshal(data, &rest) != nil || code != 200 || rest.ID != id || rest.Namespace != gateway.Namespace || rest.CreatedBy != "alice" || !rest.CreatedAt.Equal(gateway.Metadata.CreatedAt.AsTime()) || rest.SupervisorImage == nil || *rest.SupervisorImage != gateway.GetSupervisorImage() {
		t.Fatalf("REST did not retrieve gRPC creation: %d %s", code, data)
	}
	body := []byte(fmt.Sprintf(`{"name":"rest-created","cluster_id":%q,"release_id":%q,"database_id":"ignored"}`, f.cluster, f.release))
	code, data = requestJSON(t, "POST", path, creator, body)
	if code != 201 || json.Unmarshal(data, &rest) != nil {
		t.Fatalf("REST create: %d %s", code, data)
	}
	got, err := client.GetGateway(call(owner), &pb.GetGatewayRequest{Id: rest.ID})
	if err != nil || got.Gateway.Name != "rest-created" {
		t.Fatalf("gRPC did not retrieve REST creation: %v %v", got, err)
	}
	readEvent(t, consumer, rest.ID)
	awaitQueueEmpty(t, f)
	hidden, err := client.CreateGateway(call(bob), request)
	if err != nil {
		t.Fatal(err)
	}
	readEvent(t, consumer, hidden.Gateway.Metadata.Id)
	awaitQueueEmpty(t, f)
	for _, req := range []*pb.ListGatewaysRequest{{}, {Page: 1, Size: 1}, {Page: 2, Size: 1}, {Page: 1, Size: 500}, {Page: -1, Size: 501}} {
		list, err := client.ListGateways(call(owner), req)
		if err != nil {
			t.Fatal(err)
		}
		wantSize := req.Size
		if wantSize < 1 || wantSize > 500 {
			wantSize = 20
		}
		wantPage := req.Page
		if wantPage < 1 {
			wantPage = 1
		}
		if list.Metadata.Total != 2 || list.Metadata.Size != wantSize || list.Metadata.Page != wantPage {
			t.Fatalf("filtered metadata: %v", list)
		}
		for _, item := range list.Items {
			if item.Metadata.Id == hidden.Gateway.Metadata.Id {
				t.Fatal("list exposed hidden Gateway")
			}
		}
	}
	for _, token := range []string{bob, token(t, key, "mallory", "gateway:owner")} {
		_, hiddenError := client.GetGateway(call(token), &pb.GetGatewayRequest{Id: id})
		_, missingError := client.GetGateway(call(token), &pb.GetGatewayRequest{Id: ksuid.New().String()})
		if status.Code(hiddenError) != codes.NotFound || status.Convert(hiddenError).Message() != status.Convert(missingError).Message() {
			t.Fatalf("denied read exposes existence: %v %v", hiddenError, missingError)
		}
	}
	for _, token := range []string{owner, token(t, key, "admin", "platform:admin")} {
		if _, err := client.CreateGateway(call(token), request); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("creation allowed: %v", err)
		}
	}
	viewer := token(t, key, "mallory")
	if _, err := f.db.Exec(`INSERT INTO role_bindings (id,user_id,role_id,gateway_id,scope) SELECT $1,u.id,r.id,$2,'gateway' FROM users u,roles r WHERE u.username='mallory' AND r.name='gateway:viewer'`, ksuid.New().String(), id); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetGateway(call(viewer), &pb.GetGatewayRequest{Id: id}); err != nil {
		t.Fatalf("viewer read: %v", err)
	}
	if list, err := client.ListGateways(call(viewer), &pb.ListGatewaysRequest{}); err != nil || list.Metadata.Total != 1 {
		t.Fatalf("viewer list: %v %v", list, err)
	}
	if _, err := f.db.Exec(`UPDATE role_bindings SET deleted_at=now() WHERE user_id=(SELECT id FROM users WHERE username='mallory')`); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetGateway(call(viewer), &pb.GetGatewayRequest{Id: id}); status.Code(err) != codes.NotFound {
		t.Fatalf("removed viewer grant: %v", err)
	}
	if list, err := client.ListGateways(call(token(t, key, "admin", "platform:admin")), &pb.ListGatewaysRequest{}); err != nil || list.Metadata.Total != 3 {
		t.Fatalf("admin list: %v %v", list, err)
	}
	if _, err := client.GetGateway(ctx, &pb.GetGatewayRequest{Id: id}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("missing bearer: %v", err)
	}
	if _, err := client.GetGateway(call("forged"), &pb.GetGatewayRequest{Id: id}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("forged bearer: %v", err)
	}
	duplicate := metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+owner, "authorization", "Bearer "+owner))
	if _, err := client.GetGateway(duplicate, &pb.GetGatewayRequest{Id: id}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("duplicate bearer: %v", err)
	}
	if _, err := client.GetGateway(call(owner), &pb.GetGatewayRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("missing ID: %v", err)
	}
	invalid := proto.Clone(request).(*pb.CreateGatewayRequest)
	invalid.Name = "bad\x00name"
	if _, err := client.CreateGateway(call(creator), invalid); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid request: %v", err)
	}
	invalid.Name = strings.Repeat("x", 65537)
	if _, err := client.CreateGateway(call(creator), invalid); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("oversized request: %v", err)
	}
	beforeGateway, beforeGrant := count(t, f.db, "gateways"), count(t, f.db, "role_bindings")
	for _, table := range []string{"role_bindings", "stego_outbox.messages"} {
		if _, err := f.db.Exec("ALTER TABLE " + table + " ADD CONSTRAINT acceptance_reject CHECK (false) NOT VALID"); err != nil {
			t.Fatal(err)
		}
		_, err := client.CreateGateway(call(creator), request)
		if status.Code(err) != codes.Internal || strings.Contains(err.Error(), "acceptance_reject") {
			t.Fatalf("write failure exposed: %v", err)
		}
		if count(t, f.db, "gateways") != beforeGateway || count(t, f.db, "role_bindings") != beforeGrant {
			t.Fatal("failed gRPC create left a resource or grant")
		}
		if _, err := f.db.Exec("ALTER TABLE " + table + " DROP CONSTRAINT acceptance_reject"); err != nil {
			t.Fatal(err)
		}
	}
	connection.Close()
	stop()
	stop, _, grpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	client, _ = grpcClient(t, grpcAddress, tlsIdentity)
	got, err = client.GetGateway(call(owner), &pb.GetGatewayRequest{Id: id})
	if err != nil || !proto.Equal(got.Gateway, gateway) {
		t.Fatalf("restart changed Gateway or access: %v %v", got, err)
	}
	stop()
}
