package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/contracts"
	"github.com/jsell-rh/hypershell-stego/internal/catalog"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	storage "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"github.com/segmentio/ksuid"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestPlacementDescriptorsMatchReference(t *testing.T) {
	reference, err := contracts.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, descriptor := range []protoreflect.FileDescriptor{pb.File_hypershell_v1_managed_clusters_proto, pb.File_hypershell_v1_gateway_releases_proto, pb.File_hypershell_v1_managed_databases_proto} {
		expected := protodesc.ToFileDescriptorProto(reference.Proto.FindFileByPath(descriptor.Path()))
		actual := protodesc.ToFileDescriptorProto(descriptor)
		actual.Options.GoPackage = nil
		expected.Options.GoPackage = nil
		actual.SourceCodeInfo = nil
		expected.SourceCodeInfo = nil
		if !proto.Equal(actual, expected) {
			t.Fatal("catalog wire contract changed", descriptor.Path())
		}
	}
}

func TestPlacementWorkflowThroughGeneratedRuntime(t *testing.T) {
	f := databaseSetup(t, false)
	_, config := broker(t, identity(t, "localhost"))
	key, settings := issuer(t)
	tlsIdentity := identity(t, "localhost")
	directory := filepath.Dir(tlsIdentity.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"), `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["controller"]`)
	binary := buildApplication(t)
	stop, address, grpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	defer func() { stop() }()
	base := address + "/api/hypershell/v1"
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	call := func(bearer string) context.Context {
		return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+bearer))
	}
	admin := token(t, key, "operator", "platform:admin")
	creator := token(t, key, "alice", "gateway:creator")
	owner := token(t, key, "alice")
	outsider := token(t, key, "mallory")
	controller := token(t, key, "controller")
	gatewayClient, connection := grpcClient(t, grpcAddress, tlsIdentity)
	clusters := pb.NewManagedClusterServiceClient(connection)
	releases := pb.NewGatewayReleaseServiceClient(connection)
	databases := pb.NewManagedDatabaseServiceClient(connection)
	clusterWatch, err := clusters.WatchManagedClusters(call(creator), &pb.WatchManagedClustersRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := clusterWatch.Header(); err != nil {
		t.Fatal(err)
	}
	releaseWatch, err := releases.WatchGatewayReleases(call(creator), &pb.WatchGatewayReleasesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := releaseWatch.Header(); err != nil {
		t.Fatal(err)
	}
	databaseWatch, err := databases.WatchManagedDatabases(call(creator), &pb.WatchManagedDatabasesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := databaseWatch.Header(); err != nil {
		t.Fatal(err)
	}

	clusterBody := []byte(`{"name":"primary-cluster","provider":"kubernetes","region":"east","kubeconfig_secret":"cluster-access","status":"ready","api_server_url":"https://cluster.example.test:6443"}`)
	code, data := requestJSON(t, "POST", base+"/managed_clusters", admin, clusterBody)
	var cluster httpapi.ManagedCluster
	if code != 201 || json.Unmarshal(data, &cluster) != nil {
		t.Fatal("cluster creation", code, string(data))
	}
	reference, err := contracts.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	checkREST := func(path string, code int, data []byte) {
		t.Helper()
		var body any
		schema := reference.OpenAPI.Paths.Value("/api/hypershell/v1/" + path).Post.Responses.Status(code).Value.Content.Get("application/json").Schema.Value
		if json.Unmarshal(data, &body) != nil || schema.VisitJSON(body) != nil {
			t.Fatal("catalog response shape", path, string(data))
		}
	}
	checkREST("managed_clusters", 201, data)
	release, err := releases.CreateGatewayRelease(call(controller), &pb.CreateGatewayReleaseRequest{Name: "stable", Image: "registry.example/gateway:v1", RolloutStrategy: proto.String("canary"), CanaryPercent: proto.Int32(10), CanaryDuration: proto.String("30m"), Status: proto.String("ready")})
	if err != nil {
		t.Fatal(err)
	}
	releaseID := release.GetGatewayRelease().GetMetadata().GetId()
	code, data = requestJSON(t, "POST", base+"/managed_databases", admin, []byte(`{"name":"shared","provider":"cnpg","region":"east","engine":"postgresql","engine_version":"18","instance_class":"small","connection_secret":"database-access","status":"ready"}`))
	var database httpapi.ManagedDatabase
	if code != 201 || json.Unmarshal(data, &database) != nil {
		t.Fatal("database creation", code, string(data))
	}
	checkREST("managed_databases", 201, data)
	wantNamespace, err := catalog.DatabaseNamespace(database.ID)
	if err != nil || database.Namespace != wantNamespace {
		t.Fatal("database namespace", database.Namespace, err)
	}
	clusterNotice, err := clusterWatch.Recv()
	if err != nil || clusterNotice.GetType() != pb.EventType_EVENT_TYPE_CREATED || clusterNotice.GetResourceId() != cluster.ID || clusterNotice.GetManagedCluster().GetKubeconfigSecret() != cluster.KubeconfigSecret {
		t.Fatal("cluster watch", clusterNotice, err)
	}
	releaseNotice, err := releaseWatch.Recv()
	if err != nil || releaseNotice.GetType() != pb.EventType_EVENT_TYPE_CREATED || releaseNotice.GetResourceId() != releaseID || releaseNotice.GetGatewayRelease().GetCanaryPercent() != 10 {
		t.Fatal("release watch", releaseNotice, err)
	}
	databaseNotice, err := databaseWatch.Recv()
	if err != nil || databaseNotice.GetType() != pb.EventType_EVENT_TYPE_CREATED || databaseNotice.GetResourceId() != database.ID || databaseNotice.GetManagedDatabase().GetNamespace() != wantNamespace {
		t.Fatal("database watch", databaseNotice, err)
	}
	for _, v := range []struct{ id, source, kind string }{{cluster.ID, "ManagedClusters", "managedcluster.created"}, {releaseID, "GatewayReleases", "gatewayrelease.created"}, {database.ID, "ManagedDatabases", "manageddatabase.created"}} {
		readCatalogEvent(t, kafkaConsumer(t, config), v.id, v.source, "Create", v.kind)
	}

	gotCluster, err := clusters.GetManagedCluster(call(creator), &pb.GetManagedClusterRequest{Id: cluster.ID})
	if err != nil || gotCluster.ManagedCluster.GetApiServerUrl() != *cluster.ApiServerUrl || !gotCluster.ManagedCluster.Metadata.CreatedAt.AsTime().Equal(cluster.CreatedAt) {
		t.Fatal("cluster cross-transport read", gotCluster, err)
	}
	gotDatabase, err := databases.GetManagedDatabase(call(creator), &pb.GetManagedDatabaseRequest{Id: database.ID})
	if err != nil || gotDatabase.ManagedDatabase.GetConnectionSecret() != *database.ConnectionSecret || gotDatabase.ManagedDatabase.GetNamespace() != database.Namespace {
		t.Fatal("database cross-transport read", gotDatabase, err)
	}
	code, data = requestJSON(t, "GET", base+"/gateway_releases/"+releaseID, creator, nil)
	var restRelease httpapi.GatewayRelease
	if code != 200 || json.Unmarshal(data, &restRelease) != nil || restRelease.Image != release.GatewayRelease.Image || restRelease.CanaryPercent == nil || *restRelease.CanaryPercent != 10 {
		t.Fatal("release cross-transport read", code, string(data))
	}
	checkREST("gateway_releases", 201, data)
	for _, path := range []string{"managed_clusters", "managed_databases", "gateway_releases"} {
		code, data = requestJSON(t, "GET", base+"/"+path+"?search="+url.QueryEscape("name = 'does-not-exist'"), creator, nil)
		var list struct {
			Total int64
			Items []json.RawMessage
		}
		if code != 200 || json.Unmarshal(data, &list) != nil || list.Total != 0 || len(list.Items) != 0 {
			t.Fatal("catalog search", path, code, string(data))
		}
		code, data = requestJSON(t, "GET", base+"/"+path+"?size=0", creator, nil)
		if code != 200 || json.Unmarshal(data, &list) != nil || list.Total != 1 || len(list.Items) != 0 {
			t.Fatal("catalog count", path, code, string(data))
		}
		for _, denied := range []string{owner, outsider} {
			if code, _ := requestJSON(t, "GET", base+"/"+path, denied, nil); code != 403 {
				t.Fatal("catalog list disclosure", path, code)
			}
		}
		if code, _ := requestJSON(t, "GET", base+"/"+path, "", nil); code != 401 {
			t.Fatal("unauthenticated catalog list", code)
		}
	}
	for _, v := range []struct {
		path, id string
		body     []byte
	}{{"managed_clusters", cluster.ID, clusterBody}, {"gateway_releases", releaseID, []byte(`{"name":"evil","image":"example/evil:v1"}`)}, {"managed_databases", database.ID, []byte(`{"name":"evil","provider":"cnpg"}`)}} {
		for _, method := range []string{"POST", "PATCH", "DELETE"} {
			target := base + "/" + v.path
			body := v.body
			if method != "POST" {
				target += "/" + v.id
			}
			if method == "DELETE" {
				body = nil
			}
			if method == "PATCH" {
				body = []byte(`{"name":"evil"}`)
			}
			if code, _ := requestJSON(t, method, target, creator, body); code != 403 {
				t.Fatal("creator catalog write", method, v.path, code)
			}
		}
		if code, _ := requestJSON(t, "GET", base+"/"+v.path+"/"+v.id, outsider, nil); code != 403 {
			t.Fatal("catalog item disclosure", code)
		}
	}
	if _, err := clusters.CreateManagedCluster(call(creator), &pb.CreateManagedClusterRequest{Name: "evil", Provider: "kubernetes", KubeconfigSecret: "evil"}); status.Code(err) != codes.PermissionDenied {
		t.Fatal("gRPC denied create", err)
	}
	if _, err := releases.UpdateGatewayRelease(call(creator), &pb.UpdateGatewayReleaseRequest{Id: releaseID, Image: proto.String("evil")}); status.Code(err) != codes.PermissionDenied {
		t.Fatal("gRPC denied update", err)
	}
	if _, err := databases.DeleteManagedDatabase(call(creator), &pb.DeleteManagedDatabaseRequest{Id: database.ID}); status.Code(err) != codes.PermissionDenied {
		t.Fatal("gRPC denied delete", err)
	}
	if _, err := databases.ListManagedDatabases(call(outsider), &pb.ListManagedDatabasesRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatal("gRPC list disclosure", err)
	}
	deniedWatch, err := databases.WatchManagedDatabases(call(outsider), &pb.WatchManagedDatabasesRequest{})
	if err == nil {
		_, err = deniedWatch.Recv()
	}
	if status.Code(err) != codes.PermissionDenied {
		t.Fatal("gRPC watch disclosure", err)
	}

	clusterUpdate, err := clusters.UpdateManagedCluster(call(admin), &pb.UpdateManagedClusterRequest{Id: cluster.ID, Region: proto.String("west")})
	if err != nil || clusterUpdate.ManagedCluster.GetRegion() != "west" {
		t.Fatal("cluster update", clusterUpdate, err)
	}
	code, data = requestJSON(t, "PATCH", base+"/gateway_releases/"+releaseID, admin, []byte(`{"canary_percent":0,"status":"ready"}`))
	if code != 200 || json.Unmarshal(data, &restRelease) != nil || restRelease.CanaryPercent == nil || *restRelease.CanaryPercent != 0 {
		t.Fatal("release zero patch", code, string(data))
	}
	dbUpdate, err := databases.UpdateManagedDatabase(call(controller), &pb.UpdateManagedDatabaseRequest{Id: database.ID, EngineVersion: proto.String("18.1")})
	if err != nil || dbUpdate.ManagedDatabase.GetEngineVersion() != "18.1" || dbUpdate.ManagedDatabase.Namespace != wantNamespace {
		t.Fatal("database update", dbUpdate, err)
	}
	if _, err := databases.UpdateManagedDatabase(call(admin), &pb.UpdateManagedDatabaseRequest{Id: database.ID, Provider: proto.String("deployment")}); status.Code(err) != codes.InvalidArgument {
		t.Fatal("database provider changed", err)
	}
	for _, v := range []struct {
		path string
		body []byte
	}{{"managed_databases", []byte(`{"name":"bad","provider":"unknown"}`)}, {"managed_databases", []byte(`{"name":"bad","provider":"cnpg","namespace":"chosen"}`)}, {"managed_clusters", []byte(`{"name":"bad","provider":"kubernetes"}`)}, {"gateway_releases", []byte(`{"name":"bad","image":"image","canary_percent":101}`)}} {
		if code, _ := requestJSON(t, "POST", base+"/"+v.path, admin, v.body); code != 400 {
			t.Fatal("invalid catalog create", v.path, code)
		}
	}
	clusterNotice, err = clusterWatch.Recv()
	if err != nil || clusterNotice.Type != pb.EventType_EVENT_TYPE_UPDATED || clusterNotice.ManagedCluster.GetRegion() != "west" {
		t.Fatal("cluster update watch", clusterNotice, err)
	}
	releaseNotice, err = releaseWatch.Recv()
	if err != nil || releaseNotice.Type != pb.EventType_EVENT_TYPE_UPDATED || releaseNotice.GatewayRelease.CanaryPercent == nil || *releaseNotice.GatewayRelease.CanaryPercent != 0 {
		t.Fatal("release update watch", releaseNotice, err)
	}
	databaseNotice, err = databaseWatch.Recv()
	if err != nil || databaseNotice.Type != pb.EventType_EVENT_TYPE_UPDATED || databaseNotice.ManagedDatabase.GetEngineVersion() != "18.1" {
		t.Fatal("database update watch", databaseNotice, err)
	}

	body, _ := json.Marshal(gateways.CreateRequest{Name: "api-placed", ClusterID: cluster.ID, ReleaseID: releaseID, DatabaseID: "ignored"})
	code, data = requestJSON(t, "POST", base+"/gateways", creator, body)
	var gateway httpapi.Gateway
	if code != 201 || json.Unmarshal(data, &gateway) != nil || gateway.DatabaseID != database.ID {
		t.Fatal("API placement", code, string(data))
	}
	gotGateway, err := gatewayClient.GetGateway(call(owner), &pb.GetGatewayRequest{Id: gateway.ID})
	if err != nil || gotGateway.Gateway.ClusterId != cluster.ID || gotGateway.Gateway.ReleaseId != releaseID || gotGateway.Gateway.DatabaseId != database.ID {
		t.Fatal("placed Gateway read", gotGateway, err)
	}
	readEvent(t, kafkaConsumer(t, config), gateway.ID)
	user := currentUser(t, base, owner)
	grants := pb.NewRoleBindingServiceClient(connection)
	bindings, err := grants.ListRoleBindings(call(owner), &pb.ListRoleBindingsRequest{UserId: &user.ID, GatewayId: &gateway.ID})
	if err != nil || len(bindings.GetItems()) != 1 || bindings.Items[0].GetRoleName() != "gateway:owner" {
		t.Fatal("placed owner grant", bindings, err)
	}
	for _, v := range []struct{ path, id string }{{"managed_clusters", cluster.ID}, {"gateway_releases", releaseID}, {"managed_databases", database.ID}} {
		if code, _ := requestJSON(t, "DELETE", base+"/"+v.path+"/"+v.id, admin, nil); code != 409 {
			t.Fatal("deleted placement in use", v.path, code)
		}
	}
	awaitQueueEmpty(t, f)
	stop()
	// An offline catalog update queues its event for the next generated process.
	policy, err := gateways.New(f.storage, gateways.Options{DatabaseProvider: gateways.ProviderCNPG})
	if err != nil {
		t.Fatal(err)
	}
	catalogService, err := catalog.New(f.storage, policy)
	if err != nil {
		t.Fatal(err)
	}
	offlineStatus := "offline-update"
	if _, err := catalogService.Releases.Update(ctx, principal("operator", "platform:admin"), releaseID, catalog.ReleasePatch{Status: &offlineStatus}); err != nil {
		t.Fatal(err)
	}
	if count(t, f.db, "stego_outbox.messages") != 1 {
		t.Fatal("offline event was not retained")
	}
	var offlineMessageID string
	if err := f.db.QueryRow(`SELECT id::text FROM stego_outbox.messages`).Scan(&offlineMessageID); err != nil {
		t.Fatal(err)
	}
	stop, address, grpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	base = address + "/api/hypershell/v1"
	gatewayClient, connection = grpcClient(t, grpcAddress, tlsIdentity)
	databases = pb.NewManagedDatabaseServiceClient(connection)
	code, data = requestJSON(t, "GET", base+"/gateway_releases/"+releaseID, creator, nil)
	if code != 200 || json.Unmarshal(data, &restRelease) != nil || restRelease.Status == nil || *restRelease.Status != offlineStatus {
		t.Fatal("catalog restart state", code, string(data))
	}
	gotGateway, err = gatewayClient.GetGateway(call(owner), &pb.GetGatewayRequest{Id: gateway.ID})
	if err != nil || gotGateway.Gateway.DatabaseId != database.ID {
		t.Fatal("Gateway restart placement", gotGateway, err)
	}
	readCatalogEvent(t, kafkaConsumer(t, config), releaseID, "GatewayReleases", "Update", "gatewayrelease.updated", offlineMessageID)
	awaitQueueEmpty(t, f)
	if code, _ := requestJSON(t, "DELETE", base+"/gateways/"+gateway.ID, owner, nil); code != 204 {
		t.Fatal("Gateway removal", code)
	}
	dbWatch, err := databases.WatchManagedDatabases(call(creator), &pb.WatchManagedDatabasesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dbWatch.Header(); err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct{ path, id string }{{"managed_clusters", cluster.ID}, {"gateway_releases", releaseID}, {"managed_databases", database.ID}} {
		if code, _ := requestJSON(t, "DELETE", base+"/"+v.path+"/"+v.id, admin, nil); code != 204 {
			t.Fatal("unused catalog removal", v.path, code)
		}
		if code, _ := requestJSON(t, "GET", base+"/"+v.path+"/"+v.id, creator, nil); code != 404 {
			t.Fatal("deleted catalog read", v.path, code)
		}
	}
	deleted, err := dbWatch.Recv()
	if err != nil || deleted.GetType() != pb.EventType_EVENT_TYPE_DELETED || deleted.GetResourceId() != database.ID || deleted.GetManagedDatabase().GetNamespace() != wantNamespace {
		t.Fatal("database deletion watch", deleted, err)
	}
	readCatalogEvent(t, kafkaConsumer(t, config), database.ID, "ManagedDatabases", "Delete", "manageddatabase.deleted")
	// Exercise the other transport direction for every catalog operation.
	clusters = pb.NewManagedClusterServiceClient(connection)
	releases = pb.NewGatewayReleaseServiceClient(connection)
	spareCluster, err := clusters.CreateManagedCluster(call(admin), &pb.CreateManagedClusterRequest{Name: "spare", Provider: "kubernetes", KubeconfigSecret: "spare-access"})
	if err != nil {
		t.Fatal(err)
	}
	spareDB, err := databases.CreateManagedDatabase(call(admin), &pb.CreateManagedDatabaseRequest{Name: "spare", Provider: "cnpg"})
	if err != nil {
		t.Fatal(err)
	}
	code, data = requestJSON(t, "POST", base+"/gateway_releases", admin, []byte(`{"name":"spare","image":"registry.example/gateway:v2"}`))
	var spareRelease httpapi.GatewayRelease
	if code != 201 || json.Unmarshal(data, &spareRelease) != nil {
		t.Fatal("REST release create", code, string(data))
	}
	for _, v := range []struct{ path, id string }{{"managed_clusters", spareCluster.ManagedCluster.Metadata.Id}, {"managed_databases", spareDB.ManagedDatabase.Metadata.Id}} {
		if code, _ := requestJSON(t, "PATCH", base+"/"+v.path+"/"+v.id, admin, []byte(`{"region":"north"}`)); code != 200 {
			t.Fatal("REST catalog patch", v.path, code)
		}
	}
	if _, err := releases.UpdateGatewayRelease(call(admin), &pb.UpdateGatewayReleaseRequest{Id: spareRelease.ID, Image: proto.String("registry.example/gateway:v3")}); err != nil {
		t.Fatal(err)
	}
	gotRelease, err := releases.GetGatewayRelease(call(creator), &pb.GetGatewayReleaseRequest{Id: spareRelease.ID})
	if err != nil || gotRelease.GatewayRelease.Image != "registry.example/gateway:v3" {
		t.Fatal("RPC release read", gotRelease, err)
	}
	clusterList, err := clusters.ListManagedClusters(call(creator), &pb.ListManagedClustersRequest{Page: 1, Size: 1})
	if err != nil || clusterList.GetMetadata().GetTotal() != 1 || len(clusterList.Items) != 1 || clusterList.Items[0].GetRegion() != "north" {
		t.Fatal("RPC cluster list", clusterList, err)
	}
	releaseList, err := releases.ListGatewayReleases(call(creator), &pb.ListGatewayReleasesRequest{})
	if err != nil || releaseList.GetMetadata().GetTotal() != 1 || len(releaseList.Items) != 1 {
		t.Fatal("RPC release list", releaseList, err)
	}
	databaseList, err := databases.ListManagedDatabases(call(creator), &pb.ListManagedDatabasesRequest{})
	if err != nil || databaseList.GetMetadata().GetTotal() != 1 || len(databaseList.Items) != 1 || databaseList.Items[0].GetRegion() != "north" {
		t.Fatal("RPC database list", databaseList, err)
	}
	if _, err := clusters.DeleteManagedCluster(call(admin), &pb.DeleteManagedClusterRequest{Id: spareCluster.ManagedCluster.Metadata.Id}); err != nil {
		t.Fatal(err)
	}
	if _, err := releases.DeleteGatewayRelease(call(admin), &pb.DeleteGatewayReleaseRequest{Id: spareRelease.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := databases.DeleteManagedDatabase(call(admin), &pb.DeleteManagedDatabaseRequest{Id: spareDB.ManagedDatabase.Metadata.Id}); err != nil {
		t.Fatal(err)
	}

}

func readCatalogEvent(t *testing.T, consumer *kgo.Client, id, source, operation, kind string, expectedID ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		for _, record := range consumer.PollRecords(ctx, 1).Records() {
			if string(record.Key) != id {
				continue
			}
			var payload map[string]string
			if json.Unmarshal(record.Value, &payload) != nil {
				t.Fatal("invalid event JSON")
			}
			if payload["event_type"] != operation {
				continue
			}
			if len(payload) != 3 || payload["source"] != source || payload["source_id"] != id {
				t.Fatal("catalog event payload", string(record.Value))
			}
			headers := map[string]string{}
			for _, h := range record.Headers {
				headers[h.Key] = string(h.Value)
			}
			if len(expectedID) > 0 && headers["stego-message-id"] != expectedID[0] {
				continue
			}
			if headers["stego-message-kind"] != kind || headers["stego-message-id"] == "" {
				t.Fatal("catalog event headers", headers)
			}
			return headers["stego-message-id"]
		}
	}
	t.Fatal("catalog event was not delivered", id, operation)
	return ""
}

func TestCatalogAtomicChangesAndMigration(t *testing.T) {
	f := database(t)
	ctx := context.Background()
	s, err := catalog.New(f.storage, f.service)
	if err != nil {
		t.Fatal(err)
	}
	p := principal("operator", "platform:admin")
	// The outbox failure must roll back create, update, and delete.
	if _, err := f.db.Exec(`CREATE FUNCTION reject_catalog_event() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected catalog event failure'; END $$; CREATE TRIGGER reject_catalog_event BEFORE INSERT ON stego_outbox.messages FOR EACH ROW EXECUTE FUNCTION reject_catalog_event()`); err != nil {
		t.Fatal(err)
	}
	before := count(t, f.db, "managed_clusters")
	if _, err := s.Clusters.Create(ctx, p, catalog.ClusterCreate{Name: "rollback", Provider: "kubernetes", KubeconfigSecret: "ref"}); err == nil {
		t.Fatal("create committed without event")
	}
	changed := "changed"
	if _, err := s.Clusters.Update(ctx, p, f.cluster, catalog.ClusterPatch{Name: &changed}); err == nil {
		t.Fatal("update committed without event")
	}
	if err := s.Clusters.Delete(ctx, p, f.cluster); err == nil {
		t.Fatal("delete committed without event")
	}
	cluster, err := s.Clusters.Get(ctx, p, f.cluster)
	if err != nil || cluster.Name != "cluster" || count(t, f.db, "managed_clusters") != before || count(t, f.db, "stego_outbox.messages") != 0 {
		t.Fatal("catalog rollback", cluster, err)
	}
	if _, err := f.db.Exec(`DROP TRIGGER reject_catalog_event ON stego_outbox.messages`); err != nil {
		t.Fatal(err)
	}
	// Recreate the old name-only schema while preserving each row and its ID.
	for _, query := range []string{`ALTER TABLE managed_clusters DROP COLUMN provider,DROP COLUMN region,DROP COLUMN kubeconfig_secret,DROP COLUMN status,DROP COLUMN api_server_url`, `ALTER TABLE gateway_releases DROP COLUMN image,DROP COLUMN rollout_strategy,DROP COLUMN canary_percent,DROP COLUMN canary_duration,DROP COLUMN status`, `ALTER TABLE managed_databases DROP COLUMN provider,DROP COLUMN namespace,DROP COLUMN region,DROP COLUMN engine,DROP COLUMN engine_version,DROP COLUMN instance_class,DROP COLUMN connection_secret,DROP COLUMN status`} {
		if _, err := f.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	migration, err := os.ReadFile("../migrations/000006_placement_catalog.sql")
	if err != nil {
		t.Fatal(err)
	}
	// Use a dedicated connection because the failed explicit transaction needs rollback.
	conn, err := f.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, string(migration)); err == nil {
		t.Fatal("upgrade invented missing placement data")
	}
	if _, err := conn.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	var columns int
	if err := f.db.QueryRow(`SELECT count(*) FROM information_schema.columns WHERE table_name='managed_clusters' AND column_name='provider'`).Scan(&columns); err != nil || columns != 0 {
		t.Fatal("failed migration changed schema", columns, err)
	}
	for _, query := range []string{`ALTER TABLE managed_clusters ADD COLUMN provider varchar(64),ADD COLUMN kubeconfig_secret varchar(253);UPDATE managed_clusters SET provider='kubernetes',kubeconfig_secret='actual-cluster-reference'`, `ALTER TABLE gateway_releases ADD COLUMN image varchar(2048);UPDATE gateway_releases SET image='registry.example/gateway:v1'`, `ALTER TABLE managed_databases ADD COLUMN provider text,ADD COLUMN namespace varchar(29);UPDATE managed_databases SET provider='cnpg'`} {
		if _, err := f.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	namespace, err := catalog.DatabaseNamespace(f.database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE managed_databases SET namespace=$1 WHERE id=$2`, namespace, f.database); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := conn.ExecContext(ctx, string(migration)); err != nil {
			t.Fatal(err)
		}
	}
	after, err := s.Clusters.Get(ctx, p, f.cluster)
	if err != nil || after.ID != cluster.ID || !after.CreatedTime.Equal(cluster.CreatedTime) || !after.UpdatedTime.Equal(cluster.UpdatedTime) || after.KubeconfigSecret != "actual-cluster-reference" {
		t.Fatal("migration changed identity or times", after, err)
	}
	if err := f.storage.Create(ctx, "ManagedDatabase", model.ManagedDatabase{Meta: model.Meta{ID: ksuid.New().String()}, Name: "bad", Provider: "unknown", Namespace: "openshell-db-0000000000000000"}); err == nil {
		t.Fatal("database provider constraint missing")
	}
	badPercent := int32(101)
	if err := f.storage.Create(ctx, "GatewayRelease", model.GatewayRelease{Meta: model.Meta{ID: ksuid.New().String()}, Name: "invalid-range", Image: "registry.example/gateway:v1", CanaryPercent: &badPercent}); err == nil {
		t.Fatal("direct storage write bypassed the numeric bound")
	}
	if _, err := s.Databases.Event(ctx, p, f.database, true); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("forged deletion accepted", err)
	}
	if _, err := catalog.DatabaseNamespace("not-a-ksuid"); !errors.Is(err, gateways.ErrInvalid) {
		t.Fatal("invalid namespace ID accepted", err)
	}
}

func BenchmarkCatalogFilteredPage(b *testing.B) {
	f := database(b)
	ctx := context.Background()
	s, err := catalog.New(f.storage, f.service)
	if err != nil {
		b.Fatal(err)
	}
	ids := make([]string, 10000)
	for i := range ids {
		ids[i] = ksuid.New().String()
	}
	if _, err := f.db.Exec(`INSERT INTO managed_clusters(id,name,provider,kubeconfig_secret,created_time,updated_time) SELECT id,'cluster-'||ordinal,'kubernetes','cluster-reference',now(),now() FROM unnest($1::text[]) WITH ORDINALITY AS rows(id,ordinal)`, ids); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		result, err := s.Clusters.List(ctx, principal("creator", "gateway:creator"), catalog.Query{Page: 1, Size: 20, Search: "provider = 'kubernetes'"})
		if err != nil || result.Total != 10001 {
			b.Fatal(fmt.Sprint(result.Total, err))
		}
	}
}

// Pause after the delete transaction reads its Gateway references. The other
// transaction can then create a Gateway before deletion tries to commit.
type catalogRaceRepository struct {
	gateways.Repository
	ready  chan struct{}
	resume chan struct{}
}
type catalogRaceTransaction struct {
	storage.Transaction
	ready  chan struct{}
	resume chan struct{}
}

func (r catalogRaceRepository) WithTransaction(ctx context.Context, fn func(context.Context, storage.Transaction) error) error {
	return r.Repository.WithTransaction(ctx, func(ctx context.Context, tx storage.Transaction) error {
		return fn(ctx, catalogRaceTransaction{tx, r.ready, r.resume})
	})
}
func (tx catalogRaceTransaction) List(ctx context.Context, entity, field, value string, q storage.ListOptions) (storage.ListResult, error) {
	result, err := tx.Transaction.List(ctx, entity, field, value, q)
	if err == nil && entity == "Gateway" {
		close(tx.ready)
		select {
		case <-tx.resume:
		case <-ctx.Done():
			return storage.ListResult{}, ctx.Err()
		}
	}
	return result, err
}
func TestCatalogDeletionCannotRaceGatewayCreation(t *testing.T) {
	for _, entity := range []string{"cluster", "release", "database"} {
		t.Run(entity, func(t *testing.T) {
			f := database(t)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			ready, resume := make(chan struct{}), make(chan struct{})
			repository := catalogRaceRepository{f.storage, ready, resume}
			service, err := catalog.New(repository, f.service)
			if err != nil {
				t.Fatal(err)
			}
			outcome := make(chan error, 1)
			go func() {
				p := principal("operator", "platform:admin")
				switch entity {
				case "cluster":
					outcome <- service.Clusters.Delete(ctx, p, f.cluster)
				case "release":
					outcome <- service.Releases.Delete(ctx, p, f.release)
				case "database":
					outcome <- service.Databases.Delete(ctx, p, f.database)
				}
			}()
			select {
			case <-ready:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			gateway, createErr := f.service.Create(ctx, principal("alice", "gateway:creator"), f.request("concurrent-placement"))
			close(resume)
			deleteErr := <-outcome
			if createErr != nil || !errors.Is(deleteErr, storage.ErrSerialization) {
				t.Fatal("concurrent placement result", createErr, deleteErr)
			}
			if _, err := f.service.Get(ctx, principal("alice"), gateway.ID); err != nil {
				t.Fatal(err)
			}
			for kind, id := range map[string]string{"ManagedCluster": f.cluster, "GatewayRelease": f.release, "ManagedDatabase": f.database} {
				if _, err := f.storage.Get(ctx, kind, id); err != nil {
					t.Fatal("Gateway has a deleted placement", kind, err)
				}
			}
		})
	}
}
