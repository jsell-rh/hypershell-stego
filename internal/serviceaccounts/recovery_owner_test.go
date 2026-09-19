package serviceaccounts

import (
	"context"
	"errors"
	"testing"
	"time"

	storage "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"github.com/segmentio/ksuid"
	"gorm.io/gorm"
)

type recoveryOwnerStore struct {
	storage.Repository
	read func(context.Context, string, string) (any, error)
}

func (s recoveryOwnerStore) GetRetained(ctx context.Context, entity, id string) (any, error) {
	return s.read(ctx, entity, id)
}

type recoveryOwnerProvider struct {
	Provisioner
	delete func(context.Context, string, string, string) error
}

func (p recoveryOwnerProvider) Delete(ctx context.Context, gateway, account, provider string) error {
	return p.delete(ctx, gateway, account, provider)
}

func TestDeletedAccountRecoverySelectsCleanupOwner(t *testing.T) {
	gatewayID, accountID := ksuid.New().String(), ksuid.New().String()
	live := model.Gateway{Meta: model.Meta{ID: gatewayID}}
	deleted := live
	deleted.DeletedAt = gorm.DeletedAt{Time: time.Now(), Valid: true}
	finalized := deleted
	finalized.DeletionFinalizedAt = &deleted.DeletedAt.Time
	failure := errors.New("storage is unavailable")
	providerFailure := errors.New("provider is unavailable")
	for _, tc := range []struct {
		name          string
		value         any
		readError     error
		providerError error
		wantCalls     int
		wantError     error
	}{
		{"deleted-parent", deleted, nil, nil, 0, nil},
		{"finalized-parent", finalized, nil, nil, 0, nil},
		{"live-parent", live, nil, nil, 1, nil},
		{"absent-parent", nil, storage.ErrNotFound, nil, 1, nil},
		{"storage-failure", nil, failure, nil, 0, failure},
		{"invalid-parent", "invalid", nil, nil, 0, runtime.ErrSweepContract},
		{"wrong-parent", model.Gateway{Meta: model.Meta{ID: ksuid.New().String()}}, nil, nil, 0, runtime.ErrSweepContract},
		{"provider-failure", live, nil, providerFailure, 1, providerFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads, calls := 0, 0
			var action context.Context
			s := &Service{
				repository: recoveryOwnerStore{read: func(ctx context.Context, entity, id string) (any, error) {
					reads++
					action = ctx
					deadline, ok := ctx.Deadline()
					if !ok || time.Until(deadline) > 4*time.Second || ctx.Err() != nil || entity != "Gateway" || id != gatewayID {
						t.Error("Parent read lost its identity or time limit")
					}
					return tc.value, tc.readError
				}},
				provider: recoveryOwnerProvider{delete: func(ctx context.Context, gateway, account, provider string) error {
					calls++
					if ctx != action || gateway != gatewayID || account != accountID || provider != "" {
						t.Error("Retained account recovery changed its identity or time limit")
					}
					return tc.providerError
				}},
			}
			row := model.ServiceAccount{Meta: model.Meta{ID: accountID, DeletedAt: deleted.DeletedAt}, GatewayID: gatewayID, ClientUuid: "obsolete-provider-id"}
			if err := s.recoverTask(context.Background(), recoveryTask{row: row, deleted: true}); !errors.Is(err, tc.wantError) || calls != tc.wantCalls || reads != 1 {
				t.Fatal("Incorrect cleanup owner", err, reads, calls)
			}
			if action.Err() != context.Canceled {
				t.Fatal("Recovery kept its action context")
			}
			// The old stream snapshot cannot bypass the stored deletion state.
			if err := s.recoverTask(context.Background(), recoveryTask{row: row, deleted: false}); err != nil || reads != 1 || calls != tc.wantCalls {
				t.Fatal("The wrong stream acted on a deleted row", err)
			}
		})
	}
}

func TestDeletedAccountRecoveryRequiresParentReader(t *testing.T) {
	for _, tc := range []struct {
		name       string
		repository storage.Repository
		gatewayID  string
	}{
		{"no-reader", struct{ storage.Repository }{}, ksuid.New().String()},
		{"empty-id", recoveryOwnerStore{}, ""},
		{"invalid-id", recoveryOwnerStore{}, "invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Service{repository: tc.repository, provider: recoveryOwnerProvider{delete: func(context.Context, string, string, string) error {
				t.Fatal("Invalid parent selection reached the provider")
				return nil
			}}}
			row := model.ServiceAccount{Meta: model.Meta{ID: ksuid.New().String(), DeletedAt: gorm.DeletedAt{Time: time.Now(), Valid: true}}, GatewayID: tc.gatewayID}
			if err := s.recoverTask(context.Background(), recoveryTask{row: row, deleted: true}); !errors.Is(err, runtime.ErrSweepContract) {
				t.Fatal("Invalid parent selection did not stop recovery", err)
			}
		})
	}
}
