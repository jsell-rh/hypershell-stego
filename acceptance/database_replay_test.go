package acceptance

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/catalog"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestDatabaseDeleteReplayThroughGeneratedRuntime(t *testing.T) {
	for _, collation := range []string{"C", "und-x-icu"} {
		t.Run(collation, func(t *testing.T) { testDatabaseDeleteReplay(t, collation) })
	}
}

func testDatabaseDeleteReplay(t *testing.T, collation string) {
	f := databaseSetup(t, false)
	// Use fixed SQL so the test does not accept an arbitrary SQL identifier.
	statement := `ALTER TABLE managed_databases ALTER COLUMN id TYPE text COLLATE "C"`
	if collation == "und-x-icu" {
		statement = `ALTER TABLE managed_databases ALTER COLUMN id TYPE text COLLATE "und-x-icu"`
	}
	if _, err := f.db.Exec(statement); err != nil {
		t.Fatal(err)
	}
	policy, err := gateways.New(f.storage, gateways.Options{ControlPlaneSubjects: []string{"controller"}})
	if err != nil {
		t.Fatal(err)
	}
	catalogs, err := catalog.New(f.storage, policy)
	if err != nil {
		t.Fatal(err)
	}
	p := gateways.Principal{Subject: "controller", Username: "controller", Issuer: "https://issuer.example"}
	deleted := map[string]string{}
	for i := 0; i < 103; i++ {
		row, err := catalogs.Databases.Create(context.Background(), p, catalog.DatabaseCreate{Name: fmt.Sprintf("db-%03d", i), Provider: "deployment"})
		if err != nil {
			t.Fatal(err)
		}
		if i%17 == 0 {
			continue
		}
		if err := catalogs.Databases.Delete(context.Background(), p, row.ID); err != nil {
			t.Fatal(err)
		}
		deleted[row.ID] = row.Namespace
	}
	// Include enough deleted records to cross the 100-row replay page.
	for i := 0; i < 12; i++ {
		row, err := catalogs.Databases.Create(context.Background(), p, catalog.DatabaseCreate{Name: fmt.Sprintf("extra-%d", i), Provider: "deployment"})
		if err != nil {
			t.Fatal(err)
		}
		if err := catalogs.Databases.Delete(context.Background(), p, row.ID); err != nil {
			t.Fatal(err)
		}
		deleted[row.ID] = row.Namespace
	}
	// These IDs force a different order under the ICU test collation.
	for _, prefix := range []string{"000000a", "000000B"} {
		id := prefix + strings.Repeat("0", 20)
		ns, err := catalog.DatabaseNamespace(id)
		if err != nil {
			t.Fatal(err)
		}
		row := model.ManagedDatabase{Meta: model.Meta{ID: id}, Name: "order-" + prefix, Namespace: ns, Provider: "deployment"}
		if err := f.storage.Create(context.Background(), "ManagedDatabase", row); err != nil {
			t.Fatal(err)
		}
		if err := catalogs.Databases.Delete(context.Background(), p, id); err != nil {
			t.Fatal(err)
		}
		deleted[id] = ns
	}
	ordered, err := f.db.Query("SELECT id FROM managed_databases WHERE deleted_at IS NOT NULL ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	var expected []string
	for ordered.Next() {
		var id string
		if err := ordered.Scan(&id); err != nil {
			t.Fatal(err)
		}
		expected = append(expected, id)
	}
	if err := ordered.Err(); err != nil {
		t.Fatal(err)
	}
	ordered.Close()
	if len(expected) != len(deleted) || len(expected) <= 100 {
		t.Fatal("replay fixture does not cross a page")
	}
	if collation == "und-x-icu" && slices.IsSorted(expected) {
		t.Fatal("test collation did not change ID order")
	}
	_, config := broker(t, identity(t, "localhost"))
	key, settings := issuer(t)
	tlsIdentity := identity(t, "localhost")
	directory := filepath.Dir(tlsIdentity.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"), `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["controller"]`)
	binary := buildApplication(t)
	stop, _, address := startBoth(t, binary, f.dsn, config, settings...)
	defer stop()
	_, connection := grpcClient(t, address, tlsIdentity)
	client := pb.NewManagedDatabaseServiceClient(connection)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	replay := func(bearer, mode string) pb.ManagedDatabaseService_WatchManagedDatabasesClient {
		call := metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+bearer, "hypershell-managed-database-replay", mode))
		stream, err := client.WatchManagedDatabases(call, &pb.WatchManagedDatabasesRequest{})
		if err != nil {
			t.Fatal(err)
		}
		return stream
	}
	for _, roles := range [][]string{{}, {"gateway:creator"}, {"platform:admin"}} {
		stream := replay(token(t, key, "ordinary", roles...), "deleted-v1")
		_, err := stream.Recv()
		if status.Code(err) != codes.PermissionDenied {
			t.Fatal("replay access", roles, err)
		}
		header, _ := stream.Header()
		if len(header.Get("hypershell-managed-database-delete-tombstones")) != 0 {
			t.Fatal("denied replay confirmed the capability")
		}
	}
	invalid := replay(token(t, key, "controller"), "all")
	if _, err := invalid.Recv(); status.Code(err) != codes.InvalidArgument {
		t.Fatal("replay mode", err)
	}
	for attempt := range 2 {
		stream := replay(token(t, key, "controller"), "deleted-v1")
		header, err := stream.Header()
		if err != nil || len(header.Get("hypershell-managed-database-delete-tombstones")) != 1 || header.Get("hypershell-managed-database-delete-tombstones")[0] != "v1" {
			t.Fatal("replay capability", header, err)
		}
		var actual []string
		for {
			event, err := stream.Recv()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			id := event.GetResourceId()
			ns, ok := deleted[id]
			if !ok || event.GetType() != pb.EventType_EVENT_TYPE_DELETED || event.GetManagedDatabase().GetNamespace() != ns || event.GetManagedDatabase().GetProvider() != "deployment" {
				t.Fatal("invalid replay row", event)
			}
			actual = append(actual, id)
		}
		if !slices.Equal(actual, expected) {
			t.Fatal("replay lost, repeated, or reordered database IDs", attempt)
		}
		if attempt == 0 {
			stop()
			var restarted string
			stop, _, restarted = startBoth(t, binary, f.dsn, config, settings...)
			defer stop()
			_, connection := grpcClient(t, restarted, tlsIdentity)
			client = pb.NewManagedDatabaseServiceClient(connection)
		}
	}
	// A separate database has no deletion history. Retained rows cannot be purged.
	stop()
	emptyFixture := database(t)
	emptyStop, _, emptyAddress := startBoth(t, binary, emptyFixture.dsn, config, settings...)
	defer emptyStop()
	_, emptyConnection := grpcClient(t, emptyAddress, tlsIdentity)
	client = pb.NewManagedDatabaseServiceClient(emptyConnection)
	empty := replay(token(t, key, "controller"), "deleted-v1")
	header, err := empty.Header()
	if err != nil || !slices.Equal(header.Get("hypershell-managed-database-delete-tombstones"), []string{"v1"}) {
		t.Fatal("empty replay did not confirm its capability", err)
	}
	if _, err := empty.Recv(); err != io.EOF {
		t.Fatal("empty replay did not finish", err)
	}
}
