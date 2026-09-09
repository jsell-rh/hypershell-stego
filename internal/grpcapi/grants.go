package grpcapi

import (
	"context"
	"errors"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	events "github.com/jsell-rh/hypershell-stego/out/contracts/events"
	storage "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type grantServer struct {
	pb.UnimplementedRoleBindingServiceServer
	service *gateways.Service
	source  events.Source
}

func presentGrant(view gateways.GrantView) (*pb.RoleBinding, error) {
	row := view.Grant
	created, updated := timestamppb.New(row.CreatedTime), timestamppb.New(row.UpdatedTime)
	if err := created.CheckValid(); err != nil {
		return nil, err
	}
	if err := updated.CheckValid(); err != nil {
		return nil, err
	}
	return &pb.RoleBinding{Metadata: &pb.ObjectReference{Id: row.ID, Kind: "RoleBinding", Href: "/api/hypershell/v1/role_bindings/" + row.ID, CreatedAt: created, UpdatedAt: updated}, RoleId: row.RoleID, UserId: &row.UserID, GatewayId: &row.GatewayID, Scope: row.Scope, RoleName: view.RoleName, Username: view.Username}, nil
}
func (s *grantServer) ListRoleBindings(ctx context.Context, req *pb.ListRoleBindingsRequest) (*pb.ListRoleBindingsResponse, error) {
	if req.GetUserId() == "" || (req.GatewayId != nil && req.GetGatewayId() == "") {
		return nil, status.Error(codes.InvalidArgument, "user_id and a nonempty optional gateway_id are required")
	}
	views, err := s.service.AllGrants(ctx, gateways.PrincipalFromContext(ctx), req.GetUserId(), req.GetGatewayId())
	if err != nil {
		return nil, mapError(err)
	}
	result := &pb.ListRoleBindingsResponse{Items: make([]*pb.RoleBinding, 0, len(views))}
	for _, view := range views {
		row, err := presentGrant(view)
		if err != nil {
			return nil, mapError(err)
		}
		result.Items = append(result.Items, row)
	}
	// Leave room below the generated server's message limit.
	if proto.Size(result) > 3<<20 {
		return nil, mapError(gateways.ErrGrantCapacity)
	}
	return result, nil
}
func (s *grantServer) WatchRoleBindings(_ *pb.WatchRoleBindingsRequest, stream grpc.ServerStreamingServer[pb.WatchRoleBindingsResponse]) error {
	ctx := stream.Context()
	principal := gateways.PrincipalFromContext(ctx)
	if _, err := s.service.ListGrants(ctx, principal, gateways.GrantQuery{Page: 1, Size: 0}); err != nil {
		return mapError(err)
	}
	subscription, err := s.source.Subscribe(ctx)
	if err != nil {
		return watchError(err)
	}
	defer subscription.Close()
	if err := stream.SendHeader(nil); err != nil {
		return err
	}
	// The reference stream replays active grants after the subscription starts.
	// Each send repeats the access check, including during a long replay.
	views, err := s.service.AllGrants(ctx, principal, "", "")
	if err != nil {
		return mapError(err)
	}
	send := func(id string, kind pb.EventType, deleted bool) error {
		view, err := s.service.EventGrant(ctx, principal, id, deleted)
		if errors.Is(err, storage.ErrNotFound) {
			return nil
		}
		if err != nil {
			return mapError(err)
		}
		row, err := presentGrant(view)
		if err != nil {
			return mapError(err)
		}
		return stream.Send(&pb.WatchRoleBindingsResponse{Type: kind, ResourceId: id, RoleBinding: row})
	}
	for _, view := range views {
		if err := send(view.Grant.ID, pb.EventType_EVENT_TYPE_UPDATED, false); err != nil {
			return err
		}
	}
	for {
		event, err := subscription.Next(ctx)
		if err != nil {
			return watchError(err)
		}
		if event.Destination != "kafka" {
			continue
		}
		var kind pb.EventType
		switch event.Kind {
		case "rolebinding.created":
			kind = pb.EventType_EVENT_TYPE_CREATED
		case "rolebinding.deleted":
			kind = pb.EventType_EVENT_TYPE_DELETED
		default:
			continue
		}
		if err := send(event.ResourceKey, kind, kind == pb.EventType_EVENT_TYPE_DELETED); err != nil {
			return err
		}
	}
}
