package gateways

import (
	"context"
	"errors"
	"testing"

	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"github.com/segmentio/ksuid"
)

type accountStateReadFixture struct {
	store.Transaction
	gateway     model.Gateway
	account     model.ServiceAccount
	cursorCalls int
}

func (f *accountStateReadFixture) GetRetained(_ context.Context, entity, id string) (any, error) {
	if entity != "Gateway" || id != f.gateway.ID {
		return nil, errors.New("only Gateway has versioned retained reads")
	}
	return f.gateway, nil
}
func (f *accountStateReadFixture) ReadCursor(_ context.Context, entity, field, value string, options store.CursorOptions) (store.CursorResult, error) {
	f.cursorCalls++
	if entity != "ServiceAccount" || field != "id" || value != f.account.ID || options.Limit != 1 || options.Deletion != store.CursorAll || options.AfterID != "" {
		return store.CursorResult{}, errors.New("account read is not bounded to its retained identity")
	}
	return store.CursorResult{Items: []model.ServiceAccount{f.account}}, nil
}
func TestAccountJournalReadsUnversionedAccount(t *testing.T) {
	gatewayID, accountID := ksuid.New().String(), ksuid.New().String()
	f := &accountStateReadFixture{gateway: model.Gateway{Meta: model.Meta{ID: gatewayID}, ResourceVersion: 1}, account: model.ServiceAccount{Meta: model.Meta{ID: accountID}, GatewayID: gatewayID, ClientID: "hs-sa-" + gatewayID + "-" + accountID, Status: "provisioning"}}
	if err := checkAccountProviderState(context.Background(), f, gatewayID, accountID, false); err != nil {
		t.Fatal("valid unversioned account reservation was rejected", err)
	}
	if f.cursorCalls != 1 {
		t.Fatal("account did not use the common retained cursor")
	}
	f.account.GatewayID = ksuid.New().String()
	if err := checkAccountProviderState(context.Background(), f, gatewayID, accountID, true); !errors.Is(err, ErrForbidden) {
		t.Fatal("foreign account cleanup was not denied", err)
	}
}
