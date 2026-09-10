package acceptance

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestIdentityReferenceCursorUsesBoundedReads(t *testing.T) {
	f := database(t)
	ctx := context.Background()
	gateway, err := f.service.Create(ctx, principal("alice", "gateway:creator"), f.request("cursor-queries"))
	if err != nil {
		t.Fatal(err)
	}
	input := grantInput(t, f, gateway.ID, "bob", "gateway:viewer")
	seedDiscoveryGrants(t, f, gateway.ID, input.RoleID, 205)
	if _, err := f.db.Exec("UPDATE role_bindings SET deleted_at=now() WHERE id=lpad('200',27,'0')"); err != nil {
		t.Fatal(err)
	}
	queries := &recoveryQueryLog{Interface: logger.Default.LogMode(logger.Silent)}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: f.db}), &gorm.Config{Logger: queries})
	if err != nil {
		t.Fatal(err)
	}
	storage, err := model.NewStore(orm)
	if err != nil {
		t.Fatal(err)
	}
	service, err := gateways.New(storage, gateways.Options{ControlPlaneSubjects: []string{"controller"}})
	if err != nil {
		t.Fatal(err)
	}
	after := ""
	total := 0
	foundDeleted := false
	for page := 0; page < 3; page++ {
		queries.reads.Store(0)
		queries.counts.Store(0)
		refs, more, err := service.IdentityUserReferences(ctx, principal("controller"), gateway.ID, after, 100)
		if err != nil || queries.reads.Load() != 2 || queries.counts.Load() != 0 {
			t.Fatal("cursor query contract", err, queries.reads.Load(), queries.counts.Load())
		}
		if more != (page < 2) {
			t.Fatal("incorrect continuation", page, more)
		}
		if page == 0 {
			// This new retained reference sorts before the saved page boundary.
			// It must not move rows in the current scan. A later full scan must find it.
			if _, err := f.db.Exec(`INSERT INTO role_bindings(id,user_id,role_id,gateway_id,scope,created_time,updated_time,deleted_at)
    VALUES ($1,$2,$3,$4,'gateway',now(),now(),now())`, "00000000000000000000000000A", fmt.Sprintf("%027d", 1), input.RoleID, gateway.ID); err != nil {
				t.Fatal(err)
			}
		}
		for _, ref := range refs {
			if ref.GrantID == fmt.Sprintf("%027d", 200) {
				foundDeleted = true
			}
			after = ref.GrantID
			total++
		}
	}
	if total != 206 || !foundDeleted {
		t.Fatal("retained inventory incomplete", total, foundDeleted)
	}
	first, _, err := service.IdentityUserReferences(ctx, principal("controller"), gateway.ID, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	foundInserted := false
	for _, ref := range first {
		foundInserted = foundInserted || ref.GrantID == "00000000000000000000000000A"
	}
	if !foundInserted {
		t.Fatal("new reference was absent from the next full scan")
	}
	for _, p := range []gateways.Principal{principal("alice"), principal("admin", "platform:admin")} {
		queries.reads.Store(0)
		refs, _, err := service.IdentityUserReferences(ctx, p, gateway.ID, "", 100)
		if !errors.Is(err, gateways.ErrForbidden) || refs != nil || queries.reads.Load() != 0 {
			t.Fatal("denied request reached storage", err)
		}
	}
	for _, test := range []struct {
		after string
		limit int
	}{{"bad", 100}, {"", 0}, {"", 101}} {
		queries.reads.Store(0)
		refs, _, err := service.IdentityUserReferences(ctx, principal("controller"), gateway.ID, test.after, test.limit)
		if !errors.Is(err, gateways.ErrInvalid) || refs != nil || queries.reads.Load() != 0 {
			t.Fatal("invalid cursor reached storage", err)
		}
	}
}

