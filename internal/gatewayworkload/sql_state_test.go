package gatewayworkload

import (
	"context"
	"strings"
	"sync"
	"testing"

	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
)

// This fixture models the common binding contract. The API tests use PostgreSQL.
type bindingFixture struct {
	mu     sync.Mutex
	values map[string]store.EffectBinding
}

func (b *bindingFixture) Load(_ context.Context, id, cluster string) (store.EffectBinding, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.values[cluster+":"+id], nil
}
func (b *bindingFixture) Bind(_ context.Context, id, cluster, digest string) (store.EffectBinding, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.values == nil {
		b.values = map[string]store.EffectBinding{}
	}
	key := cluster + ":" + id
	value := b.values[key]
	if value.Closed || (value.Present && value.Digest != digest) {
		return store.EffectBinding{}, store.ErrEffectBindingConflict
	}
	value = store.EffectBinding{Present: true, Digest: digest}
	b.values[key] = value
	return value, nil
}
func (b *bindingFixture) Close(_ context.Context, id, cluster string) (store.EffectBinding, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.values == nil {
		b.values = map[string]store.EffectBinding{}
	}
	key := cluster + ":" + id
	value := b.values[key]
	value.Present = true
	value.Closed = true
	b.values[key] = value
	return value, nil
}

func TestSQLBindingResponseRequiresExactIdentityAndValidState(t *testing.T) {
	for _, value := range []*control.GatewaySQLStateBinding{
		nil,
		{GatewayId: "other", ClusterId: "cluster"},
		{GatewayId: "id", ClusterId: "cluster", Component: control.GatewaySQLComponent_GATEWAY_SQL_COMPONENT_CONSOLE},
		{GatewayId: "id", ClusterId: "other"},
		{GatewayId: "id", ClusterId: "cluster", Closed: true},
		{GatewayId: "id", ClusterId: "cluster", Digest: strings.Repeat("a", 64)},
		{GatewayId: "id", ClusterId: "cluster", Present: true},
		{GatewayId: "id", ClusterId: "cluster", Present: true, Digest: strings.Repeat("A", 64)},
	} {
		if _, err := sqlBinding(value, "id", "cluster", control.GatewaySQLComponent_GATEWAY_SQL_COMPONENT_GATEWAY); err == nil {
			t.Fatal("invalid SQL state response accepted")
		}
	}
	for _, value := range []*control.GatewaySQLStateBinding{
		{GatewayId: "id", ClusterId: "cluster"},
		{GatewayId: "id", ClusterId: "cluster", Present: true, Closed: true},
		{GatewayId: "id", ClusterId: "cluster", Present: true, Digest: strings.Repeat("a", 64)},
		{GatewayId: "id", ClusterId: "cluster", Present: true, Closed: true, Digest: strings.Repeat("a", 64)},
	} {
		if _, err := sqlBinding(value, "id", "cluster", control.GatewaySQLComponent_GATEWAY_SQL_COMPONENT_GATEWAY); err != nil {
			t.Fatal("valid SQL state response rejected", err)
		}
	}
}

type consoleBindingFixture struct {
	bindingFixture
	completed bool
}

func (b *consoleBindingFixture) Complete(ctx context.Context, id, cluster string) (store.EffectBinding, error) {
	value, err := b.Load(ctx, id, cluster)
	if err != nil {
		return value, err
	}
	if !value.Present || !value.Closed {
		return value, store.ErrEffectBindingConflict
	}
	b.mu.Lock()
	b.completed = true
	b.mu.Unlock()
	return value, nil
}
