package acceptance

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/jsell-rh/hypershell-stego/internal/serviceaccounts"
)

func TestConfiguredServiceAccountQuotaRemainsAtomic(t *testing.T) {
	f := database(t)
	provider := newAccountProvider()
	_, gateway := accountService(t, f, provider)
	service, err := serviceaccounts.NewWithLimits(f.storage, provider, serviceaccounts.Limits{PerGateway: 4, PerCreator: 3})
	if err != nil {
		t.Fatal(err)
	}
	create := func(creator string, requests, wanted int, quota error) {
		t.Helper()
		results := make(chan error, requests)
		start := make(chan struct{})
		var workers sync.WaitGroup
		for i := range requests {
			workers.Go(func() {
				<-start
				_, err := service.Create(context.Background(), principal(creator), gateway.ID, accountInput(fmt.Sprintf("%s-%d", creator, i)))
				results <- err
			})
		}
		close(start)
		workers.Wait()
		close(results)
		created := 0
		for err := range results {
			if err == nil {
				created++
			} else if !errors.Is(err, quota) {
				t.Fatal("unexpected quota result", err)
			}
		}
		if created != wanted {
			t.Fatal("concurrent reservations crossed a quota", created, wanted)
		}
	}
	create("alice", 4, 3, serviceaccounts.ErrCreatorQuota)
	grantViewer(t, f, gateway.ID, "bob")
	create("bob", 2, 1, serviceaccounts.ErrGatewayQuota)
	if count(t, f.db, "service_accounts") != 4 || provider.calls != 4 {
		t.Fatal("denied reservations reached the provider")
	}
}
