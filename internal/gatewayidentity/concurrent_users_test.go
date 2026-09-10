package gatewayidentity

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

type concurrentUserProvider struct {
	Provider
	mu       sync.Mutex
	cancels  map[string]context.CancelFunc
	subjects map[string][]string
}

func (p *concurrentUserProvider) ReconcileGatewayUser(_ context.Context, id, _, subject, _ string) error {
	p.mu.Lock()
	p.subjects[id] = append(p.subjects[id], subject)
	cancel := p.cancels[id]
	delete(p.cancels, id)
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func TestConcurrentGatewayUserScansKeepSeparateProgress(t *testing.T) {
	provider := &concurrentUserProvider{cancels: map[string]context.CancelFunc{}, subjects: map[string][]string{}}
	controller, err := New(new(apiFixture), new(progressUserState), provider)
	if err != nil {
		t.Fatal(err)
	}
	const gateways = 64
	contexts := make([]context.Context, gateways)
	for i := range contexts {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		contexts[i] = ctx
		provider.cancels[fmt.Sprint(i)] = cancel
	}
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i, ctx := range contexts {
		workers.Add(1)
		go func(id string, ctx context.Context) {
			defer workers.Done()
			<-start
			if err := controller.reconcileUsers(ctx, id); !errors.Is(err, context.Canceled) {
				t.Error("first user scan did not stop", err)
				return
			}
			if err := controller.reconcileUsers(context.Background(), id); err != nil {
				t.Error(err)
			}
		}(fmt.Sprint(i), ctx)
	}
	close(start)
	workers.Wait()
	for id, subjects := range provider.subjects {
		if len(subjects) != 2 || subjects[0] != "first" || subjects[1] != "second" {
			t.Error("Gateway lost its user cursor", id, subjects)
		}
	}
	if len(provider.subjects) != gateways || len(controller.userScans) != 0 {
		t.Fatal("user scans lost a Gateway or retained completed cursors")
	}
}
