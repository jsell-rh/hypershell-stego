package serviceaccountprovisioner

import (
	"context"
	"errors"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	"github.com/segmentio/ksuid"
)

// NewProviderStateJournal binds the common encrypted journal to one account
// and Gateway. Its callback transaction must not acquire the caller's Gateway
// row lock. The server requires a separate exact provider-state grant.
func NewProviderStateJournal(client pb.ServiceAccountProviderStateServiceClient, protector *runtime.StateProtector, instance, gatewayID, accountID string, cleanup bool) (*runtime.StateJournal, error) {
	for _, id := range []string{gatewayID, accountID} {
		parsed, err := ksuid.Parse(id)
		if err != nil || parsed == ksuid.Nil || parsed.String() != id {
			return nil, errors.New("account provider state requires canonical resource IDs")
		}
	}
	if client == nil {
		return nil, errors.New("account provider state requires an API client")
	}
	check := func(record *pb.ServiceAccountProviderState) (runtime.SealedStateRecord, error) {
		if record == nil || len(record.ProtoReflect().GetUnknown()) != 0 || record.GatewayId != gatewayID || record.ServiceAccountId != accountID {
			return runtime.SealedStateRecord{}, runtime.ErrStateJournal
		}
		return runtime.SealedStateRecord{Version: record.Version, Data: record.SealedState}, nil
	}
	return runtime.NewStateJournal(protector, runtime.StateKey{Instance: instance, Entity: "ServiceAccount", ResourceID: accountID, Scope: gateways.AccountProviderStateScope(gatewayID)}, runtime.StatePersistence{
		Load: func(ctx context.Context) (runtime.SealedStateRecord, error) {
			record, err := client.LoadServiceAccountProviderState(ctx, &pb.LoadServiceAccountProviderStateRequest{GatewayId: gatewayID, ServiceAccountId: accountID, Cleanup: cleanup})
			if err != nil {
				return runtime.SealedStateRecord{}, err
			}
			return check(record)
		},
		Save: func(ctx context.Context, expected int64, sealed []byte) (runtime.SealedStateRecord, error) {
			record, err := client.SaveServiceAccountProviderState(ctx, &pb.SaveServiceAccountProviderStateRequest{GatewayId: gatewayID, ServiceAccountId: accountID, Cleanup: cleanup, ExpectedVersion: expected, SealedState: sealed})
			if err != nil {
				return runtime.SealedStateRecord{}, err
			}
			return check(record)
		},
	}, gateways.MaxGatewayProviderStateBytes)
}
