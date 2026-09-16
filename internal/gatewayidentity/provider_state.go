package gatewayidentity

import (
	"context"
	"errors"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	"github.com/segmentio/ksuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// NewProviderStateJournal fixes the Gateway scope and the observed revision.
// STEGO owns encryption, record checks, size bounds, and storage sequencing.
// Call contexts must carry the assigned controller or identity cleanup grant.
func NewProviderStateJournal(client control.GatewayIdentityServiceClient, protector *runtime.StateProtector, instance, id string, observed int64, cleanup bool) (*runtime.StateJournal, error) {
	parsed, err := ksuid.Parse(id)
	if client == nil || err != nil || parsed == ksuid.Nil || parsed.String() != id || observed < 1 {
		return nil, errors.New("Gateway provider journal requires an observed resource")
	}
	check := func(record *control.GatewayProviderState) (runtime.SealedStateRecord, error) {
		if record == nil || len(record.ProtoReflect().GetUnknown()) != 0 || record.GatewayId != id {
			return runtime.SealedStateRecord{}, runtime.ErrStateJournal
		}
		if record.ResourceVersion != observed || record.Deleted != cleanup {
			return runtime.SealedStateRecord{}, status.Error(codes.Aborted, "Gateway provider observation changed")
		}
		return runtime.SealedStateRecord{Version: record.Version, Data: record.SealedState}, nil
	}
	return runtime.NewStateJournal(protector, runtime.StateKey{Instance: instance, Entity: "Gateway", ResourceID: id, Scope: "identity-provider"}, runtime.StatePersistence{
		Load: func(ctx context.Context) (runtime.SealedStateRecord, error) {
			record, err := client.LoadGatewayProviderState(ctx, &control.LoadGatewayProviderStateRequest{GatewayId: id})
			if err != nil {
				return runtime.SealedStateRecord{}, err
			}
			return check(record)
		},
		Save: func(ctx context.Context, expected int64, sealed []byte) (runtime.SealedStateRecord, error) {
			ctx, err := rpc.WithResourceVersion(ctx, observed)
			if err != nil {
				return runtime.SealedStateRecord{}, err
			}
			record, err := client.SaveGatewayProviderState(ctx, &control.SaveGatewayProviderStateRequest{GatewayId: id, ExpectedVersion: expected, SealedState: sealed, Cleanup: cleanup})
			if err != nil {
				return runtime.SealedStateRecord{}, err
			}
			return check(record)
		},
	}, gateways.MaxGatewayProviderStateBytes)
}
