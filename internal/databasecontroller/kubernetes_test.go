package databasecontroller

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"github.com/segmentio/ksuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func testKubernetes(t *testing.T, handler http.HandlerFunc) (*Kubernetes, string) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	dir := t.TempDir()
	ca, file := filepath.Join(dir, "ca"), filepath.Join(dir, "token")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("first-token"), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := NewKubernetes(KubernetesOptions{ServerURL: server.URL, CAFile: ca, TokenFile: file, ClusterIssuer: "issuer"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client, file
}
func TestCleanupUsesNamespaceIdentityAndRetainsFailures(t *testing.T) {
	id := ksuid.New().String()
	ns, _ := gateways.DatabaseNamespace(id)
	db := &pb.ManagedDatabase{Metadata: &pb.ObjectReference{Id: id}, Namespace: ns, Provider: "deployment"}
	phase, deletes := 0, 0
	k, _ := testKubernetes(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/namespaces/"+ns {
			t.Error("wrong namespace path")
			w.WriteHeader(500)
			return
		}
		if r.Method == "GET" {
			if phase == 2 {
				w.WriteHeader(404)
				return
			}
			owner := id
			if phase == 3 {
				owner = "foreign"
			}
			_ = json.NewEncoder(w).Encode(object{"metadata": object{"uid": "namespace-uid", "resourceVersion": "17", "labels": labels(owner)}})
			return
		}
		deletes++
		var options object
		if json.NewDecoder(r.Body).Decode(&options) != nil || str(options, "preconditions", "uid") != "namespace-uid" || str(options, "preconditions", "resourceVersion") != "17" {
			t.Error("delete did not preserve namespace identity")
		}
		if phase == 0 {
			w.WriteHeader(409)
			return
		}
		w.WriteHeader(403)
	})
	if err := k.Delete(context.Background(), db); err == nil || errors.Is(err, ErrPending) {
		t.Fatal("namespace conflict was lost", err)
	}
	phase = 1
	if err := k.Delete(context.Background(), db); err == nil {
		t.Fatal("namespace denial was lost")
	}
	phase = 2
	if err := k.Delete(context.Background(), db); err != nil {
		t.Fatal("absent namespace cleanup", err)
	}
	phase = 3
	if err := k.Delete(context.Background(), db); err == nil {
		t.Fatal("foreign namespace cleanup succeeded")
	}
	if deletes != 2 {
		t.Fatal("foreign or absent namespace received DELETE", deletes)
	}
	db.Namespace = "kube-system"
	if err := k.Delete(context.Background(), db); err == nil {
		t.Fatal("namespace mismatch was accepted")
	}
}
func TestMissingCredentialsCannotReplaceAnExistingVolumePassword(t *testing.T) {
	requests := 0
	k, _ := testKubernetes(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != "GET" {
			t.Error("credentials were written for an existing volume")
			w.WriteHeader(500)
			return
		}
		switch r.URL.Path {
		case "/api/v1/namespaces/example/secrets/" + CredentialsName:
			w.WriteHeader(404)
		case "/api/v1/namespaces/example/persistentvolumeclaims/" + WorkloadName + "-data":
			_ = json.NewEncoder(w).Encode(object{"metadata": object{"uid": "existing-volume"}})
		default:
			t.Error("unexpected credential operation")
			w.WriteHeader(500)
		}
	})
	if err := k.credentials(context.Background(), "/api/v1/namespaces/example", "id", "example", "ca"); err == nil {
		t.Fatal("missing volume password was replaced")
	}
	if requests != 2 {
		t.Fatal("unexpected credential requests", requests)
	}
}
func TestKubernetesTokenRotationAndMissingMutationResource(t *testing.T) {
	expected := "first-token"
	k, file := testKubernetes(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+expected {
			t.Error("old Kubernetes token was used")
		}
		w.WriteHeader(404)
	})
	if _, code, err := k.request(context.Background(), "GET", "/api/v1/namespaces/missing", nil); err != nil || code != 404 {
		t.Fatal(err)
	}
	expected = "second-token"
	if err := os.WriteFile(file, []byte(expected), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := k.request(context.Background(), "PATCH", "/api/v1/namespaces/missing", object{}); err == nil {
		t.Fatal("missing mutation resource was accepted")
	}
	if err := os.Chmod(file, 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := k.request(context.Background(), "GET", "/api/v1/namespaces/missing", nil); err == nil {
		t.Fatal("public token file was read")
	}
}

type headerStream struct {
	grpc.ClientStream
	header metadata.MD
}

func (s *headerStream) Header() (metadata.MD, error) { return s.header, nil }
func (s *headerStream) Recv() (*pb.WatchManagedDatabasesResponse, error) {
	return nil, io.EOF
}
func TestDatabaseWatchRequiresOneKnownCapability(t *testing.T) {
	for _, md := range []metadata.MD{nil, metadata.Pairs(capability, "v2"), metadata.Pairs(capability, "v1", capability, "v1")} {
		if err := checkHeader(&headerStream{header: md}); err == nil {
			t.Fatal("unsupported database watch accepted", md)
		}
	}
	if err := checkHeader(&headerStream{header: metadata.Pairs(capability, "v1")}); err != nil {
		t.Fatal(err)
	}
}

type staleAPI struct {
	pb.ManagedDatabaseServiceClient
	control.DatabaseCleanupServiceClient
}

func (staleAPI) GetManagedDatabase(context.Context, *pb.GetManagedDatabaseRequest, ...grpc.CallOption) (*pb.GetManagedDatabaseResponse, error) {
	return nil, status.Error(codes.NotFound, "deleted")
}

type rejectProvider struct{ calls int }

func (p *rejectProvider) Ensure(context.Context, *pb.ManagedDatabase) error { p.calls++; return nil }
func (p *rejectProvider) Delete(context.Context, *pb.ManagedDatabase) error { p.calls++; return nil }
func reconcileHint(ctx context.Context, c *Controller, event *pb.WatchManagedDatabasesResponse) error {
	id, err := eventKey(event)
	if err != nil {
		return err
	}
	return c.reconcile(ctx, id)
}
func TestStaleCreateCannotRecreateDeletedDatabase(t *testing.T) {
	p := &rejectProvider{}
	c, _ := New(staleAPI{}, staleAPI{}, testClusterID, p)
	event := &pb.WatchManagedDatabasesResponse{ResourceId: "old", Type: pb.EventType_EVENT_TYPE_CREATED, ManagedDatabase: &pb.ManagedDatabase{Metadata: &pb.ObjectReference{Id: "old"}, Provider: "deployment"}}
	if err := reconcileHint(context.Background(), c, event); status.Code(err) != codes.NotFound {
		t.Fatal("missing retained state did not remain an error", err)
	}
	if p.calls != 0 {
		t.Fatal("stale live event changed Kubernetes")
	}
	event.ManagedDatabase.Metadata.Id = "different"
	if err := reconcileHint(context.Background(), c, event); err == nil {
		t.Fatal("event with a different ID was accepted")
	}
}

const testClusterID = "000000000000000000000000001"

type stateAPI struct {
	pb.ManagedDatabaseServiceClient
	control.DatabaseCleanupServiceClient
	cleanupComplete bool
	cleanupCalls    []bool
	cleanupError    error
	db              *pb.ManagedDatabase
	updateError     error
	updates         []string
	version         int64
	headerOverride  metadata.MD
	deleted         bool
	readError       error
	reads           int
}

func (a *stateAPI) GetManagedDatabase(ctx context.Context, _ *pb.GetManagedDatabaseRequest, options ...grpc.CallOption) (*pb.GetManagedDatabaseResponse, error) {
	a.reads++
	md, _ := metadata.FromOutgoingContext(ctx)
	if !slices.Equal(md.Get("resource-read-mode"), []string{"retained-v1"}) {
		return nil, status.Error(codes.InvalidArgument, "retained read required")
	}
	if a.readError != nil {
		return nil, a.readError
	}
	if a.version == 0 {
		a.version = 1
	}
	header := metadata.Pairs("resource-version", strconv.FormatInt(a.version, 10), "resource-deleted", strconv.FormatBool(a.deleted), "resource-cleanup", `{"provider":`+strconv.FormatBool(a.cleanupComplete)+`}`)
	header.Set("hypershell-database-placement", "cluster-v1")
	if a.db.GetProvider() == "deployment" {
		header.Set("hypershell-database-cluster-id", testClusterID)
	}
	if a.headerOverride != nil {
		header = a.headerOverride
	}
	for _, option := range options {
		if h, ok := option.(grpc.HeaderCallOption); ok && h.HeaderAddr != nil {
			*h.HeaderAddr = header.Copy()
		}
	}
	return &pb.GetManagedDatabaseResponse{ManagedDatabase: proto.Clone(a.db).(*pb.ManagedDatabase)}, nil
}
func (a *stateAPI) UpdateManagedDatabase(ctx context.Context, r *pb.UpdateManagedDatabaseRequest, _ ...grpc.CallOption) (*pb.UpdateManagedDatabaseResponse, error) {
	md, _ := metadata.FromOutgoingContext(ctx)
	versions := md.Get("if-resource-version")
	if len(versions) != 1 || versions[0] != strconv.FormatInt(a.version, 10) {
		return nil, status.Error(codes.Aborted, "resource changed")
	}
	a.updates = append(a.updates, r.GetStatus())
	if a.updateError != nil {
		return nil, a.updateError
	}
	a.db.Status = r.Status
	if r.ConnectionSecret != nil {
		a.db.ConnectionSecret = r.ConnectionSecret
	}
	a.version++
	return &pb.UpdateManagedDatabaseResponse{ManagedDatabase: a.db}, nil
}

type pendingProvider struct{}

func (pendingProvider) Ensure(context.Context, *pb.ManagedDatabase) error { return ErrPending }
func (pendingProvider) Delete(context.Context, *pb.ManagedDatabase) error { return ErrPending }
func TestReadinessLossAndFailedStatusWritesRemainVisible(t *testing.T) {
	ready := "ready"
	db := &pb.ManagedDatabase{Metadata: &pb.ObjectReference{Id: "database"}, Provider: "deployment", Status: &ready}
	failure := errors.New("status update failed")
	api := &stateAPI{db: db, updateError: failure}
	c, _ := New(api, api, testClusterID, pendingProvider{})
	event := &pb.WatchManagedDatabasesResponse{ResourceId: "database", Type: pb.EventType_EVENT_TYPE_UPDATED, ManagedDatabase: db}
	if err := reconcileHint(context.Background(), c, event); !errors.Is(err, failure) {
		t.Fatal("status update failure was lost", err)
	}
	api.updateError = nil
	if err := reconcileHint(context.Background(), c, event); !errors.Is(err, ErrPending) {
		t.Fatal("pending workload was marked ready", err)
	}
	if db.GetStatus() != "provisioning" || len(api.updates) != 2 {
		t.Fatal("readiness loss was not published", api.updates)
	}
	if err := reconcileHint(context.Background(), c, event); !errors.Is(err, ErrPending) {
		t.Fatal(err)
	}
	if len(api.updates) != 2 {
		t.Fatal("unchanged status caused another write")
	}
}

func TestUnsupportedDatabaseOptionsCannotProvisionDifferentResources(t *testing.T) {
	id := ksuid.New().String()
	ns, _ := gateways.DatabaseNamespace(id)
	db := &pb.ManagedDatabase{Metadata: &pb.ObjectReference{Id: id}, Namespace: ns, Provider: "deployment"}
	k, _ := testKubernetes(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("unsupported placement reached Kubernetes")
		w.WriteHeader(500)
	})
	for _, field := range []**string{&db.Engine, &db.EngineVersion, &db.Region, &db.InstanceClass, &db.ConnectionSecret} {
		value := "unsupported"
		*field = &value
		if err := k.Ensure(context.Background(), db); err == nil {
			t.Fatal("unsupported options provisioned a database")
		}
		if err := validatePlacement(db); err != nil {
			t.Fatal("mutable options prevented cleanup", err)
		}
		*field = nil
	}
	if err := validateDatabase(db); err != nil {
		t.Fatal("default placement", err)
	}
}

