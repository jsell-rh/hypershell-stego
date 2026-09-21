package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestGRPCGrantMappingRejectsStoredFaultAcrossRestart(t *testing.T) {
	f := database(t)
	_, config := broker(t, identity(t, "localhost"))
	key, settings := issuer(t)
	tlsIdentity := identity(t, "localhost")
	directory := filepath.Dir(tlsIdentity.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"))
	binary := buildApplication(t)
	stop, address, grpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	defer func() { stop() }()
	owner := token(t, key, "mapping-rpc-owner", "gateway:creator")
	outsider := token(t, key, "mapping-rpc-outsider")
	var gatewayIDs, grantIDs [2]string
	var userID string
	for i, name := range []string{"grant-valid", "grant-invalid"} {
		data, err := json.Marshal(f.request(name))
		if err != nil {
			t.Fatal(err)
		}
		code, body := requestJSON(t, "POST", address+"/api/hypershell/v1/gateways", owner, data)
		var row gatewayResponse
		if code != 201 || json.Unmarshal(body, &row) != nil || row.ID == "" {
			t.Fatal("cannot create grant mapping fixture", code)
		}
		gatewayIDs[i] = row.ID
		if err := f.db.QueryRow("SELECT id,user_id FROM role_bindings WHERE gateway_id=$1 AND deleted_at IS NULL", row.ID).Scan(&grantIDs[i], &userID); err != nil {
			t.Fatal(err)
		}
	}
	_, initialConnection := grpcClient(t, grpcAddress, tlsIdentity)
	initialClient := pb.NewRoleBindingServiceClient(initialConnection)
	initialContext, initialCancel := context.WithTimeout(context.Background(), 10*time.Second)
	initial, err := initialClient.ListRoleBindings(metadata.NewOutgoingContext(initialContext, metadata.Pairs("authorization", "Bearer "+owner)), &pb.ListRoleBindingsRequest{UserId: &userID})
	initialCancel()
	if err != nil || initial == nil || len(initial.Items) != 3 {
		t.Fatal("missing owner grants and projected creator grant", err)
	}
	valid := make(map[string]*pb.RoleBinding)
	for _, grant := range initial.Items {
		id := grant.GetMetadata().GetId()
		if id == "" || grant.GetUserId() != userID {
			t.Fatal("invalid grant fixture")
		}
		if id != grantIDs[1] {
			valid[id] = grant
		}
	}
	if len(valid) != 2 || valid[grantIDs[0]] == nil {
		t.Fatal("missing valid Gateway and global grants")
	}
	// Only the private fixture database receives this invalid timestamp.
	result, err := f.db.Exec("UPDATE role_bindings SET updated_time=make_timestamptz(10000,1,1,0,0,0,'UTC') WHERE id=$1", grantIDs[1])
	if err != nil {
		t.Fatal(err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		t.Fatal("fault did not select one grant", err)
	}
	privateError := func(t *testing.T, err error) {
		t.Helper()
		if status.Code(err) != codes.Internal || status.Convert(err).Message() != "request failed" || len(status.Convert(err).Details()) != 0 {
			t.Fatal("conversion changed the private gRPC error", err)
		}
	}
	for phase := 0; phase < 2; phase++ {
		if phase == 1 {
			stop()
			stop, address, grpcAddress = startBoth(t, binary, f.dsn, config, settings...)
		}
		t.Run(fmt.Sprintf("restart-%d", phase), func(t *testing.T) {
			_, connection := grpcClient(t, grpcAddress, tlsIdentity)
			client := pb.NewRoleBindingServiceClient(connection)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			call := func(bearer string) context.Context {
				return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+bearer))
			}
			request := &pb.ListRoleBindingsRequest{UserId: &userID}
			if got, err := client.ListRoleBindings(ctx, request); got != nil || status.Code(err) != codes.Unauthenticated {
				t.Fatal("missing identity reached conversion", err)
			}
			if got, err := client.ListRoleBindings(call(outsider), request); err != nil || got == nil || len(got.Items) != 0 {
				t.Fatal("conversion ran before access filtering", err)
			}
			selected, err := client.ListRoleBindings(call(owner), &pb.ListRoleBindingsRequest{UserId: &userID, GatewayId: &gatewayIDs[0]})
			if err != nil || selected == nil || len(selected.Items) != 1 || selected.Items[0].GetMetadata().GetId() != grantIDs[0] || selected.Items[0].RoleName != "gateway:owner" || selected.Items[0].Username != "mapping-rpc-owner" {
				t.Fatal("valid grant selection changed", err)
			}
			for _, gateway := range []*string{nil, &gatewayIDs[1]} {
				got, err := client.ListRoleBindings(call(owner), &pb.ListRoleBindingsRequest{UserId: &userID, GatewayId: gateway})
				if got != nil {
					t.Fatal("invalid stored grant returned a partial list")
				}
				privateError(t, err)
			}
			stream, err := client.WatchRoleBindings(call(owner), &pb.WatchRoleBindingsRequest{})
			if err != nil {
				t.Fatal(err)
			}
			ended := false
			seen := make(map[string]bool)
			for received := 0; received <= len(valid); received++ {
				message, err := stream.Recv()
				if err != nil {
					if message != nil {
						t.Fatal("invalid watch returned a partial message")
					}
					privateError(t, err)
					ended = true
					break
				}
				id := message.GetResourceId()
				if seen[id] || valid[id] == nil || message.GetType() != pb.EventType_EVENT_TYPE_UPDATED || !proto.Equal(message.GetRoleBinding(), valid[id]) {
					t.Fatal("watch returned an invalid or repeated grant")
				}
				seen[id] = true
			}
			if !ended {
				t.Fatal("invalid grant did not end the watch")
			}
		})
	}
	if _, err := f.db.Exec("UPDATE role_bindings SET updated_time=created_time WHERE id=$1", grantIDs[1]); err != nil {
		t.Fatal(err)
	}
	_, connection := grpcClient(t, grpcAddress, tlsIdentity)
	client := pb.NewRoleBindingServiceClient(connection)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+owner))
	got, err := client.ListRoleBindings(ctx, &pb.ListRoleBindingsRequest{UserId: &userID})
	if err != nil || got == nil || len(got.Items) != len(initial.Items) {
		t.Fatal("grant list did not recover after repair", err)
	}
	recovered := make(map[string]bool)
	for _, grant := range got.Items {
		id := grant.GetMetadata().GetId()
		if recovered[id] || (id != grantIDs[1] && valid[id] == nil) {
			t.Fatal("repaired list changed grant identities")
		}
		recovered[id] = true
	}
}
