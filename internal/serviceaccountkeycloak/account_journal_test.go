package serviceaccountkeycloak

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"sync"
	"testing"

	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
)

func testAccountJournals(t *testing.T) func(string, string, bool) (*runtime.StateJournal, error) {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	protector, err := runtime.NewStateProtector([][]byte{key})
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	records := map[string]runtime.SealedStateRecord{}
	return func(gatewayID, id string, _ bool) (*runtime.StateJournal, error) {
		return runtime.NewStateJournal(protector, runtime.StateKey{Instance: "test", Entity: "ServiceAccount", ResourceID: id, Scope: "gateway:" + gatewayID}, runtime.StatePersistence{
			Load: func(context.Context) (runtime.SealedStateRecord, error) {
				mu.Lock()
				defer mu.Unlock()
				r := records[gatewayID+":"+id]
				return runtime.SealedStateRecord{Version: r.Version, Data: bytes.Clone(r.Data)}, nil
			},
			Save: func(_ context.Context, expected int64, data []byte) (runtime.SealedStateRecord, error) {
				mu.Lock()
				defer mu.Unlock()
				if records[gatewayID+":"+id].Version != expected {
					return runtime.SealedStateRecord{}, errors.New("test state conflict")
				}
				r := runtime.SealedStateRecord{Version: expected + 1, Data: bytes.Clone(data)}
				records[gatewayID+":"+id] = r
				return r, nil
			},
		}, 60<<10)
	}
}