func TestMissingDatabaseRevisionStopsProviderWork(t *testing.T) {
	for _, header := range []metadata.MD{{}, {"resource-version": []string{"0"}}, {"resource-version": []string{"1", "1"}}} {
		db := &pb.ManagedDatabase{Metadata: &pb.ObjectReference{Id: "database"}, Provider: "deployment"}
		api := &stateAPI{db: db, headerOverride: header}
		provider := &rejectProvider{}
		c, _ := New(api, api, testClusterID, provider)
		event := &pb.WatchManagedDatabasesResponse{ResourceId: "database", Type: pb.EventType_EVENT_TYPE_UPDATED, ManagedDatabase: db}
		if err := reconcileHint(context.Background(), c, event); err == nil {
			t.Fatal("missing revision accepted")
		}
		if provider.calls != 0 || len(api.updates) != 0 {
			t.Fatal("invalid revision reached external work")
		}
	}
}

type changeDuringEnsure struct {
	calls  int
	change func()
}

func (p *changeDuringEnsure) Ensure(context.Context, *pb.ManagedDatabase) error {
	p.calls++
	if p.change != nil {
		p.change()
		p.change = nil
	}
	return nil
}
func (*changeDuringEnsure) Delete(context.Context, *pb.ManagedDatabase) error { return nil }
func TestDatabaseConflictRequiresAnotherProviderObservation(t *testing.T) {
	db := &pb.ManagedDatabase{Metadata: &pb.ObjectReference{Id: "database"}, Provider: "deployment"}
	api := &stateAPI{db: db}
	provider := &changeDuringEnsure{change: func() { api.version++ }}
	c, _ := New(api, api, testClusterID, provider)
	event := &pb.WatchManagedDatabasesResponse{ResourceId: "database", Type: pb.EventType_EVENT_TYPE_UPDATED, ManagedDatabase: db}
	if err := reconcileHint(context.Background(), c, event); status.Code(err) != codes.Aborted {
		t.Fatal("old observation did not fail", err)
	}
	if provider.calls != 1 || len(api.updates) != 0 || api.db.GetStatus() == "ready" {
		t.Fatal("conflict published or retried the old result")
	}
	if err := reconcileHint(context.Background(), c, event); err != nil {
		t.Fatal(err)
	}
	if provider.calls != 2 || len(api.updates) != 1 || api.db.GetStatus() != "ready" {
		t.Fatal("fresh observation was not committed")
	}
}

