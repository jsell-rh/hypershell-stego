package acceptance

import (
	"context"
	"errors"
	storage "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	"strings"
	"testing"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
)

func seedDiscoveryGrants(t testing.TB, f *fixture, gatewayID, roleID string, size int) {
	t.Helper()
	if _, err := f.db.Exec(`INSERT INTO users(id,created_time,updated_time,username,issuer,subject,email,name)
 SELECT lpad(n::text,27,'0'),now(),now(),'member-'||n,'https://issuer.example','discovery-'||n,'',''
 FROM generate_series(1,$1::integer) AS n`, size); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO role_bindings(id,user_id,role_id,gateway_id,scope,created_time,updated_time)
 SELECT lpad(n::text,27,'0'),lpad(n::text,27,'0'),$1,$2,'gateway',now(),now()
 FROM generate_series(1,$3::integer) AS n`, roleID, gatewayID, size); err != nil {
		t.Fatal(err)
	}
}
func TestGrantDiscoveryUnpagedResponseIsCompleteOrFails(t *testing.T) {
	f := database(t)
	ctx := context.Background()
	owner := principal("alice", "gateway:creator")
	gateway, err := f.service.Create(ctx, owner, f.request("many-grants"))
	if err != nil {
		t.Fatal(err)
	}
	input := grantInput(t, f, gateway.ID, "bob", "gateway:viewer")
	seedDiscoveryGrants(t, f, gateway.ID, input.RoleID, 105)
	rows, err := f.service.AllGrants(ctx, owner, "", gateway.ID)
	if err != nil || len(rows) != 106 {
		t.Fatal("unpaged list truncated", len(rows), err)
	}
	seen := map[string]bool{}
	for _, row := range rows {
		if seen[row.Grant.ID] || row.RoleName == "" || row.Username == "" {
			t.Fatal("incomplete projection", row)
		}
		seen[row.Grant.ID] = true
	}
	// Add enough retained live grants to exceed the response bound.
	if _, err := f.db.Exec(`INSERT INTO users(id,created_time,updated_time,username,issuer,subject,email,name)
 SELECT lpad(n::text,27,'0'),now(),now(),'member-'||n,'https://issuer.example','discovery-'||n,'',''
 FROM generate_series(106,10000) AS n`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO role_bindings(id,user_id,role_id,gateway_id,scope,created_time,updated_time)
 SELECT lpad(n::text,27,'0'),lpad(n::text,27,'0'),$1,$2,'gateway',now(),now()
 FROM generate_series(106,10000) AS n`, input.RoleID, gateway.ID); err != nil {
		t.Fatal(err)
	}
	rows, err = f.service.AllGrants(ctx, owner, "", gateway.ID)
	if !errors.Is(err, gateways.ErrGrantCapacity) || rows != nil {
		t.Fatal("oversized snapshot returned partial rows", len(rows), err)
	}
	page, err := f.service.ListGrants(ctx, owner, gateways.GrantQuery{Page: 101, Size: 100})
	if err != nil || page.Total != 10001 || len(page.Items) != 1 {
		t.Fatal("REST paging lost tail", page.Total, len(page.Items), err)
	}
	if _, err := f.db.Exec("UPDATE role_bindings SET deleted_at=now() WHERE id=lpad('10000',27,'0')"); err != nil {
		t.Fatal(err)
	}
	rows, err = f.service.AllGrants(ctx, owner, "", gateway.ID)
	if err != nil || len(rows) != 10000 {
		t.Fatal("response at capacity failed", len(rows), err)
	}
	// Current ownership is checked again on every request.
	if _, err := f.db.Exec("UPDATE role_bindings SET deleted_at=now() WHERE user_id=(SELECT id FROM users WHERE subject='alice')"); err != nil {
		t.Fatal(err)
	}
	page, err = f.service.ListGrants(ctx, owner, gateways.GrantQuery{Page: 1, Size: 100})
	if err != nil || page.Total != 0 || len(page.Items) != 0 {
		t.Fatal("removed owner retained inventory", page.Total, err)
	}
	if _, err := f.service.EventGrant(ctx, owner, strings.Repeat("0", 26)+"1", false); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("removed owner can read another grant event", err)
	}
}

func BenchmarkGrantDiscoveryFilteredPage(b *testing.B) {
	f := database(b)
	ctx := context.Background()
	owner := principal("alice", "gateway:creator")
	gateway, err := f.service.Create(ctx, owner, f.request("visible-grants"))
	if err != nil {
		b.Fatal(err)
	}
	hidden, err := f.service.Create(ctx, principal("mallory", "gateway:creator"), f.request("hidden-grants"))
	if err != nil {
		b.Fatal(err)
	}
	input := grantInput(b, f, gateway.ID, "bob", "gateway:viewer")
	if _, err := f.service.CreateGrant(ctx, owner, input); err != nil {
		b.Fatal(err)
	}
	seedDiscoveryGrants(b, f, hidden.ID, input.RoleID, 10000)
	if _, err := f.db.Exec("ANALYZE users; ANALYZE role_bindings; ANALYZE gateways"); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		page, err := f.service.ListGrants(ctx, owner, gateways.GrantQuery{Page: 1, Size: 20})
		if err != nil || page.Total != 2 || len(page.Items) != 2 {
			b.Fatal("visible page", page.Total, err)
		}
	}
}
