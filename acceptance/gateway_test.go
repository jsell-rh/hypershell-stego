package acceptance

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/jsell-rh/hypershell-stego/internal/catalog"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	contract "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"github.com/segmentio/ksuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type fixture struct {
	db                         *sql.DB
	dsn                        string
	storage                    *model.Store
	service                    *gateways.Service
	cluster, release, database string
}

func database(t testing.TB) *fixture { return databaseSetup(t, true) }

func databaseSetup(t testing.TB, seedPlacement bool) *fixture {
	t.Helper()
	dsn := os.Getenv("STEGO_TEST_POSTGRES_DSN")
	if dsn == "" {
		if os.Getenv("STEGO_REQUIRE_POSTGRES") == "1" {
			t.Fatal("PostgreSQL acceptance tests require STEGO_TEST_POSTGRES_DSN")
		}
		t.Skip("set STEGO_TEST_POSTGRES_DSN for application acceptance tests")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { admin.Close() })
	name := "hypershell_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := admin.ExecContext(ctx, `CREATE DATABASE "`+name+`"`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, `DROP DATABASE "`+name+`" WITH (FORCE)`); err != nil {
			t.Error(err)
		}
	})
	cfg.Database = name
	privateDSN := dsn + " dbname=" + name
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		address, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		address.Path = "/" + name
		query := address.Query()
		query.Del("dbname")
		query.Del("database")
		address.RawQuery = query.Encode()
		privateDSN = address.String()
	}
	check, err := pgx.ParseConfig(privateDSN)
	if err != nil || check.Database != name {
		t.Fatal("private database DSN is invalid")
	}
	db := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { db.Close() })
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	// Schema creation is an explicit acceptance setup step, outside request handling.
	if err := model.Migrate(orm); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../out/outbox/migrations/000001_outbox.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	s, err := model.NewStore(orm)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := gateways.New(s)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{db: db, dsn: privateDSN, storage: s, service: svc, cluster: ksuid.New().String(), release: ksuid.New().String(), database: ksuid.New().String()}
	databaseNamespace, err := catalog.DatabaseNamespace(f.database)
	if err != nil {
		t.Fatal(err)
	}
	if seedPlacement {
		for entity, value := range map[string]any{
			"ManagedCluster":  model.ManagedCluster{Meta: model.Meta{ID: f.cluster}, Name: "cluster", Provider: "kubernetes", KubeconfigSecret: "test-cluster"},
			"GatewayRelease":  model.GatewayRelease{Meta: model.Meta{ID: f.release}, Name: "release", Image: "registry.example/gateway:v1"},
			"ManagedDatabase": model.ManagedDatabase{Meta: model.Meta{ID: f.database}, Name: "database", Provider: "cnpg", Namespace: databaseNamespace},
		} {
			if err := s.Create(ctx, entity, value); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := applyRoleCatalog(ctx, db); err != nil {
		t.Fatal(err)
	}
	migration, err = os.ReadFile("../migrations/000005_global_roles.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	migration, err = os.ReadFile("../migrations/000006_placement_catalog.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	return f
}

func BenchmarkGatewayFilteredPage(b *testing.B) {
	f := database(b)
	ctx := context.Background()
	for i := range 200 {
		name := "alice"
		if i%2 == 1 {
			name = "bob"
		}
		if _, err := f.service.Create(ctx, principal(name, "gateway:creator"), f.request(fmt.Sprintf("gateway-%d", i))); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		result, err := f.service.List(ctx, principal("alice"), 1, 20)
		if err != nil || result.Total != 100 {
			b.Fatalf("filtered page: %v", err)
		}
	}
}
func (f *fixture) request(name string) gateways.CreateRequest {
	return gateways.CreateRequest{Name: name, ClusterID: f.cluster, ReleaseID: f.release, DatabaseID: "client-placeholder"}
}
func principal(name string, roles ...string) gateways.Principal {
	return gateways.Principal{Issuer: "https://issuer.example", Subject: name, Username: name, Email: name + "@example.test", Name: name, Roles: roles}
}
func count(t testing.TB, db *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestGatewayCreationCommitsOwnerAndEvent(t *testing.T) {
	f := database(t)
	ctx := context.Background()
	p := principal("alice", "gateway:creator")
	gateway, err := f.service.Create(ctx, p, f.request("first"))
	if err != nil {
		t.Fatal(err)
	}
	id, err := ksuid.Parse(gateway.ID)
	if err != nil {
		t.Fatalf("Gateway ID: %v", err)
	}
	if gateway.Namespace != "openshell-"+hex.EncodeToString(id.Payload()[:8]) {
		t.Fatalf("namespace: %s", gateway.Namespace)
	}
	if gateway.DatabaseID != f.database {
		t.Fatal("client database_id was not replaced")
	}
	if gateway.CreatedTime.IsZero() || gateway.UpdatedTime.IsZero() {
		t.Fatal("stored timestamps are missing")
	}
	if count(t, f.db, "gateways") != 1 || count(t, f.db, "role_bindings") != 1 || count(t, f.db, "stego_outbox.messages") != 2 {
		t.Fatal("creation did not commit the resource, grant, and both events")
	}
	var username, role, scope, key, kind, payload string
	if err := f.db.QueryRow(`SELECT u.username,r.name,b.scope FROM role_bindings b JOIN users u ON u.id=b.user_id JOIN roles r ON r.id=b.role_id WHERE b.gateway_id=$1`, gateway.ID).Scan(&username, &role, &scope); err != nil {
		t.Fatal(err)
	}
	if username != "alice" || role != "gateway:owner" || scope != "gateway" {
		t.Fatalf("owner grant: %s %s %s", username, role, scope)
	}
	if err := f.db.QueryRow("SELECT resource_key,kind,payload FROM stego_outbox.messages WHERE kind='gateway.created'").Scan(&key, &kind, &payload); err != nil {
		t.Fatal(err)
	}
	if key != gateway.ID || kind != "gateway.created" || !strings.Contains(payload, gateway.ID) {
		t.Fatal("event does not identify the committed Gateway")
	}
	// The creator role is no longer present. The owner grant must still work.
	got, err := f.service.Get(ctx, principal("alice"), gateway.ID)
	if err != nil || got.ID != gateway.ID {
		t.Fatalf("owner read after creator removal: %v", err)
	}
	if _, err := f.service.Create(ctx, principal("alice"), f.request("denied")); !errors.Is(err, gateways.ErrForbidden) {
		t.Fatalf("revoked creator created a Gateway: %v", err)
	}
}

func TestOwnerGrantFailureRollsBackGatewayAndEvent(t *testing.T) {
	f := database(t)
	if _, err := f.db.Exec(`ALTER TABLE role_bindings ADD CONSTRAINT test_reject_owner CHECK (scope <> 'gateway')`); err != nil {
		t.Fatal(err)
	}
	gateway, err := f.service.Create(context.Background(), principal("alice", "gateway:creator"), f.request("rollback"))
	if err == nil || gateway.ID != "" {
		t.Fatal("failed grant reported success")
	}
	for _, table := range []string{"gateways", "role_bindings", "stego_outbox.messages", "users"} {
		if count(t, f.db, table) != 0 {
			t.Fatalf("failed owner grant left a row in %s", table)
		}
	}
}

func TestEventFailureRollsBackGatewayAndOwner(t *testing.T) {
	for _, kind := range []string{"gateway.created", "rolebinding.created"} {
		t.Run(kind, func(t *testing.T) {
			f := database(t)
			if _, err := f.db.Exec(`ALTER TABLE stego_outbox.messages ADD CONSTRAINT test_reject_event CHECK (kind <> '` + kind + `')`); err != nil {
				t.Fatal(err)
			}
			if _, err := f.service.Create(context.Background(), principal("alice", "gateway:creator"), f.request("rollback")); err == nil {
				t.Fatal("failed event reported success")
			}
			for _, table := range []string{"gateways", "role_bindings", "stego_outbox.messages"} {
				if count(t, f.db, table) != 0 {
					t.Fatalf("failed event left a row in %s", table)
				}
			}

		})
	}
}

func TestAccessFiltersRunBeforeCountAndPagination(t *testing.T) {
	f := database(t)
	ctx := context.Background()
	visible := map[string]bool{}
	hidden := ""
	for i := 0; i < 8; i++ {
		owner := "alice"
		if i%2 == 1 {
			owner = "bob"
		}
		gateway, err := f.service.Create(ctx, principal(owner, "gateway:creator"), f.request(fmt.Sprintf("gateway-%d", i)))
		if err != nil {
			t.Fatal(err)
		}
		if owner == "alice" {
			visible[gateway.ID] = true
		} else {
			hidden = gateway.ID
		}
	}
	seen := map[string]bool{}
	for page := 1; page <= 5; page++ {
		result, err := f.service.List(ctx, principal("alice"), page, 1)
		if err != nil {
			t.Fatal(err)
		}
		if result.Total != 4 {
			t.Fatalf("unfiltered total: %d", result.Total)
		}
		rows := result.Items.([]model.Gateway)
		if page <= 4 && len(rows) != 1 {
			t.Fatalf("page %d is not full", page)
		}
		if page == 5 && len(rows) != 0 {
			t.Fatal("extra page is not empty")
		}
		for _, row := range rows {
			if !visible[row.ID] || seen[row.ID] {
				t.Fatalf("page exposed or repeated %s", row.ID)
			}
			seen[row.ID] = true
		}
	}
	if len(seen) != 4 {
		t.Fatal("visible Gateway is missing")
	}
	for _, id := range []string{hidden, ksuid.New().String(), "invalid"} {
		if _, err := f.service.Get(ctx, principal("alice"), id); !errors.Is(err, contract.ErrNotFound) {
			t.Fatalf("denied read must be opaque: %v", err)
		}
	}
	admin := principal("admin", "platform:admin")
	result, err := f.service.List(ctx, admin, 1, 100)
	if err != nil || result.Total != 8 {
		t.Fatalf("admin list: %v, %+v", err, result)
	}
	if _, err := f.service.Create(ctx, admin, f.request("admin-denied")); !errors.Is(err, gateways.ErrForbidden) {
		t.Fatalf("admin created without creator role: %v", err)
	}
	// A viewer can read the one granted Gateway, with no global creator role.
	viewer := principal("viewer")
	if _, err := f.service.List(ctx, viewer, 1, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO role_bindings (id,user_id,role_id,gateway_id,scope) SELECT $1,u.id,r.id,$2,'gateway' FROM users u,roles r WHERE u.username='viewer' AND r.name='gateway:viewer'`, ksuid.New().String(), hidden); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Get(ctx, viewer, hidden); err != nil {
		t.Fatalf("viewer read: %v", err)
	}
	if _, err := f.service.Create(ctx, viewer, f.request("viewer-denied")); !errors.Is(err, gateways.ErrForbidden) {
		t.Fatalf("viewer create: %v", err)
	}
	if _, err := f.db.Exec(`UPDATE role_bindings SET deleted_at=now() WHERE user_id=(SELECT id FROM users WHERE username='viewer')`); err != nil {
		t.Fatal(err)
	}
	result, err = f.service.List(ctx, viewer, 1, 100)
	if err != nil || result.Total != 0 {
		t.Fatalf("revoked viewer grant remains visible: %v", err)
	}
}

func TestRelatedFilterRejectsInvalidReferencesAndSQL(t *testing.T) {
	f := database(t)
	ctx := context.Background()
	for _, filter := range []contract.RelatedFilter{
		{Entity: "RoleBinding", ForeignField: "user_id"},
		{Entity: "RoleBinding", ForeignField: "gateway_id; DROP TABLE gateways"},
		{Entity: "RoleBinding", ForeignField: "gateway_id", Values: map[string][]string{"user_id OR true --": {"one"}}},
	} {
		if _, err := f.storage.List(ctx, "Gateway", "", "", contract.ListOptions{Page: 1, Size: 1, Related: []contract.RelatedFilter{filter}}); err == nil {
			t.Fatal("invalid relation was accepted")
		}
	}
	for _, value := range []string{"' OR true --", ""} {
		result, err := f.storage.List(ctx, "Gateway", "", "", contract.ListOptions{Page: 1, Size: 1, Related: []contract.RelatedFilter{{Entity: "RoleBinding", ForeignField: "gateway_id", Values: map[string][]string{"user_id": {value}}}}})
		if err != nil || result.Total != 0 {
			t.Fatalf("filter value became SQL: %v", err)
		}
	}
	if _, err := f.storage.List(ctx, "Gateway", "", "", contract.ListOptions{OrderBy: []contract.OrderByField{{Field: "id", Direction: "asc; DROP TABLE gateways"}}}); err == nil {
		t.Fatal("invalid order direction was accepted")
	}
}