func TestLiveDatabaseEventUsesCurrentProvider(t *testing.T) {
	for _, test := range []struct {
		hint, current string
		calls         int
	}{{"cnpg", "deployment", 1}, {"deployment", "cnpg", 0}} {
		row := &pb.ManagedDatabase{Metadata: &pb.ObjectReference{Id: "database"}, Provider: test.current}
		api := &stateAPI{db: row}
		provider := &rejectProvider{}
		c, _ := New(api, api, testClusterID, provider)
		hint := proto.Clone(row).(*pb.ManagedDatabase)
		hint.Provider = test.hint
		event := &pb.WatchManagedDatabasesResponse{ResourceId: "database", Type: pb.EventType_EVENT_TYPE_UPDATED, ManagedDatabase: hint}
		if err := reconcileHint(context.Background(), c, event); err != nil {
			t.Fatal(err)
		}
		if provider.calls != test.calls {
			t.Fatal("event provider replaced current state", test, provider.calls)
		}
	}
}

func TestDatabaseDeleteHintCannotDeleteCurrentLiveState(t *testing.T) {
	row := &pb.ManagedDatabase{Metadata: &pb.ObjectReference{Id: "database"}, Provider: "deployment"}
	expected := proto.Clone(row)
	api := &stateAPI{db: row}
	provider := &recordingProvider{}
	c, _ := New(api, api, testClusterID, provider)
	event := &pb.WatchManagedDatabasesResponse{ResourceId: "database", Type: pb.EventType_EVENT_TYPE_DELETED, ManagedDatabase: row}
	if err := reconcileHint(context.Background(), c, event); err != nil {
		t.Fatal(err)
	}
	if api.reads != 1 || len(provider.deleted) != 0 || len(provider.ensured) != 1 || !proto.Equal(provider.ensured[0], expected) {
		t.Fatal("delete hint replaced current live intent")
	}
}

