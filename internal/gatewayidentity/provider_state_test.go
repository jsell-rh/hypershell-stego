package gatewayidentity

import (
	"bytes"
	"context"
	"testing"

	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	"github.com/segmentio/ksuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type providerJournalFixture struct {
	control.GatewayIdentityServiceClient
	t       *testing.T
	record  *control.GatewayProviderState
	writes  int
	cleanup bool
}

func (f *providerJournalFixture) LoadGatewayProviderState(_ context.Context, request *control.LoadGatewayProviderStateRequest, _ ...grpc.CallOption) (*control.GatewayProviderState, error) {
	if f.record != nil && request.GatewayId == "" {
		f.t.Fatal("missing resource ID")
	}
	return f.record, nil
}
func (f *providerJournalFixture) SaveGatewayProviderState(ctx context.Context, request *control.SaveGatewayProviderStateRequest, _ ...grpc.CallOption) (*control.GatewayProviderState, error) {
	f.writes++
	md, _ := metadata.FromOutgoingContext(ctx)
	if values := md.Get("if-resource-version"); len(values) != 1 || values[0] != "7" {
		f.t.Fatal("observed revision was not carried to storage")
	}
	if values := md.Get("authorization"); len(values) != 1 || values[0] != "Bearer fixture" {
		f.t.Fatal("authentication was not retained")
	}
	if request.GatewayId != f.record.GatewayId || request.ExpectedVersion != f.record.Version || request.Cleanup != f.cleanup || request.ClientKind != f.record.ClientKind {
		f.t.Fatal("save crossed the resource or cleanup boundary")
	}
	f.record.Version++
	f.record.SealedState = bytes.Clone(request.SealedState)
	return f.record, nil
}

func TestProviderStateJournalScopeAndObservation(t *testing.T) {
	p, err := runtime.NewStateProtector([][]byte{bytes.Repeat([]byte{1}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	id := ksuid.New().String()
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer fixture", "if-resource-version", "old"))
	for _, cleanup := range []bool{false, true} {
		f := &providerJournalFixture{t: t, cleanup: cleanup, record: &control.GatewayProviderState{GatewayId: id, ResourceVersion: 7, Deleted: cleanup}}
		journal, err := NewProviderStateJournal(f, p, "instance", id, 7, cleanup)
		if err != nil {
			t.Fatal(err)
		}
		initial, err := journal.Load(ctx)
		if err != nil {
			t.Fatal(err)
		}
		saved, err := journal.Save(ctx, initial, []byte("private checkpoint"))
		if err != nil || saved.Version() != 1 {
			t.Fatal("journal save failed", err)
		}
		if f.writes != 1 || bytes.Contains(f.record.SealedState, []byte("private checkpoint")) {
			t.Fatal("storage did not receive one encrypted record")
		}
		if _, err := p.Open(runtime.StateKey{Instance: "instance", Entity: "Gateway", ResourceID: id, Scope: "identity-provider"}, 1, f.record.SealedState); err != nil {
			t.Fatal("state uses the wrong application scope", err)
		}
		for _, change := range []func(){
			func() { f.record.GatewayId = ksuid.New().String() },
			func() { f.record.GatewayId = id; f.record.ResourceVersion = 8 },
			func() { f.record.ResourceVersion = 7; f.record.Deleted = !cleanup },
			func() { f.record.Deleted = cleanup; f.record.ProtoReflect().SetUnknown([]byte{0x78, 0x01}) },
			func() { f.record = nil },
		} {
			change()
			if _, err := journal.Load(ctx); err == nil {
				t.Fatal("changed provider state response accepted")
			}
		}
		if f.writes != 1 {
			t.Fatal("invalid reads changed provider state")
		}
	}
	for _, badID := range []string{"", "invalid", ksuid.Nil.String()} {
		if _, err := NewProviderStateJournal(&providerJournalFixture{}, p, "instance", badID, 7, false); err == nil {
			t.Fatal("invalid Gateway ID accepted")
		}
	}
	if _, err := NewProviderStateJournal(&providerJournalFixture{}, p, "instance", id, 0, false); err == nil {
		t.Fatal("missing observation accepted")
	}
}

func TestConsoleProviderStateJournalSeparatesNativeScope(t *testing.T) {
	p, err := runtime.NewStateProtector([][]byte{bytes.Repeat([]byte{2}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	id := ksuid.New().String()
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer fixture"))
	f := &providerJournalFixture{t: t, record: &control.GatewayProviderState{GatewayId: id, ResourceVersion: 7, ClientKind: control.GatewayIdentityClientKind_GATEWAY_IDENTITY_CLIENT_KIND_CONSOLE}}
	console, err := NewConsoleProviderStateJournal(f, p, "instance", id, 7, false)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := console.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := console.Save(ctx, initial, []byte("private console state")); err != nil {
		t.Fatal(err)
	}
	key := runtime.StateKey{Instance: "instance", Entity: "Gateway", ResourceID: id, Scope: "console-identity-provider"}
	if _, err := p.Open(key, 1, f.record.SealedState); err != nil {
		t.Fatal(err)
	}
	key.Scope = "identity-provider"
	if _, err := p.Open(key, 1, f.record.SealedState); err == nil {
		t.Fatal("console state opened in native scope")
	}
	native, err := NewProviderStateJournal(f, p, "instance", id, 7, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := native.Load(ctx); err == nil {
		t.Fatal("native journal accepted console response")
	}
	f.record.ClientKind = control.GatewayIdentityClientKind_GATEWAY_IDENTITY_CLIENT_KIND_NATIVE
	if _, err := native.Load(ctx); err == nil {
		t.Fatal("changed kind bypassed encrypted scope")
	}
	if _, err := console.Load(ctx); err == nil {
		t.Fatal("console journal accepted native response")
	}
	f.record.ClientKind = 99
	if _, err := console.Load(ctx); err == nil {
		t.Fatal("unknown client kind accepted")
	}
	if f.writes != 1 {
		t.Fatal("failed reads changed state")
	}
}
