package acceptance

import (
	"context"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"testing"
)

func BenchmarkGatewayIdentityUserState(b *testing.B) {
	f := database(b)
	ctx := context.Background()
	owner := principal("alice", "gateway:creator")
	gateway, err := f.service.Create(ctx, owner, f.request("identity-state-benchmark"))
	if err != nil {
		b.Fatal(err)
	}
	other, err := f.service.Create(ctx, owner, f.request("unrelated-gateway"))
	if err != nil {
		b.Fatal(err)
	}
	input := grantInput(b, f, gateway.ID, "bob", "gateway:owner")
	if _, err := f.service.CreateGrant(ctx, owner, input); err != nil {
		b.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO users(id,created_time,updated_time,username,issuer,subject,email,name)
 SELECT lpad(n::text,27,'0'),now(),now(),'benchmark','https://issuer.example','identity-subject-'||n,'',''
 FROM generate_series(1,10000) AS n`); err != nil {
		b.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO role_bindings(id,user_id,role_id,gateway_id,scope,created_time,updated_time)
 SELECT lpad(n::text,27,'0'),lpad(n::text,27,'0'),$1,$2,'gateway',now(),now()
 FROM generate_series(1,10000) AS n`, input.RoleID, other.ID); err != nil {
		b.Fatal(err)
	}
	if _, err := f.db.Exec("ANALYZE users; ANALYZE role_bindings"); err != nil {
		b.Fatal(err)
	}
	service, err := gateways.New(f.storage, gateways.Options{DatabaseProvider: gateways.ProviderCNPG, ControlPlaneSubjects: []string{"controller"}})
	if err != nil {
		b.Fatal(err)
	}
	reader := principal("controller")
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		state, err := service.IdentityUserState(ctx, reader, gateway.ID, input.UserID)
		if err != nil || state.Role != "gateway:owner" {
			b.Fatal("read current access", err)
		}
	}
}
