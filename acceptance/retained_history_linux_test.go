package acceptance

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
)

// Run only in bounded CI containers. This measures the generated SQL cursor
// and domain authorization. It does not measure provider calls or transport.
func BenchmarkRetainedGrantInventory(b *testing.B) {
	for _, size := range []int{10000, 100000} {
		b.Run(fmt.Sprintf("rows-%d", size), func(b *testing.B) {
			f := database(b)
			setup, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			gateway, err := f.service.Create(setup, principal("alice", "gateway:creator"), f.request("retained-history"))
			if err != nil {
				b.Fatal(err)
			}
			var ownerGrant, ownerUser string
			if err := f.db.QueryRowContext(setup, "SELECT id,user_id FROM role_bindings WHERE gateway_id=$1", gateway.ID).Scan(&ownerGrant, &ownerUser); err != nil {
				b.Fatal(err)
			}
			input := grantInput(b, f, gateway.ID, "bob", "gateway:viewer")
			seedDiscoveryGrants(b, f, gateway.ID, input.RoleID, size)
			// Keep 90 percent of the synthetic grants as deleted history.
			result, err := f.db.ExecContext(setup, "UPDATE role_bindings SET deleted_at=now() WHERE gateway_id=$1 AND id<>$2 AND right(id,1)<>'0'", gateway.ID, ownerGrant)
			if err != nil {
				b.Fatal(err)
			}
			if count, err := result.RowsAffected(); err != nil || count != int64(size*9/10) {
				b.Fatal("retained history has the wrong size", count, err)
			}
			analyzeDiscoveryFixture(b, f)
			service, err := gateways.New(f.storage, gateways.Options{ControlPlaneSubjects: []string{"controller"}})
			if err != nil {
				b.Fatal(err)
			}
			if _, _, err := service.IdentityUserReferences(setup, principal("alice"), gateway.ID, "", 100); !errors.Is(err, gateways.ErrForbidden) {
				b.Fatal("an unauthorized inventory request was accepted", err)
			}
			reader := principal("controller")
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				ctx, stop := context.WithTimeout(context.Background(), time.Minute)
				after, count, pages := "", 0, 0
				for {
					refs, more, err := service.IdentityUserReferences(ctx, reader, gateway.ID, after, 100)
					if err != nil || len(refs) == 0 || len(refs) > 100 {
						stop()
						b.Fatal("retained inventory page failed", len(refs), err)
					}
					pages++
					for _, ref := range refs {
						count++
						if ref.GrantID <= after {
							stop()
							b.Fatal("retained inventory did not advance")
						}
						if count <= size {
							n, err := strconv.Atoi(ref.GrantID)
							if err != nil || n != count || ref.UserID != ref.GrantID {
								stop()
								b.Fatal("retained inventory skipped or changed a grant")
							}
						} else if count != size+1 || ref.GrantID != ownerGrant || ref.UserID != ownerUser {
							stop()
							b.Fatal("retained inventory has an unexpected tail")
						}
						after = ref.GrantID
					}
					if !more {
						break
					}
					if pages > size/100 {
						stop()
						b.Fatal("retained inventory exceeded its page bound")
					}
				}
				stop()
				if count != size+1 || pages != size/100+1 {
					b.Fatal("retained inventory is incomplete", count, pages)
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(size+1), "rows/op")
			b.ReportMetric(float64(size/100+1), "pages/op")
			var usage syscall.Rusage
			if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
				b.Fatal(err)
			}
			// Linux reports KiB. This is the process high-water mark, including
			// setup and earlier cases. It is not memory use for one operation.
			b.ReportMetric(float64(usage.Maxrss)*1024, "process-max-rss-B")
		})
	}
}
