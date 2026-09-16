package acceptance

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
)

// Workers retain the same external key file across process restarts. Direct
// provider-only fixtures use an isolated journal; real workers use the API store.
func withIdentityState(t *testing.T, k *keycloakFixture) *keycloakFixture {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal([]string{base64.StdEncoding.EncodeToString(key)})
	var instance [16]byte
	if _, err := rand.Read(instance[:]); err != nil {
		t.Fatal(err)
	}
	k.instanceID = "acceptance-" + hex.EncodeToString(instance[:])
	k.stateKeysFile = filepath.Join(t.TempDir(), "identity-state-keys.json")
	if err := os.WriteFile(k.stateKeysFile, data, 0600); err != nil {
		t.Fatal(err)
	}
	protector, err := runtime.NewStateProtectorFromJSON(data)
	clear(data)
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	records := map[string]runtime.SealedStateRecord{}
	k.options.GatewayJournal = func(id string, _ int64, _ bool) (*runtime.StateJournal, error) {
		return runtime.NewStateJournal(protector, runtime.StateKey{Instance: k.instanceID, Entity: "Gateway", ResourceID: id, Scope: "identity-provider"}, runtime.StatePersistence{
			Load: func(context.Context) (runtime.SealedStateRecord, error) {
				mu.Lock()
				defer mu.Unlock()
				r := records[id]
				return runtime.SealedStateRecord{Version: r.Version, Data: bytes.Clone(r.Data)}, nil
			},
			Save: func(_ context.Context, expected int64, data []byte) (runtime.SealedStateRecord, error) {
				mu.Lock()
				defer mu.Unlock()
				if records[id].Version != expected {
					return runtime.SealedStateRecord{}, errors.New("fixture journal conflict")
				}
				r := runtime.SealedStateRecord{Version: expected + 1, Data: bytes.Clone(data)}
				records[id] = r
				return r, nil
			},
		}, 60<<10)
	}
	k.options.AccountJournal = func(gatewayID, id string, _ bool) (*runtime.StateJournal, error) {
		return runtime.NewStateJournal(protector, runtime.StateKey{Instance: k.instanceID, Entity: "ServiceAccount", ResourceID: id, Scope: "gateway:" + gatewayID}, runtime.StatePersistence{
			Load: func(context.Context) (runtime.SealedStateRecord, error) {
				mu.Lock()
				defer mu.Unlock()
				r := records["account:"+gatewayID+":"+id]
				return runtime.SealedStateRecord{Version: r.Version, Data: bytes.Clone(r.Data)}, nil
			},
			Save: func(_ context.Context, expected int64, data []byte) (runtime.SealedStateRecord, error) {
				mu.Lock()
				defer mu.Unlock()
				if records["account:"+gatewayID+":"+id].Version != expected {
					return runtime.SealedStateRecord{}, errors.New("fixture journal conflict")
				}
				r := runtime.SealedStateRecord{Version: expected + 1, Data: bytes.Clone(data)}
				records["account:"+gatewayID+":"+id] = r
				return r, nil
			},
		}, 60<<10)
	}
	return k
}
