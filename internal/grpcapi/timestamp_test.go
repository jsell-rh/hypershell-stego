package grpcapi

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jsell-rh/hypershell-stego/internal/catalog"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/out/auth"
	events "github.com/jsell-rh/hypershell-stego/out/contracts/events"
	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"github.com/segmentio/ksuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// The store supplies fixed domain results. These checks test adapter responses,
// not database transactions or token trust rules.
type timestampStore struct {
	gateways.Repository
	row   any
	rows  any
	reads int
}

func (s *timestampStore) WithTransaction(ctx context.Context, fn func(context.Context, store.Transaction) error) error {
	return fn(ctx, s)
}
func (s *timestampStore) Get(context.Context, string, string) (any, error) {
	s.reads++
	return s.row, nil
}
func (s *timestampStore) List(_ context.Context, _ string, field, _ string, q store.ListOptions) (store.ListResult, error) {
	s.reads++
	if q.CountOnly {
		return store.ListResult{Total: 1}, nil
	}
	if field == "id" {
		value := reflect.MakeSlice(reflect.SliceOf(reflect.TypeOf(s.row)), 1, 1)
		value.Index(0).Set(reflect.ValueOf(s.row))
		return store.ListResult{Items: value.Interface(), Total: 1}, nil
	}
	return store.ListResult{Items: s.rows, Total: int64(reflect.ValueOf(s.rows).Len())}, nil
}
func (*timestampStore) Create(context.Context, string, any) error          { return nil }
func (*timestampStore) Replace(context.Context, string, string, any) error { return nil }
func (*timestampStore) Notify(...store.Notification) error                 { return nil }

