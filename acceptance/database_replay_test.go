package acceptance

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/catalog"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestDatabaseDeleteReplayThroughGeneratedRuntime(t *testing.T) {
	f := databaseSetup(t, false)
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
	_, config := broker(t, identity(t, "localhost"))
	key, settings := issuer(t)
	tlsIdentity := identity(t, "localhost")
	directory := filepath.Dir(tlsIdentity.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"), `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["controller"]`)
	stop, _, address := startBoth(t, buildApplication(t), f.dsn, config, settings...)
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
	}
	invalid := replay(token(t, key, "controller"), "all")
	if _, err := invalid.Recv(); status.Code(err) != codes.InvalidArgument {
		t.Fatal("replay mode", err)
	}
	stream := replay(token(t, key, "controller"), "deleted-v1")
	header, err := stream.Header()
	if err != nil || len(header.Get("hypershell-managed-database-delete-tombstones")) != 1 || header.Get("hypershell-managed-database-delete-tombstones")[0] != "v1" {
		t.Fatal("replay capability", header, err)
	}
	after := ""
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
		if !ok || id <= after || event.GetType() != pb.EventType_EVENT_TYPE_DELETED || event.GetManagedDatabase().GetNamespace() != ns || event.GetManagedDatabase().GetProvider() != "deployment" {
			t.Fatal("invalid replay row", event)
		}
		delete(deleted, id)
		after = id
	}
	if len(deleted) != 0 {
		t.Fatal("missing replay rows", len(deleted))
	}
}
