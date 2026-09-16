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

type ConsoleSQLBindings interface {
	SQLBindings
	Complete(context.Context, string, string) (store.EffectBinding, error)
}

type remoteSQLBindings struct {
	client    control.GatewayIdentityServiceClient
	component control.GatewaySQLComponent
}

func NewSQLBindings(client control.GatewayIdentityServiceClient) (SQLBindings, error) {
	return newSQLBindings(client, control.GatewaySQLComponent_GATEWAY_SQL_COMPONENT_GATEWAY)
}
func NewConsoleSQLBindings(client control.GatewayIdentityServiceClient) (ConsoleSQLBindings, error) {
	return newSQLBindings(client, control.GatewaySQLComponent_GATEWAY_SQL_COMPONENT_CONSOLE)
}
func newSQLBindings(client control.GatewayIdentityServiceClient, component control.GatewaySQLComponent) (*remoteSQLBindings, error) {
	if client == nil {
		return nil, errors.New("SQL state requires a control-plane client")
	}
	return &remoteSQLBindings{client: client, component: component}, nil
}
func sqlBinding(value *control.GatewaySQLStateBinding, id, cluster string, component control.GatewaySQLComponent) (store.EffectBinding, error) {
	invalid := errors.New("SQL state response is invalid")
	if value == nil || value.GetGatewayId() != id || value.GetClusterId() != cluster || value.GetComponent() != component {
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
	operation := b.client.LoadGatewaySQLState
	if b.component == control.GatewaySQLComponent_GATEWAY_SQL_COMPONENT_CONSOLE {
		operation = b.client.LoadGatewayConsoleSQLState
	}
	value, err := operation(ctx, &control.GatewaySQLStateRequest{GatewayId: id, ClusterId: cluster})
	if err != nil {
		return store.EffectBinding{}, err
	}
	return sqlBinding(value, id, cluster, b.component)
}
func (b *remoteSQLBindings) Bind(ctx context.Context, id, cluster, digest string) (store.EffectBinding, error) {
	operation := b.client.BindGatewaySQLState
	if b.component == control.GatewaySQLComponent_GATEWAY_SQL_COMPONENT_CONSOLE {
		operation = b.client.BindGatewayConsoleSQLState
	}
	value, err := operation(ctx, &control.BindGatewaySQLStateRequest{GatewayId: id, ClusterId: cluster, Digest: digest})
	if err != nil {
		return store.EffectBinding{}, err
	}
	result, err := sqlBinding(value, id, cluster, b.component)
	if err == nil && (!result.Present || result.Closed || result.Digest != digest) {
		err = errors.New("SQL state registration was not confirmed")
	}
	return result, err
}
func (b *remoteSQLBindings) Close(ctx context.Context, id, cluster string) (store.EffectBinding, error) {
	operation := b.client.CloseGatewaySQLState
	if b.component == control.GatewaySQLComponent_GATEWAY_SQL_COMPONENT_CONSOLE {
		operation = b.client.CloseGatewayConsoleSQLState
	}
	value, err := operation(ctx, &control.GatewaySQLStateRequest{GatewayId: id, ClusterId: cluster})
	if err != nil {
		return store.EffectBinding{}, err
	}
	result, err := sqlBinding(value, id, cluster, b.component)
	if err == nil && (!result.Present || !result.Closed) {
		err = errors.New("SQL state closure was not confirmed")
	}
	return result, err
}

func (b *remoteSQLBindings) Complete(ctx context.Context, id, cluster string) (store.EffectBinding, error) {
	if b.component != control.GatewaySQLComponent_GATEWAY_SQL_COMPONENT_CONSOLE {
		return store.EffectBinding{}, errors.New("console SQL completion requires its component client")
	}
	value, err := b.client.CompleteGatewayConsoleSQLCleanup(ctx, &control.GatewaySQLStateRequest{GatewayId: id, ClusterId: cluster})
	if err != nil {
		return store.EffectBinding{}, err
	}
	result, err := sqlBinding(value, id, cluster, b.component)
	if err == nil && (!result.Present || !result.Closed) {
		err = errors.New("console SQL completion was not confirmed")
	}
	return result, err
}