type recordingProvider struct {
	ensured, deleted []*pb.ManagedDatabase
	failure          error
}

func (p *recordingProvider) Ensure(_ context.Context, row *pb.ManagedDatabase) error {
	p.ensured = append(p.ensured, proto.Clone(row).(*pb.ManagedDatabase))
	return p.failure
}
func (p *recordingProvider) Delete(_ context.Context, row *pb.ManagedDatabase) error {
	p.deleted = append(p.deleted, proto.Clone(row).(*pb.ManagedDatabase))
	return p.failure
}

func TestDatabaseDeleteUsesCurrentRetainedRecord(t *testing.T) {
	for _, kind := range []pb.EventType{pb.EventType_EVENT_TYPE_DELETED, pb.EventType_EVENT_TYPE_CREATED, pb.EventType_EVENT_TYPE_UPDATED} {
		current := &pb.ManagedDatabase{Metadata: &pb.ObjectReference{Id: "database"}, Provider: "deployment", Namespace: "current"}
		api := &stateAPI{db: current, deleted: true, version: 7}
		provider := &recordingProvider{}
		c, _ := New(api, api, testClusterID, provider)
		hint := proto.Clone(current).(*pb.ManagedDatabase)
		hint.Namespace = "old"
		hint.Provider = "cnpg"
		event := &pb.WatchManagedDatabasesResponse{ResourceId: "database", Type: kind, ManagedDatabase: hint}
		if err := reconcileHint(context.Background(), c, event); err != nil {
			t.Fatal(err)
		}
		if api.reads != 1 || len(api.updates) != 0 || len(provider.ensured) != 0 || len(provider.deleted) != 1 || !proto.Equal(provider.deleted[0], current) {
			t.Fatal("deletion did not use current retained state", kind)
		}
		provider.failure = ErrPending
		if err := reconcileHint(context.Background(), c, event); !errors.Is(err, ErrPending) {
			t.Fatal("cleanup failure lost", err)
		}
		if api.reads != 2 || len(provider.deleted) != 2 {
			t.Fatal("retry did not read retained state again")
		}
	}
}

