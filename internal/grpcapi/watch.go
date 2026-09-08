package grpcapi

import (
	"errors"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	events "github.com/jsell-rh/hypershell-stego/out/contracts/events"
	storage "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *server) WatchGateways(_ *pb.WatchGatewaysRequest, stream grpc.ServerStreamingServer[pb.WatchGatewaysResponse]) error {
	ctx := stream.Context()
	principal := gateways.PrincipalFromContext(ctx)
	if _, err := s.service.List(ctx, principal, 1, 0); err != nil {
		return mapError(err)
	}
	subscription, err := s.source.Subscribe(ctx)
	if err != nil {
		return watchError(err)
	}
	defer subscription.Close()
	// Header completion is the subscription handshake. Clients can now list
	// current state while subsequent commits accumulate in the subscription.
	if err := stream.SendHeader(nil); err != nil {
		return err
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
		deleted := false
		switch event.Kind {
		case "gateway.created":
			kind = pb.EventType_EVENT_TYPE_CREATED
		case "gateway.updated":
			kind = pb.EventType_EVENT_TYPE_UPDATED
		case "gateway.deleted":
			kind = pb.EventType_EVENT_TYPE_DELETED
			deleted = true
		default:
			continue
		}
		row, err := s.service.EventGateway(ctx, principal, event.ResourceKey, deleted)
		if errors.Is(err, storage.ErrNotFound) {
			continue
		}
		if err != nil {
			return mapError(err)
		}
		gateway, err := present(row)
		if err != nil {
			return mapError(err)
		}
		if err := stream.Send(&pb.WatchGatewaysResponse{Type: kind, ResourceId: event.ResourceKey, Gateway: gateway}); err != nil {
			return err
		}
	}
}
func watchError(err error) error {
	switch {
	case errors.Is(err, events.ErrUnavailable):
		return status.Error(codes.Unavailable, "event source is unavailable; reconnect and list again")
	case errors.Is(err, events.ErrCapacity), errors.Is(err, events.ErrSlowConsumer):
		return status.Error(codes.ResourceExhausted, "watch must reconnect and list again")
	default:
		return mapError(err)
	}
}
