package acceptance

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"github.com/segmentio/ksuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestSandboxCountGrantsAcrossClustersAndRestart(t *testing.T) {
	f := database(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if _, err := f.db.Exec(`CREATE TABLE count_scope_events(kind text NOT NULL);
CREATE FUNCTION public.audit_count_scope() RETURNS trigger LANGUAGE plpgsql
SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
BEGIN INSERT INTO public.count_scope_events VALUES (NEW.kind); RETURN NEW; END $$;
REVOKE ALL ON FUNCTION public.audit_count_scope() FROM PUBLIC;
CREATE TRIGGER audit_count_scope AFTER INSERT ON stego_outbox.messages
FOR EACH ROW WHEN (NEW.kind LIKE 'gateway.%') EXECUTE FUNCTION audit_count_scope()`); err != nil {
		t.Fatal(err)
	}
	requireFixtureRuntimePermissionDenied(t, f, "INSERT INTO public.count_scope_events(kind) VALUES ('direct-runtime-write')")
	second := ksuid.New().String()
	if err := f.storage.Create(ctx, "ManagedCluster", model.ManagedCluster{Meta: model.Meta{ID: second}, Name: "second", Provider: "kubernetes", KubeconfigSecret: "unused"}); err != nil {
		t.Fatal(err)
	}
	_, config := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, config)
	key, settings := issuer(t)
	tlsIdentity := identity(t, "localhost")
	directory := filepath.Dir(tlsIdentity.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"), `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["first","second","workload","identity","cleanup","ungranted"]`)
	settings = withCleanupGrants(t, settings, cleanupGrant("cleanup", "Gateway", "workload", f.cluster))
	settings = withControllerWriteGrants(t, settings, writeGrant("first", "observe.sandbox-count", f.cluster), writeGrant("second", "observe.sandbox-count", second), writeGrant("workload", "observe.workload", f.cluster), writeGrant("identity", "configure.identity", ""), writeGrant("ordinary", "observe.sandbox-count", f.cluster))
	binary := buildApplication(t)
	stop, _, grpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	defer func() { stop() }()
	client, connection := grpcClient(t, grpcAddress, tlsIdentity)
	observed := control.NewGatewayIdentityServiceClient(connection)
	tokens := map[string]string{}
	for _, subject := range []string{"first", "second", "workload", "identity", "cleanup", "ungranted", "ordinary", "admin"} {
		tokens[subject] = token(t, key, subject, "platform:admin")
	}
	tokens["owner"] = token(t, key, "owner", "gateway:creator")
	call := func(subject string) context.Context {
		return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+tokens[subject]))
	}
	rows := map[string]*pb.Gateway{}
	for subject, cluster := range map[string]string{"first": f.cluster, "second": second} {
		created, err := client.CreateGateway(call("owner"), &pb.CreateGatewayRequest{Name: subject, ClusterId: cluster, ReleaseId: f.release})
		if err != nil {
			t.Fatal(err)
		}
		rows[subject] = created.Gateway
		readGatewayEvent(t, consumer, created.Gateway.Metadata.Id, "Create", "gateway.created")
	}
	awaitQueueEmpty(t, f)
	state := func(id string) model.Gateway {
		t.Helper()
		value, err := f.storage.Get(ctx, "Gateway", id)
		if err != nil {
			t.Fatal(err)
		}
		return value.(model.Gateway)
	}
	write := func(subject, target, method string, want codes.Code) {
		t.Helper()
		row := rows[target]
		before := state(row.Metadata.Id)
		events := count(t, f.db, "count_scope_events")
		var err error
		switch method {
		case "adjust":
			_, err = client.AdjustActiveSandboxCount(call(subject), &pb.AdjustActiveSandboxCountRequest{Namespace: row.Namespace, Delta: 1})
		case "set":
			_, err = client.SetActiveSandboxCount(call(subject), &pb.SetActiveSandboxCountRequest{Namespace: row.Namespace, Count: 3})
		case "observed":
			_, err = observed.SetObservedSandboxCount(call(subject), &control.SetObservedSandboxCountRequest{Namespace: row.Namespace, ClusterId: row.ClusterId, Count: 5})
		default:
			t.Fatal("unknown count method")
		}
		if status.Code(err) != want {
			t.Fatalf("sandbox count grant: %s %s to %s: got %v, want %v", subject, method, target, err, want)
		}
		after := state(row.Metadata.Id)
		if want != codes.OK {
			if !reflect.DeepEqual(before, after) || count(t, f.db, "count_scope_events") != events {
				t.Fatal("denied count changed state or events")
			}
			return
		}
		if after.ResourceVersion != before.ResourceVersion+1 || count(t, f.db, "count_scope_events") != events+1 {
			t.Fatal("count did not commit its revision and event together")
		}
		readGatewayEvent(t, consumer, row.Metadata.Id, "Update", "gateway.updated")
		awaitQueueEmpty(t, f)
	}
	for _, method := range []string{"adjust", "set", "observed"} {
		for _, subject := range []string{"second", "workload", "identity", "cleanup", "ungranted", "ordinary", "admin", "owner"} {
			write(subject, "first", method, codes.PermissionDenied)
		}
		write("first", "second", method, codes.PermissionDenied)
		write("first", "first", method, codes.OK)
		write("second", "second", method, codes.OK)
	}
	connection.Close()
	stop()
	settings = withControllerWriteGrants(t, settings, writeGrant("second", "observe.sandbox-count", second))
	stop, _, grpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	client, connection = grpcClient(t, grpcAddress, tlsIdentity)
	observed = control.NewGatewayIdentityServiceClient(connection)
	for _, method := range []string{"adjust", "set", "observed"} {
		write("first", "first", method, codes.PermissionDenied)
		write("first", "second", method, codes.PermissionDenied)
		write("second", "first", method, codes.PermissionDenied)
		write("second", "second", method, codes.OK)
	}
}
