package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/internal/serviceaccounts"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestGatewayGrantsFollowSubjectInsteadOfUsername(t *testing.T) {
	f := database(t)
	key, settings := issuer(t)
	_, config := broker(t, identity(t, "localhost"))
	provider := newAccountProvider()
	providerSettings, _ := startAccountProvisioner(t, provider, key, settings)
	settings = append(settings, providerSettings...)
	tlsIdentity := identity(t, "localhost")
	dir := filepath.Dir(tlsIdentity.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(dir, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(dir, "server-key.pem"))
	binary := buildApplication(t)
	stop, address, grpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	root := address + "/api/hypershell/v1/gateways"
	request := f.request("subject-owned")
	request.Phase = pointer("Running")
	request.Status = pointer("Healthy")
	request.OIDC = pointer(`{"issuer":"https://issuer.example/realms/gateway","client_id":"gateway-audience","audience":"gateway-audience"}`)
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	code, body := requestJSON(t, "POST", root, token(t, key, "alice", "gateway:creator"), body)
	var gateway struct {
		ID string `json:"id"`
	}
	if code != 201 || json.Unmarshal(body, &gateway) != nil {
		t.Fatalf("create Gateway: %d", code)
	}
	claimIssuer := "https://issuer.example"
	sign := func(subject, username string) string {
		t.Helper()
		claims := jwt.MapClaims{"iss": claimIssuer, "aud": "hypershell", "sub": subject, "preferred_username": username, "iat": time.Now().Add(-time.Minute).Unix(), "exp": time.Now().Add(time.Hour).Unix(), "realm_access": map[string]any{"roles": []string{}}}
		value, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	accountPath := root + "/" + gateway.ID + "/service_accounts"
	code, body = requestJSON(t, "POST", accountPath, sign("alice", "alice"), []byte(`{"name":"owner-credential"}`))
	var account struct {
		ID string `json:"id"`
	}
	if code != 201 || json.Unmarshal(body, &account) != nil {
		t.Fatalf("create owner credential: %d", code)
	}
	impostor := sign("different-subject", "alice")
	if code, _ := requestJSON(t, "GET", root+"/"+gateway.ID, impostor, nil); code != 404 {
		t.Errorf("different subject inherited username access: %d", code)
	}
	if code, _ := requestJSON(t, "PATCH", root+"/"+gateway.ID, impostor, []byte(`{"name":"unauthorized"}`)); code != 404 {
		t.Errorf("different subject changed the Gateway: %d", code)
	}
	code, body = requestJSON(t, "GET", root, impostor, nil)
	var list struct {
		Items []json.RawMessage `json:"items"`
	}
	if code != 200 || json.Unmarshal(body, &list) != nil || len(list.Items) != 0 {
		t.Errorf("different subject received the owner's list: %d", code)
	}
	for _, path := range []string{accountPath, accountPath + "/" + account.ID} {
		if code, _ := requestJSON(t, "GET", path, impostor, nil); code != 404 {
			t.Errorf("different subject accessed credentials: %d", code)
		}
	}
	if code, _ := requestJSON(t, "POST", accountPath, impostor, []byte(`{"name":"impostor"}`)); code != 404 {
		t.Errorf("different subject created a credential: %d", code)
	}
	provider.mu.Lock()
	calls := provider.calls
	provider.mu.Unlock()
	if calls != 1 {
		t.Error("denied subject reached credential provider")
	}
	client, _ := grpcClient(t, grpcAddress, tlsIdentity)
	rpcContext := func(bearer string) context.Context {
		return metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+bearer))
	}
	if _, err := client.GetGateway(rpcContext(impostor), &pb.GetGatewayRequest{Id: gateway.ID}); status.Code(err) != codes.NotFound {
		t.Errorf("gRPC subject isolation: %v", err)
	}
	renamed := sign("alice", "alice-renamed")
	if code, _ := requestJSON(t, "GET", root+"/"+gateway.ID, renamed, nil); code != 200 {
		t.Errorf("username change lost the subject's grant: %d", code)
	}
	if _, err := client.GetGateway(rpcContext(renamed), &pb.GetGatewayRequest{Id: gateway.ID}); err != nil {
		t.Errorf("gRPC renamed subject: %v", err)
	}
	if code, _ := requestJSON(t, "GET", accountPath+"/"+account.ID, renamed, nil); code != 200 {
		t.Errorf("rename lost credential ownership: %d", code)
	}
	stop()
	stop, address, _ = startBoth(t, binary, f.dsn, config, settings...)
	defer stop()
	root = address + "/api/hypershell/v1/gateways"
	if code, _ := requestJSON(t, "GET", root+"/"+gateway.ID, renamed, nil); code != 200 {
		t.Errorf("restart lost subject grant: %d", code)
	}
	if code, _ := requestJSON(t, "GET", root+"/"+gateway.ID, impostor, nil); code != 404 {
		t.Errorf("restart transferred a subject grant: %d", code)
	}
	stop()
	claimIssuer = "https://other-issuer.example"
	stop, address, _ = startBoth(t, binary, f.dsn, config, append(settings, "STEGO_AUTH_ISSUER="+claimIssuer)...)
	defer stop()
	root = address + "/api/hypershell/v1/gateways"
	if code, _ := requestJSON(t, "GET", root+"/"+gateway.ID, sign("alice", "alice"), nil); code != 404 {
		t.Errorf("issuer change transferred a subject grant: %d", code)
	}
	if code, _ := requestJSON(t, "GET", root+"/"+gateway.ID, renamed, nil); code != 401 {
		t.Errorf("old issuer remained trusted: %d", code)
	}
}

