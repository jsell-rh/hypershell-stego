// Package resourceevents builds the Hypershell resource event envelope.
package resourceevents

import (
	"encoding/json"

	"github.com/google/uuid"
	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
)

// Notify stages one resource notice in the caller's transaction. The caller
// supplies trusted resource types. Record contents and credentials are absent.
func Notify(tx store.Transaction, source, id, operation, kind string) error {
	payload, err := json.Marshal(struct {
		Source    string `json:"source"`
		SourceID  string `json:"source_id"`
		EventType string `json:"event_type"`
	}{source, id, operation})
	if err != nil {
		return err
	}
	key, err := uuid.NewRandom()
	if err != nil {
		return err
	}
	return tx.Notify(store.Notification{ID: key, Destination: "kafka", ResourceKey: id, Kind: kind, Payload: payload})
}