func TestIdentityReferenceCursorThroughGeneratedRuntime(t *testing.T) {
	f := database(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	gateway, err := f.service.Create(ctx, principal("alice", "gateway:creator"), f.request("cursor-runtime"))
	if err != nil {
		t.Fatal(err)
	}
	input := grantInput(t, f, gateway.ID, "bob", "gateway:viewer")
	seedDiscoveryGrants(t, f, gateway.ID, input.RoleID, 10105)
	// Keep a duplicate user reference. Provider actions must tolerate repeated users.
	if _, err := f.db.Exec("UPDATE role_bindings SET deleted_at=now(),user_id=lpad('1',27,'0') WHERE id=lpad('2',27,'0')"); err != nil {
		t.Fatal(err)
	}
	rows, err := f.db.Query("SELECT id FROM role_bindings WHERE gateway_id=$1 ORDER BY id", gateway.ID)
	if err != nil {
		t.Fatal(err)
	}
	var expected []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		expected = append(expected, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	_, brokerConfig := broker(t, identity(t, "localhost"))
	key, settings := issuer(t)
	apiTLS := identity(t, "localhost")
	directory := filepath.Dir(apiTLS.config.CAFile)
	settings = append(settings, `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["controller"]`, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"))
	binary := buildApplication(t)
	stop, _, address := startBoth(t, binary, f.dsn, brokerConfig, settings...)
	defer func() { stop() }()
	_, connection := grpcClient(t, address, apiTLS)
	client := control.NewGatewayIdentityServiceClient(connection)
	auth := func(parent context.Context, subject string, roles ...string) context.Context {
		return metadata.NewOutgoingContext(parent, metadata.Pairs("authorization", "Bearer "+token(t, key, subject, roles...)))
	}
	for _, denied := range []context.Context{ctx, auth(ctx, "alice"), auth(ctx, "admin", "platform:admin")} {
		_, err := client.ScanGatewayIdentityUsers(denied, &control.ScanGatewayIdentityUsersRequest{GatewayId: gateway.ID, PageSize: 100})
		if status.Code(err) != codes.PermissionDenied && status.Code(err) != codes.Unauthenticated {
			t.Fatal("unauthorized cursor read", err)
		}
	}
	for _, request := range []*control.ScanGatewayIdentityUsersRequest{{GatewayId: gateway.ID, PageSize: 0}, {GatewayId: gateway.ID, PageSize: 101}, {GatewayId: gateway.ID, PageSize: 100, AfterGrantId: "invalid"}} {
		if _, err := client.ScanGatewayIdentityUsers(auth(ctx, "controller"), request); status.Code(err) != codes.InvalidArgument {
			t.Fatal("invalid RPC accepted", err)
		}
	}
	source := func(ctx context.Context, after string, limit int) (runtime.CursorPage[string], error) {
		response, err := client.ScanGatewayIdentityUsers(auth(ctx, "controller"), &control.ScanGatewayIdentityUsersRequest{GatewayId: gateway.ID, AfterGrantId: after, PageSize: int32(limit)})
		if err != nil {
			return runtime.CursorPage[string]{}, err
		}
		if response.GatewayId != gateway.ID || response.AfterGrantId != after {
			t.Fatal("cursor scope mismatch")
		}
		page := runtime.CursorPage[string]{More: response.HasMore}
		for _, ref := range response.References {
			page.Items = append(page.Items, runtime.CursorItem[string]{Cursor: ref.GrantId, Value: ref.GrantId})
		}
		return page, nil
	}
	var actual []string
	partial, stopPartial := context.WithCancel(ctx)
	options := runtime.ScanOptions{PageSize: 100, MaxPages: 100, PageTimeout: 5 * time.Second}
	progress, err := runtime.ScanFrom(partial, "", source, func(id string) error {
		actual = append(actual, id)
		if len(actual) == 7 {
			stopPartial()
		}
		return nil
	}, options)
	stopPartial()
	if !errors.Is(err, context.Canceled) || progress.After != expected[6] || progress.Complete {
		t.Fatal("partial cursor lost", progress, err)
	}
	if _, err := f.db.Exec("UPDATE role_bindings SET deleted_at=now() WHERE id=$1", expected[200]); err != nil {
		t.Fatal(err)
	}
	stop()
	stop, _, address = startBoth(t, binary, f.dsn, brokerConfig, settings...)
	_, connection = grpcClient(t, address, apiTLS)
	client = control.NewGatewayIdentityServiceClient(connection)
	for passes := 0; !progress.Complete; passes++ {
		if passes > 2 {
			t.Fatal("cursor did not reach the tail")
		}
		progress, err = runtime.ScanFrom(ctx, progress.After, source, func(id string) error { actual = append(actual, id); return nil }, options)
		if err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Equal(actual, expected) || len(actual) != 10106 {
		t.Fatal("restart lost or repeated grant references", len(actual))
	}
	t.Log("The generated runtime resumed a partial page after API restart and read all 10,106 retained grant references")
}

func BenchmarkIdentityRecoveryInventory(b *testing.B) {
	f := database(b)
	ctx := context.Background()
	gateway, err := f.service.Create(ctx, principal("alice", "gateway:creator"), f.request("inventory-bench"))
	if err != nil {
		b.Fatal(err)
	}
	input := grantInput(b, f, gateway.ID, "bob", "gateway:viewer")
	seedDiscoveryGrants(b, f, gateway.ID, input.RoleID, 10105)
	service, err := gateways.New(f.storage, gateways.Options{ControlPlaneSubjects: []string{"controller"}})
	if err != nil {
		b.Fatal(err)
	}
	reader := principal("controller")
	b.Run("offset-page-100", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			refs, more, err := service.IdentityUsers(ctx, reader, gateway.ID, 100)
			if err != nil || len(refs) != 100 || !more {
				b.Fatal(len(refs), more, err)
			}
		}
	})
	b.Run("cursor-after-9900", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			refs, more, err := service.IdentityUserReferences(ctx, reader, gateway.ID, fmt.Sprintf("%027d", 9900), 100)
			if err != nil || len(refs) != 100 || !more {
				b.Fatal(len(refs), more, err)
			}
		}
	})
}