func TestIdentityMigrationDoesNotAdoptLegacyGrants(t *testing.T) {
	f := database(t)
	provider := newAccountProvider()
	service, gateway := accountService(t, f, provider)
	ctx := context.Background()
	account, err := service.Create(ctx, principal("alice"), gateway.ID, accountInput("before-migration"))
	if err != nil {
		t.Fatal(err)
	}
	var userID string
	if err := f.db.QueryRow("SELECT user_id FROM role_bindings WHERE gateway_id=$1", gateway.ID).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	// Recreate the previous User schema. No API runs during this migration.
	for _, query := range []string{"ALTER TABLE users DROP COLUMN issuer", "ALTER TABLE users DROP COLUMN subject", "CREATE UNIQUE INDEX idx_users_username ON users(username)"} {
		if _, err := f.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	migration, err := os.ReadFile("../migrations/000002_user_identity.sql")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := f.db.Exec(string(migration)); err != nil {
			t.Fatal(err)
		}
	}
	// The new application opens new database connections after the migration.
	orm, err := gorm.Open(postgres.Open(f.dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	database, err := orm.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	migrated, err := model.NewStore(orm)
	if err != nil {
		t.Fatal(err)
	}
	f.service, err = gateways.New(migrated, gateways.Options{DatabaseProvider: gateways.ProviderCNPG})
	if err != nil {
		t.Fatal(err)
	}
	service, err = serviceaccounts.New(migrated, provider)
	if err != nil {
		t.Fatal(err)
	}
	var unbound bool
	if err := f.db.QueryRow("SELECT issuer IS NULL AND subject IS NULL FROM users WHERE id=$1", userID).Scan(&unbound); err != nil || !unbound {
		t.Fatal("migration inferred a legacy identity")
	}
	if _, err := f.service.Get(ctx, principal("alice"), gateway.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("username adopted a legacy grant: %v", err)
	}
	if err := service.Recover(ctx, gateway.ID, account.Account.ID); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := f.db.QueryRow("SELECT status FROM service_accounts WHERE id=$1", account.Account.ID).Scan(&state); err != nil || state != "revoked" {
		t.Fatal("unbound creator retained a credential")
	}
	provider.mu.Lock()
	_, exists := provider.clients[account.Account.ID]
	provider.mu.Unlock()
	if exists {
		t.Fatal("unbound creator retained provider access")
	}
}

func TestIssuerSeparatesEqualSubjects(t *testing.T) {
	f := database(t)
	ctx := context.Background()
	owner := principal("alice", "gateway:creator")
	gateway, err := f.service.Create(ctx, owner, f.request("issuer-owned"))
	if err != nil {
		t.Fatal(err)
	}
	other := owner
	other.Issuer = "https://other-issuer.example"
	other.Roles = nil
	if _, err := f.service.Get(ctx, other, gateway.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("other issuer inherited a subject grant: %v", err)
	}
	if _, err := f.service.Get(ctx, owner, gateway.ID); err != nil {
		t.Fatalf("issuer isolation removed owner access: %v", err)
	}
}

func BenchmarkGatewaySubjectLookup(b *testing.B) {
	f := database(b)
	ctx := context.Background()
	owner := principal("benchmark-owner", "gateway:creator")
	gateway, err := f.service.Create(ctx, owner, f.request("subject-lookup"))
	if err != nil {
		b.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO users(id,created_time,updated_time,username,issuer,subject,email,name)
 SELECT lpad(n::text,27,'0'),now(),now(),'shared-profile-name','https://issuer.example','benchmark-subject-'||n,'',''
 FROM generate_series(1,10000) AS n`); err != nil {
		b.Fatal(err)
	}
	if _, err := f.db.Exec("ANALYZE users"); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := f.service.Get(ctx, owner, gateway.ID); err != nil {
			b.Fatal(err)
		}
	}
}