func TestDatabaseDeleteStopsWithoutAuthoritativeEvidence(t *testing.T) {
	for _, test := range []struct {
		name      string
		readError error
		header    metadata.MD
		id        string
	}{
		{name: "missing", readError: status.Error(codes.NotFound, "missing")},
		{name: "denied", readError: status.Error(codes.PermissionDenied, "denied")},
		{name: "unavailable", readError: status.Error(codes.Unavailable, "offline")},
		{name: "timeout", readError: context.DeadlineExceeded},
		{name: "old server", header: metadata.Pairs("resource-version", "1")},
		{name: "invalid state", header: metadata.Pairs("resource-version", "1", "resource-deleted", "TRUE")},
		{name: "duplicate state", header: metadata.Pairs("resource-version", "1", "resource-deleted", "true", "resource-deleted", "true")},
		{name: "missing revision", header: metadata.Pairs("resource-deleted", "true")},
		{name: "wrong resource", id: "different"},
	} {
		t.Run(test.name, func(t *testing.T) {
			id := test.id
			if id == "" {
				id = "database"
			}
			current := &pb.ManagedDatabase{Metadata: &pb.ObjectReference{Id: id}, Provider: "deployment"}
			api := &stateAPI{db: current, deleted: true, readError: test.readError, headerOverride: test.header}
			provider := &recordingProvider{}
			c, _ := New(api, api, testClusterID, provider)
			hint := &pb.ManagedDatabase{Metadata: &pb.ObjectReference{Id: "database"}, Provider: "deployment"}
			event := &pb.WatchManagedDatabasesResponse{ResourceId: "database", Type: pb.EventType_EVENT_TYPE_DELETED, ManagedDatabase: hint}
			if err := reconcileHint(context.Background(), c, event); err == nil {
				t.Fatal("deletion accepted without evidence")
			}
			if len(provider.deleted) != 0 || len(provider.ensured) != 0 || len(api.updates) != 0 {
				t.Fatal("failed read reached provider or write")
			}
		})
	}
}

func (a *stateAPI) ObserveDatabaseCleanup(ctx context.Context, request *control.ObserveDatabaseCleanupRequest, _ ...grpc.CallOption) (*control.ObserveDatabaseCleanupResponse, error) {
	md, _ := metadata.FromOutgoingContext(ctx)
	if !a.deleted || !slices.Equal(md.Get("if-resource-version"), []string{strconv.FormatInt(a.version, 10)}) {
		return nil, status.Error(codes.Aborted, "resource changed")
	}
	if request.Id != a.db.GetMetadata().GetId() || request.Owner != "provider" {
		return nil, status.Error(codes.PermissionDenied, "wrong cleanup owner")
	}
	if a.cleanupError != nil {
		return nil, a.cleanupError
	}
	a.cleanupComplete = request.Complete
	a.cleanupCalls = append(a.cleanupCalls, request.Complete)
	a.version++
	return &control.ObserveDatabaseCleanupResponse{}, nil
}