func timestampContext(t *testing.T) context.Context {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := auth.NewVerifier(auth.Config{Issuer: "https://issuer.example", Audience: "timestamp-tests", PublicKey: &key.PublicKey, RolesClaim: "roles"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	token, err := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": "https://issuer.example", "sub": "operator", "preferred_username": "operator", "aud": "timestamp-tests", "iat": now.Add(-time.Minute).Unix(), "exp": now.Add(time.Minute).Unix(), "roles": []string{"platform:admin"}}).SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := verifier.Authenticate(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

type timestampSource struct {
	next   func() (events.Event, error)
	closed bool
}

func (s *timestampSource) Subscribe(context.Context) (events.Subscription, error) { return s, nil }
func (s *timestampSource) Next(context.Context) (events.Event, error)             { return s.next() }
func (s *timestampSource) Close()                                                 { s.closed = true }

type timestampStream[T any] struct {
	grpc.ServerStream
	ctx     context.Context
	sent    []*T
	headers int
}

func (s *timestampStream[T]) Context() context.Context     { return s.ctx }
func (s *timestampStream[T]) SendHeader(metadata.MD) error { s.headers++; return nil }
func (s *timestampStream[T]) Send(value *T) error          { s.sent = append(s.sent, value); return nil }

func requirePrivateTimestampError(t *testing.T, result any, err error) {
	t.Helper()
	if result != nil && !reflect.ValueOf(result).IsNil() {
		t.Fatal("conversion returned a partial response")
	}
	if status.Code(err) != codes.Internal || status.Convert(err).Message() != "request failed" || len(status.Convert(err).Details()) != 0 {
		t.Fatalf("conversion did not return the fixed private error: %v", err)
	}
}
func catalogForTimestamp(t *testing.T, repository *timestampStore) *catalog.Service {
	t.Helper()
	policy, err := gateways.New(repository)
	if err != nil {
		t.Fatal(err)
	}
	service, err := catalog.New(repository, policy)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestManagedClusterTimestampResponses(t *testing.T) {
	ctx := timestampContext(t)
	valid := model.Meta{ID: ksuid.New().String(), CreatedTime: time.Unix(100, 123456789), UpdatedTime: time.Unix(200, 987654321)}
	makeRow := func(meta model.Meta) model.ManagedCluster {
		return model.ManagedCluster{Meta: meta, Name: "fixture", Provider: "test", KubeconfigSecret: "fixture"}
	}
	for _, field := range []string{"created", "updated"} {
		for _, invalid := range []struct {
			name  string
			value time.Time
		}{{"below_range", time.Date(0, 12, 31, 23, 59, 59, 0, time.UTC)}, {"above_range", time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)}} {
			bad := valid
			if field == "created" {
				bad.CreatedTime = invalid.value
			} else {
				bad.UpdatedTime = invalid.value
			}
			repository := &timestampStore{row: makeRow(bad), rows: []model.ManagedCluster{makeRow(valid), makeRow(bad)}}
			service := &clusterServer{resource: catalogForTimestamp(t, repository).Clusters}
			calls := map[string]func(context.Context) (any, error){
				"create": func(ctx context.Context) (any, error) {
					return service.CreateManagedCluster(ctx, &pb.CreateManagedClusterRequest{Name: "fixture", Provider: "test", KubeconfigSecret: "fixture"})
				},
				"get": func(ctx context.Context) (any, error) {
					return service.GetManagedCluster(ctx, &pb.GetManagedClusterRequest{Id: valid.ID})
				},
				"update": func(ctx context.Context) (any, error) {
					return service.UpdateManagedCluster(ctx, &pb.UpdateManagedClusterRequest{Id: valid.ID})
				},
				"list": func(ctx context.Context) (any, error) {
					return service.ListManagedClusters(ctx, &pb.ListManagedClustersRequest{})
				},
			}
			for name, call := range calls {
				t.Run(field+"/"+invalid.name+"/"+name, func(t *testing.T) { result, err := call(ctx); requirePrivateTimestampError(t, result, err) })
			}
			t.Run(field+"/"+invalid.name+"/watch", func(t *testing.T) {
				calls := 0
				source := &timestampSource{next: func() (events.Event, error) {
					calls++
					if calls > 1 {
						return events.Event{}, errors.New("unexpected next event")
					}
					return events.Event{Destination: "kafka", Kind: "managedcluster.updated", ResourceKey: valid.ID}, nil
				}}
				service.source = source
				stream := &timestampStream[pb.WatchManagedClustersResponse]{ctx: ctx}
				err := service.WatchManagedClusters(&pb.WatchManagedClustersRequest{}, stream)
				requirePrivateTimestampError(t, nil, err)
				if len(stream.sent) != 0 || stream.headers != 1 || !source.closed {
					t.Fatal("invalid watch response was sent or subscription was retained")
				}
			})
		}
	}
	t.Run("watch_retains_prior_valid_message", func(t *testing.T) {
		bad := valid
		bad.UpdatedTime = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
		repository := &timestampStore{row: makeRow(valid), rows: []model.ManagedCluster{makeRow(valid)}}
		calls := 0
		source := &timestampSource{next: func() (events.Event, error) {
			calls++
			if calls == 2 {
				repository.row = makeRow(bad)
			}
			if calls > 2 {
				return events.Event{}, errors.New("unexpected next event")
			}
			return events.Event{Destination: "kafka", Kind: "managedcluster.updated", ResourceKey: valid.ID}, nil
		}}
		service := &clusterServer{resource: catalogForTimestamp(t, repository).Clusters, source: source}
		stream := &timestampStream[pb.WatchManagedClustersResponse]{ctx: ctx}
		err := service.WatchManagedClusters(&pb.WatchManagedClustersRequest{}, stream)
		requirePrivateTimestampError(t, nil, err)
		if len(stream.sent) != 1 || calls != 2 || !source.closed {
			t.Fatal("watch did not stop before the invalid message")
		}
		message := stream.sent[0]
		if message.GetResourceId() != valid.ID || message.GetType() != pb.EventType_EVENT_TYPE_UPDATED || !message.GetManagedCluster().GetMetadata().GetUpdatedAt().AsTime().Equal(valid.UpdatedTime) {
			t.Fatal("watch changed the valid response")
		}
		if _, err := protojson.Marshal(message); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("identity_check_precedes_conversion", func(t *testing.T) {
		bad := valid
		bad.CreatedTime = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
		repository := &timestampStore{row: makeRow(bad)}
		service := &clusterServer{resource: catalogForTimestamp(t, repository).Clusters}
		result, err := service.GetManagedCluster(context.Background(), &pb.GetManagedClusterRequest{Id: valid.ID})
		if result != nil || status.Code(err) != codes.Unauthenticated || repository.reads != 0 {
			t.Fatal("missing identity reached storage or conversion")
		}
	})
}

func TestGatewayReleaseTimestampResponses(t *testing.T) {
	ctx := timestampContext(t)
	valid := model.Meta{ID: ksuid.New().String(), CreatedTime: time.Unix(100, 123456789), UpdatedTime: time.Unix(200, 987654321)}
	makeRow := func(meta model.Meta) model.GatewayRelease {
		return model.GatewayRelease{Meta: meta, Name: "fixture", Image: "registry.example/image@sha256:fixture"}
	}
	for _, field := range []string{"created", "updated"} {
		for _, invalid := range []struct {
			name  string
			value time.Time
		}{{"below_range", time.Date(0, 12, 31, 23, 59, 59, 0, time.UTC)}, {"above_range", time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)}} {
			bad := valid
			if field == "created" {
				bad.CreatedTime = invalid.value
			} else {
				bad.UpdatedTime = invalid.value
			}
			repository := &timestampStore{row: makeRow(bad), rows: []model.GatewayRelease{makeRow(valid), makeRow(bad)}}
			service := &releaseServer{resource: catalogForTimestamp(t, repository).Releases}
			calls := map[string]func(context.Context) (any, error){
				"create": func(ctx context.Context) (any, error) {
					return service.CreateGatewayRelease(ctx, &pb.CreateGatewayReleaseRequest{Name: "fixture", Image: "registry.example/image@sha256:fixture"})
				},
				"get": func(ctx context.Context) (any, error) {
					return service.GetGatewayRelease(ctx, &pb.GetGatewayReleaseRequest{Id: valid.ID})
				},
				"update": func(ctx context.Context) (any, error) {
					return service.UpdateGatewayRelease(ctx, &pb.UpdateGatewayReleaseRequest{Id: valid.ID})
				},
				"list": func(ctx context.Context) (any, error) {
					return service.ListGatewayReleases(ctx, &pb.ListGatewayReleasesRequest{})
				},
			}
			for name, call := range calls {
				t.Run(field+"/"+invalid.name+"/"+name, func(t *testing.T) { result, err := call(ctx); requirePrivateTimestampError(t, result, err) })
			}
			t.Run(field+"/"+invalid.name+"/watch", func(t *testing.T) {
				calls := 0
				source := &timestampSource{next: func() (events.Event, error) {
					calls++
					if calls > 1 {
						return events.Event{}, errors.New("unexpected next event")
					}
					return events.Event{Destination: "kafka", Kind: "gatewayrelease.updated", ResourceKey: valid.ID}, nil
				}}
				service.source = source
				stream := &timestampStream[pb.WatchGatewayReleasesResponse]{ctx: ctx}
				err := service.WatchGatewayReleases(&pb.WatchGatewayReleasesRequest{}, stream)
				requirePrivateTimestampError(t, nil, err)
				if len(stream.sent) != 0 || stream.headers != 1 || !source.closed {
					t.Fatal("invalid watch response was sent or subscription was retained")
				}
			})
		}
	}
	t.Run("watch_retains_prior_valid_message", func(t *testing.T) {
		bad := valid
		bad.UpdatedTime = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
		repository := &timestampStore{row: makeRow(valid), rows: []model.GatewayRelease{makeRow(valid)}}
		calls := 0
		source := &timestampSource{next: func() (events.Event, error) {
			calls++
			if calls == 2 {
				repository.row = makeRow(bad)
			}
			if calls > 2 {
				return events.Event{}, errors.New("unexpected next event")
			}
			return events.Event{Destination: "kafka", Kind: "gatewayrelease.updated", ResourceKey: valid.ID}, nil
		}}
		service := &releaseServer{resource: catalogForTimestamp(t, repository).Releases, source: source}
		stream := &timestampStream[pb.WatchGatewayReleasesResponse]{ctx: ctx}
		err := service.WatchGatewayReleases(&pb.WatchGatewayReleasesRequest{}, stream)
		requirePrivateTimestampError(t, nil, err)
		if len(stream.sent) != 1 || calls != 2 || !source.closed {
			t.Fatal("watch did not stop before the invalid message")
		}
		message := stream.sent[0]
		if message.GetResourceId() != valid.ID || message.GetType() != pb.EventType_EVENT_TYPE_UPDATED || !message.GetGatewayRelease().GetMetadata().GetUpdatedAt().AsTime().Equal(valid.UpdatedTime) {
			t.Fatal("watch changed the valid response")
		}
		if _, err := protojson.Marshal(message); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("identity_check_precedes_conversion", func(t *testing.T) {
		bad := valid
		bad.CreatedTime = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
		repository := &timestampStore{row: makeRow(bad)}
		service := &releaseServer{resource: catalogForTimestamp(t, repository).Releases}
		result, err := service.GetGatewayRelease(context.Background(), &pb.GetGatewayReleaseRequest{Id: valid.ID})
		if result != nil || status.Code(err) != codes.Unauthenticated || repository.reads != 0 {
			t.Fatal("missing identity reached storage or conversion")
		}
	})
}

func TestGatewayNetworkTimestampResponses(t *testing.T) {
	ctx := timestampContext(t)
	valid := model.Meta{ID: ksuid.New().String(), CreatedTime: time.Unix(100, 123456789), UpdatedTime: time.Unix(200, 987654321)}
	makeRow := func(meta model.Meta) model.GatewayNetwork { return model.GatewayNetwork{Meta: meta, Name: "fixture"} }
	for _, field := range []string{"created", "updated"} {
		for _, invalid := range []struct {
			name  string
			value time.Time
		}{{"below_range", time.Date(0, 12, 31, 23, 59, 59, 0, time.UTC)}, {"above_range", time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)}} {
			bad := valid
			if field == "created" {
				bad.CreatedTime = invalid.value
			} else {
				bad.UpdatedTime = invalid.value
			}
			repository := &timestampStore{row: makeRow(bad), rows: []model.GatewayNetwork{makeRow(valid), makeRow(bad)}}
			service := &networkServer{resource: catalogForTimestamp(t, repository).Networks}
			calls := map[string]func(context.Context) (any, error){
				"create": func(ctx context.Context) (any, error) {
					return service.CreateGatewayNetwork(ctx, &pb.CreateGatewayNetworkRequest{Name: "fixture"})
				},
				"get": func(ctx context.Context) (any, error) {
					return service.GetGatewayNetwork(ctx, &pb.GetGatewayNetworkRequest{Id: valid.ID})
				},
				"update": func(ctx context.Context) (any, error) {
					return service.UpdateGatewayNetwork(ctx, &pb.UpdateGatewayNetworkRequest{Id: valid.ID})
				},
				"list": func(ctx context.Context) (any, error) {
					return service.ListGatewayNetworks(ctx, &pb.ListGatewayNetworksRequest{})
				},
			}
			for name, call := range calls {
				t.Run(field+"/"+invalid.name+"/"+name, func(t *testing.T) { result, err := call(ctx); requirePrivateTimestampError(t, result, err) })
			}
			t.Run(field+"/"+invalid.name+"/watch", func(t *testing.T) {
				calls := 0
				source := &timestampSource{next: func() (events.Event, error) {
					calls++
					if calls > 1 {
						return events.Event{}, errors.New("unexpected next event")
					}
					return events.Event{Destination: "kafka", Kind: "gatewaynetwork.updated", ResourceKey: valid.ID}, nil
				}}
				service.source = source
				stream := &timestampStream[pb.WatchGatewayNetworksResponse]{ctx: ctx}
				err := service.WatchGatewayNetworks(&pb.WatchGatewayNetworksRequest{}, stream)
				requirePrivateTimestampError(t, nil, err)
				if len(stream.sent) != 0 || stream.headers != 1 || !source.closed {
					t.Fatal("invalid watch response was sent or subscription was retained")
				}
			})
		}
	}
	t.Run("watch_retains_prior_valid_message", func(t *testing.T) {
		bad := valid
		bad.UpdatedTime = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
		repository := &timestampStore{row: makeRow(valid), rows: []model.GatewayNetwork{makeRow(valid)}}
		calls := 0
		source := &timestampSource{next: func() (events.Event, error) {
			calls++
			if calls == 2 {
				repository.row = makeRow(bad)
			}
			if calls > 2 {
				return events.Event{}, errors.New("unexpected next event")
			}
			return events.Event{Destination: "kafka", Kind: "gatewaynetwork.updated", ResourceKey: valid.ID}, nil
		}}
		service := &networkServer{resource: catalogForTimestamp(t, repository).Networks, source: source}
		stream := &timestampStream[pb.WatchGatewayNetworksResponse]{ctx: ctx}
		err := service.WatchGatewayNetworks(&pb.WatchGatewayNetworksRequest{}, stream)
		requirePrivateTimestampError(t, nil, err)
		if len(stream.sent) != 1 || calls != 2 || !source.closed {
			t.Fatal("watch did not stop before the invalid message")
		}
		message := stream.sent[0]
		if message.GetResourceId() != valid.ID || message.GetType() != pb.EventType_EVENT_TYPE_UPDATED || !message.GetGatewayNetwork().GetMetadata().GetUpdatedAt().AsTime().Equal(valid.UpdatedTime) {
			t.Fatal("watch changed the valid response")
		}
		if _, err := protojson.Marshal(message); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("identity_check_precedes_conversion", func(t *testing.T) {
		bad := valid
		bad.CreatedTime = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
		repository := &timestampStore{row: makeRow(bad)}
		service := &networkServer{resource: catalogForTimestamp(t, repository).Networks}
		result, err := service.GetGatewayNetwork(context.Background(), &pb.GetGatewayNetworkRequest{Id: valid.ID})
		if result != nil || status.Code(err) != codes.Unauthenticated || repository.reads != 0 {
			t.Fatal("missing identity reached storage or conversion")
		}
	})
}

func TestTimestampPresentersPreserveContract(t *testing.T) {
	times := []time.Time{time.Time{}, time.Unix(-1, 999999999), time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.UTC), time.Date(2026, 1, 2, 3, 4, 5, 123456789, time.FixedZone("offset", 3600))}
	for _, value := range times {
		meta := model.Meta{ID: "reference", CreatedTime: value, UpdatedTime: value}
		presenters := map[string]func() (proto.Message, error){
			"Gateway": func() (proto.Message, error) { return present(model.Gateway{Meta: meta, Name: "gateway"}) },
			"RoleBinding": func() (proto.Message, error) {
				return presentGrant(gateways.GrantView{Grant: model.RoleBinding{Meta: meta}})
			},
			"ManagedCluster": func() (proto.Message, error) { return presentManagedCluster(model.ManagedCluster{Meta: meta}) },
			"GatewayRelease": func() (proto.Message, error) { return presentGatewayRelease(model.GatewayRelease{Meta: meta}) },
			"GatewayNetwork": func() (proto.Message, error) { return presentGatewayNetwork(model.GatewayNetwork{Meta: meta}) },
		}
		for name, present := range presenters {
			t.Run(name+"/"+value.Format(time.RFC3339Nano), func(t *testing.T) {
				message, err := present()
				if err != nil {
					t.Fatal(err)
				}
				reference := message.(interface{ GetMetadata() *pb.ObjectReference }).GetMetadata()
				if reference.GetId() != meta.ID || reference.GetKind() != name || !reference.GetCreatedAt().AsTime().Equal(value) || !reference.GetUpdatedAt().AsTime().Equal(value) {
					t.Fatal("timestamp or reference changed")
				}
				if _, err := protojson.Marshal(message); err != nil {
					t.Fatal(err)
				}
				wire, err := proto.Marshal(message)
				if err != nil {
					t.Fatal(err)
				}
				clone := message.ProtoReflect().New().Interface()
				if err := proto.Unmarshal(wire, clone); err != nil || !proto.Equal(message, clone) {
					t.Fatal("protobuf round trip changed the response")
				}
			})
		}
	}
}
func TestCleanupSummaryTimestampPresence(t *testing.T) {
	now := time.Unix(123, 456)
	for _, oldest := range []*time.Time{nil, new(time.Time), &now} {
		result, err := cleanupSummary(store.CleanupSummary{ObservedAt: now, OldestPending: oldest}, "owner", "target", "provider")
		if err != nil || result.GetOwner() != "owner" || result.GetTarget() != "target" || result.GetProvider() != "provider" || !result.GetObservedAt().AsTime().Equal(now) {
			t.Fatal("cleanup summary changed", err)
		}
		if (result.OldestPending == nil) != (oldest == nil) || (oldest != nil && !result.OldestPending.AsTime().Equal(*oldest)) {
			t.Fatal("optional timestamp presence changed")
		}
	}
	bad := time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, row := range []store.CleanupSummary{{ObservedAt: bad}, {ObservedAt: now, OldestPending: &bad}} {
		result, err := cleanupSummary(row, "owner", "target", "provider")
		requirePrivateTimestampError(t, result, err)
	}
}

func TestTimestampPresentersRejectInvalidRecords(t *testing.T) {
	for _, field := range []string{"created", "updated"} {
		for _, year := range []int{0, 10000} {
			meta := model.Meta{ID: "private-reference", CreatedTime: time.Unix(100, 0), UpdatedTime: time.Unix(200, 0)}
			bad := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
			if field == "created" {
				meta.CreatedTime = bad
			} else {
				meta.UpdatedTime = bad
			}
			presenters := map[string]func() (proto.Message, error){
				"Gateway": func() (proto.Message, error) { return present(model.Gateway{Meta: meta}) },
				"RoleBinding": func() (proto.Message, error) {
					return presentGrant(gateways.GrantView{Grant: model.RoleBinding{Meta: meta}})
				},
			}
			for name, present := range presenters {
				t.Run(name+"/"+field+"/"+bad.Format("2006"), func(t *testing.T) {
					result, err := present()
					if err == nil {
						t.Fatal("invalid timestamp was accepted")
					}
					requirePrivateTimestampError(t, result, mapError(err))
				})
			}
		}
	}
}
