package grpcapi

import (
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func grantMappingView() gateways.GrantView {
	gateway := "gateway-reference"
	created := time.Date(2026, 9, 21, 1, 2, 3, 456789, time.FixedZone("offset", 3600))
	return gateways.GrantView{
		Grant:    model.RoleBinding{Meta: model.Meta{ID: "grant-reference", CreatedTime: created, UpdatedTime: created.Add(time.Second)}, RoleID: "role-reference", UserID: "user-reference", GatewayID: &gateway, Scope: "gateway"},
		RoleName: "gateway:viewer", Username: "Zoë",
	}
}

func TestGRPCGrantMappingPreservesContract(t *testing.T) {
	for _, presence := range []string{"absent", "empty", "set"} {
		t.Run(presence, func(t *testing.T) {
			view := grantMappingView()
			if presence == "absent" {
				view.Grant.GatewayID = nil
				view.Grant.Scope = "global"
				view.RoleName = "platform:admin"
			} else if presence == "empty" {
				view.Grant.GatewayID = proto.String("")
				view.Grant.UserID = ""
				view.RoleName, view.Username = "", ""
			}
			row := view.Grant
			want := &pb.RoleBinding{Metadata: &pb.ObjectReference{Id: row.ID, Kind: "RoleBinding", Href: "/api/hypershell/v1/role_bindings/" + row.ID, CreatedAt: timestamppb.New(row.CreatedTime), UpdatedAt: timestamppb.New(row.UpdatedTime)}, RoleId: row.RoleID, UserId: proto.String(row.UserID), GatewayId: row.GatewayID, Scope: row.Scope, RoleName: view.RoleName, Username: view.Username}
			got, err := presentGrant(view)
			if err != nil || !proto.Equal(got, want) {
				t.Fatal("grant mapping changed fields or optional presence", got, err)
			}
			wire, err := proto.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			decoded := new(pb.RoleBinding)
			if err := proto.Unmarshal(wire, decoded); err != nil || !proto.Equal(decoded, want) {
				t.Fatal("grant wire response changed", err)
			}
		})
	}
}

func TestGRPCGrantMappingOwnsValues(t *testing.T) {
	view := grantMappingView()
	got, err := presentGrant(view)
	if err != nil || got == nil || got.GatewayId == nil || got.UserId == nil || got.Metadata == nil || got.Metadata.CreatedAt == nil {
		t.Fatal("missing grant fields", err)
	}
	if got.GatewayId == view.Grant.GatewayID {
		t.Fatal("response shares the stored Gateway pointer")
	}
	*got.GatewayId, *got.UserId = "response-gateway", "response-user"
	got.Metadata.CreatedAt.Seconds++
	if *view.Grant.GatewayID != "gateway-reference" || view.Grant.UserID != "user-reference" || !view.Grant.CreatedTime.Equal(grantMappingView().Grant.CreatedTime) {
		t.Fatal("response changed the domain view")
	}
	got, err = presentGrant(view)
	if err != nil {
		t.Fatal(err)
	}
	*view.Grant.GatewayID = "stored-change"
	view.RoleName, view.Username = "role-change", "user-change"
	if got.GetGatewayId() != "gateway-reference" || got.RoleName != "gateway:viewer" || got.Username != "Zoë" {
		t.Fatal("domain view changed the response")
	}
}

func TestGRPCGrantMappingRejectsInvalidValues(t *testing.T) {
	bad := string([]byte{0xff})
	for name, edit := range map[string]func(*gateways.GrantView){
		"id":        func(v *gateways.GrantView) { v.Grant.ID = bad },
		"role id":   func(v *gateways.GrantView) { v.Grant.RoleID = bad },
		"user id":   func(v *gateways.GrantView) { v.Grant.UserID = bad },
		"gateway":   func(v *gateways.GrantView) { v.Grant.GatewayID = &bad },
		"scope":     func(v *gateways.GrantView) { v.Grant.Scope = bad },
		"role name": func(v *gateways.GrantView) { v.RoleName = bad },
		"username":  func(v *gateways.GrantView) { v.Username = bad },
		"created":   func(v *gateways.GrantView) { v.Grant.CreatedTime = time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC) },
		"updated":   func(v *gateways.GrantView) { v.Grant.UpdatedTime = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) },
	} {
		t.Run(name, func(t *testing.T) {
			view := grantMappingView()
			edit(&view)
			got, err := presentGrant(view)
			if err == nil {
				t.Fatal("invalid grant value was accepted")
			}
			requirePrivateTimestampError(t, got, mapError(err))
		})
	}
}
