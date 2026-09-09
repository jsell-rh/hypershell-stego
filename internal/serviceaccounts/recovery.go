package serviceaccounts

import (
	"context"
	"fmt"
	"sync"
	"time"

	storage "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
)

// Run recovers terminal actions and checks expiry and creator access. It never
// provisions a client, raises an existing role, or reconstructs a credential.
func (s *Service) Run(ctx context.Context) error {
	if s.provider == nil {
		<-ctx.Done()
		return nil
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	cursors := map[string]string{}
	states := []string{"error", "deleting", "revoking", "provisioning", "ready", "degraded", "drift", "revoked", "expired"}
	position := 0
	for {
		scan, cancel := context.WithTimeout(ctx, 4*time.Second)
		state := states[position]
		passes := []bool{false}
		if state == "error" || state == "deleting" || state == "provisioning" {
			passes = append(passes, true)
		}
		// Current work precedes historical checks. Both share the scan deadline.
		for _, includeDeleted := range passes {
			cursorKey := state
			if includeDeleted {
				cursorKey += "/deleted"
			}
			if scan.Err() != nil {
				break
			}
			search := ""
			if cursor := cursors[cursorKey]; cursor != "" {
				search = "id > '" + cursor + "'"
			}
			condition := ""
			queryState := state
			if state == "drift" {
				queryState = "ready"
			}
			if state == "ready" {
				condition = "expires_at <= '" + s.now().UTC().Format(time.RFC3339Nano) + "'"
			}
			if state == "provisioning" {
				condition = "created_time <= '" + s.now().Add(-ReclaimAfter).UTC().Format(time.RFC3339Nano) + "'"
			}
			if condition != "" {
				if search != "" {
					search += " and "
				}
				search += condition
			}
			result, err := s.repository.List(scan, "ServiceAccount", "status", queryState, storage.ListOptions{Page: 1, Size: 100, Search: search, IncludeDeleted: includeDeleted, OrderBy: []storage.OrderByField{{Field: "id", Direction: "asc"}}})
			if err != nil {
				continue
			}
			rows, ok := result.Items.([]model.ServiceAccount)
			if !ok {
				cancel()
				return fmt.Errorf("unexpected service-account recovery result")
			}
			permits := make(chan struct{}, 8)
			var wait sync.WaitGroup
			for _, row := range rows {
				if !validID(row.ID) {
					cancel()
					wait.Wait()
					return fmt.Errorf("invalid service-account recovery ID")
				}
				cursors[cursorKey] = row.ID
				// The historical query includes live roots; their normal pass owns them.
				if row.DeletedAt.Valid != includeDeleted {
					continue
				}
				select {
				case permits <- struct{}{}:
				case <-scan.Done():
					break
				}
				if scan.Err() != nil {
					break
				}
				wait.Go(func() {
					defer func() { <-permits }()
					if row.DeletedAt.Valid {
						// A terminal record cannot be restored. Cleanup needs no live
						// parent and uses stable IDs, even if the provider UUID was lost.
						_ = s.provider.Delete(scan, row.GatewayID, row.ID, "")
						return
					}
					_ = s.Recover(scan, row.GatewayID, row.ID)
				})
			}
			wait.Wait()
			if len(rows) < 100 {
				cursors[cursorKey] = ""
			}
		}
		cancel()
		position = (position + 1) % len(states)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