func TestCompletedDatabaseCleanupStillChecksForLateEffects(t *testing.T) {
	row := &pb.ManagedDatabase{Metadata: &pb.ObjectReference{Id: "database"}, Provider: "deployment"}
	api := &stateAPI{db: row, deleted: true, version: 4, cleanupComplete: true}
	provider := &recordingProvider{}
	c, _ := New(api, api, testClusterID, provider)
	event := &pb.WatchManagedDatabasesResponse{ResourceId: "database", Type: pb.EventType_EVENT_TYPE_DELETED, ManagedDatabase: row}
	if err := reconcileHint(context.Background(), c, event); err != nil {
		t.Fatal(err)
	}
	if len(provider.deleted) != 1 || len(api.cleanupCalls) != 0 {
		t.Fatal("completed cleanup skipped observation or rewrote stable state")
	}
	provider.failure = ErrPending
	if err := reconcileHint(context.Background(), c, event); !errors.Is(err, ErrPending) {
		t.Fatal(err)
	}
	if api.cleanupComplete || !slices.Equal(api.cleanupCalls, []bool{false}) {
		t.Fatal("late effects kept cleanup complete")
	}
	provider.failure = nil
	if err := reconcileHint(context.Background(), c, event); err != nil {
		t.Fatal(err)
	}
	if !api.cleanupComplete || !slices.Equal(api.cleanupCalls, []bool{false, true}) || len(provider.deleted) != 3 {
		t.Fatal("fresh absence was not confirmed")
	}
}

func TestDatabaseCleanupConflictRequiresFreshProviderWork(t *testing.T) {
	row := &pb.ManagedDatabase{Metadata: &pb.ObjectReference{Id: "database"}, Provider: "deployment"}
	api := &stateAPI{db: row, deleted: true, version: 2, cleanupError: status.Error(codes.Aborted, "resource changed")}
	provider := &recordingProvider{}
	c, _ := New(api, api, testClusterID, provider)
	event := &pb.WatchManagedDatabasesResponse{ResourceId: "database", Type: pb.EventType_EVENT_TYPE_DELETED, ManagedDatabase: row}
	if err := reconcileHint(context.Background(), c, event); status.Code(err) != codes.Aborted {
		t.Fatal(err)
	}
	if api.cleanupComplete || len(provider.deleted) != 1 || api.reads != 1 {
		t.Fatal("old cleanup result was retried")
	}
	api.cleanupError = nil
	api.version++
	if err := reconcileHint(context.Background(), c, event); err != nil {
		t.Fatal(err)
	}
	if !api.cleanupComplete || len(provider.deleted) != 2 || api.reads != 2 {
		t.Fatal("conflict did not require fresh cleanup work")
	}
}

func TestRecordedClusterLimitsProviderWork(t *testing.T) {
	for _, tc := range []struct {
		name, cluster string
		deleted, bad  bool
	}{
		{name: "current", cluster: testClusterID},
		{name: "deleted current", cluster: testClusterID, deleted: true},
		{name: "foreign", cluster: "000000000000000000000000002"},
		{name: "deleted foreign", cluster: "000000000000000000000000002", deleted: true},
		{name: "unassigned", bad: true},
		{name: "invalid", cluster: "bad", bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := ksuid.New().String()
			api := &stateAPI{deleted: tc.deleted, db: &pb.ManagedDatabase{Metadata: &pb.ObjectReference{Id: id}, Provider: "deployment"}, headerOverride: metadata.Pairs("resource-version", "1", "resource-deleted", strconv.FormatBool(tc.deleted), "resource-cleanup", `{"provider":false}`, "hypershell-database-placement", "cluster-v1")}
			if tc.cluster != "" {
				api.headerOverride.Set("hypershell-database-cluster-id", tc.cluster)
			}
			provider := &recordingProvider{}
			c, err := New(api, api, testClusterID, provider)
			if err != nil {
				t.Fatal(err)
			}
			err = c.reconcile(context.Background(), id)
			if (err != nil) != tc.bad {
				t.Fatal(err)
			}
			if tc.cluster != testClusterID && (len(api.updates) != 0 || len(api.cleanupCalls) != 0 || len(provider.ensured) != 0 || len(provider.deleted) != 0) {
				t.Fatal("foreign placement changed API state")
			}
		})
	}
	for _, cluster := range []string{"", "bad"} {
		if _, err := New(&stateAPI{}, &stateAPI{}, cluster, &recordingProvider{}); err == nil {
			t.Fatal("missing cluster accepted")
		}
	}
}
