package gatewayworkload

import (
	"context"
	"encoding/hex"
	"errors"

	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
)

// SQLBindings registers retained state before SQL work. Production uses the
// authenticated control-plane client. The interface also supports fault tests.
type SQLBindings interface {
	Load(context.Context, string, string) (store.EffectBinding, error)
	Bind(context.Context, string, string, string) (store.EffectBinding, error)
	Close(context.Context, string, string) (store.EffectBinding, error)
}

type remoteSQLBindings struct {
	client control.GatewayIdentityServiceClient
}

func NewSQLBindings(client control.GatewayIdentityServiceClient) (SQLBindings, error) {
	if client == nil {
		return nil, errors.New("SQL state requires a control-plane client")
	}
	return &remoteSQLBindings{client: client}, nil
}
func sqlBinding(value *control.GatewaySQLStateBinding, id, cluster string) (store.EffectBinding, error) {
	invalid := errors.New("SQL state response is invalid")
	if value == nil || value.GetGatewayId() != id || value.GetClusterId() != cluster {
		return store.EffectBinding{}, invalid
	}
	result := store.EffectBinding{Present: value.GetPresent(), Digest: value.GetDigest(), Closed: value.GetClosed()}
	if !result.Present {
		if result.Digest != "" || result.Closed {
			return store.EffectBinding{}, invalid
		}
		return result, nil
	}
	if result.Closed && result.Digest == "" {
		return result, nil
	}
	decoded, err := hex.DecodeString(result.Digest)
	if err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != result.Digest {
		return store.EffectBinding{}, invalid
	}
	return result, nil
}
func (b *remoteSQLBindings) Load(ctx context.Context, id, cluster string) (store.EffectBinding, error) {
	value, err := b.client.LoadGatewaySQLState(ctx, &control.GatewaySQLStateRequest{GatewayId: id, ClusterId: cluster})
	if err != nil {
		return store.EffectBinding{}, err
	}
	return sqlBinding(value, id, cluster)
}
func (b *remoteSQLBindings) Bind(ctx context.Context, id, cluster, digest string) (store.EffectBinding, error) {
	value, err := b.client.BindGatewaySQLState(ctx, &control.BindGatewaySQLStateRequest{GatewayId: id, ClusterId: cluster, Digest: digest})
	if err != nil {
		return store.EffectBinding{}, err
	}
	result, err := sqlBinding(value, id, cluster)
	if err == nil && (!result.Present || result.Closed || result.Digest != digest) {
		err = errors.New("SQL state registration was not confirmed")
	}
	return result, err
}
func (b *remoteSQLBindings) Close(ctx context.Context, id, cluster string) (store.EffectBinding, error) {
	value, err := b.client.CloseGatewaySQLState(ctx, &control.GatewaySQLStateRequest{GatewayId: id, ClusterId: cluster})
	if err != nil {
		return store.EffectBinding{}, err
	}
	result, err := sqlBinding(value, id, cluster)
	if err == nil && (!result.Present || !result.Closed) {
		err = errors.New("SQL state closure was not confirmed")
	}
	return result, err
}
