package acceptance

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gatewayworkload"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"github.com/segmentio/ksuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestGatewayRecoveryIDsThroughGeneratedRuntime(t *testing.T) {
	f := database(t)
	ids := make([]string, 205)
	for i := range ids {
		ids[i] = ksuid.New().String()
		ns, err := gatewayworkload.Namespace(ids[i])
		if err != nil {
			t.Fatal(err)
		}
		row := model.Gateway{Meta: model.Meta{ID: ids[i]}, Name: fmt.Sprintf("recovery-%d", i), Namespace: ns, ClusterID: f.cluster, ReleaseID: f.release, DatabaseID: f.database}
		if err := f.storage.Create(context.Background(), "Gateway", row); err != nil {
			t.Fatal(err)
		}
		if i%2 == 0 {
			if _, err := f.db.Exec("UPDATE gateways SET deleted_at=now() WHERE id=$1", ids[i]); err != nil {
				t.Fatal(err)
			}
		}
	}
	ordered, err := f.db.Query("SELECT id FROM gateways ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	ids = nil
	for ordered.Next() {
		var id string
		if err := ordered.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := ordered.Err(); err != nil {
		t.Fatal(err)
	}
	ordered.Close()
	_, config := broker(t, identity(t, "localhost"))
	key, settings := issuer(t)
	apiTLS := identity(t, "localhost")
	directory := filepath.Dir(apiTLS.config.CAFile)
	settings = append(settings, `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["controller"]`, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"))
	stop, _, address := startBoth(t, buildApplication(t), f.dsn, config, settings...)
	defer stop()
	_, connection := grpcClient(t, address, apiTLS)
	client := control.NewGatewayIdentityServiceClient(connection)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	call := func(subject string, roles ...string) context.Context {
		return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+token(t, key, subject, roles...)))
	}
	for _, denied := range []context.Context{ctx, call("creator", "gateway:creator"), call("administrator", "platform:admin")} {
		_, err := client.ListGatewayReconcileIDs(denied, &control.ListGatewayReconcileIDsRequest{})
		if status.Code(err) != codes.PermissionDenied && status.Code(err) != codes.Unauthenticated {
			t.Fatal("untrusted caller read recovery IDs", err)
		}
	}
	controller := call("controller")
	for _, cursor := range []string{"not-an-id", "' OR true", "000000000000000000000000000", ids[0] + " "} {
		if _, err := client.ListGatewayReconcileIDs(controller, &control.ListGatewayReconcileIDsRequest{AfterId: cursor}); status.Code(err) != codes.InvalidArgument {
			t.Fatal("invalid recovery cursor was accepted", err)
		}
	}
	var actual []string
	after := ""
	for page := 0; page < 3; page++ {
		response, err := client.ListGatewayReconcileIDs(controller, &control.ListGatewayReconcileIDsRequest{AfterId: after})
		if err != nil {
			t.Fatal(err)
		}
		want := 100
		if page == 2 {
			want = 5
		}
		if len(response.Ids) != want {
			t.Fatalf("page %d: got %d IDs, want %d", page, len(response.Ids), want)
		}
		actual = append(actual, response.Ids...)
		after = response.Ids[len(response.Ids)-1]
		// A deletion after the first page must not shift the next page.
		if page == 0 {
			if _, err := f.db.Exec("UPDATE gateways SET deleted_at=now() WHERE id=$1", ids[101]); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !slices.Equal(actual, ids) {
		t.Fatal("recovery skipped, repeated, or reordered retained IDs")
	}
	end, err := client.ListGatewayReconcileIDs(controller, &control.ListGatewayReconcileIDsRequest{AfterId: after})
	if err != nil || len(end.GetIds()) != 0 {
		t.Fatal("recovery did not reach the end", err)
	}
}
