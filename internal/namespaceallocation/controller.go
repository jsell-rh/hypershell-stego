// Package namespaceallocation maps trusted Hypershell state to STEGO profiles.
package namespaceallocation

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/databasecontroller"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/internal/gatewayworkload"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Allocator is the generated allocation runtime. It owns Kubernetes writes.
type Allocator interface {
	Ensure(context.Context, string, string, string) error
	Delete(context.Context, string, string, string) (bool, error)
}

type Controller struct {
	allocator Allocator
	state     control.GatewayIdentityServiceClient
	databases pb.ManagedDatabaseServiceClient
	sources   []runtime.Source[string]
	cluster   string
}

var ErrPending = errors.New("namespace cleanup is pending")

// New connects both resource streams to one generated queue and telemetry scope.
// Database placement is supplied separately; it must come from trusted state.
func New(cluster string, allocator Allocator, gatewaysAPI pb.GatewayServiceClient, state control.GatewayIdentityServiceClient, databases pb.ManagedDatabaseServiceClient) (*Controller, error) {
	if _, err := gatewayworkload.Namespace(cluster); err != nil {
		return nil, errors.New("namespace allocator requires a managed cluster ID")
	}
	if allocator == nil || state == nil || databases == nil {
		return nil, errors.New("namespace allocator dependencies are required")
	}
	gatewaySource, err := gatewayworkload.Source(gatewaysAPI, state)
	if err != nil {
		return nil, err
	}
	databaseSource, err := databasecontroller.Source(databases)
	if err != nil {
		return nil, err
	}
	return &Controller{allocator: allocator, state: state, databases: databases, cluster: cluster, sources: []runtime.Source[string]{tag("gateway:", gatewaySource), tag("database:", databaseSource)}}, nil
}

func tag(prefix string, source runtime.Source[string]) runtime.Source[string] {
	return runtime.Source[string]{Watch: func(ctx context.Context) (func() (string, error), error) {
		next, err := source.Watch(ctx)
		if err != nil {
			return nil, err
		}
		return func() (string, error) {
			key, err := next()
			if err != nil {
				return "", err
			}
			return prefix + key, nil
		}, nil
	}, Scan: func(ctx context.Context, emit func(string) error) error {
		return source.Scan(ctx, func(key string) error { return emit(prefix + key) })
	}}
}

// Run uses placement to check each database against the current managed cluster.
// The caller must reject ambiguous placement; a watch event is not authority.
func (c *Controller) Run(ctx context.Context, metrics *runtime.Metrics, placement func(context.Context, *pb.ManagedDatabase, string) error) error {
	if placement == nil {
		return errors.New("database cluster placement is required")
	}
	return runtime.RunKeyedWatches(ctx, c.sources, func(ctx context.Context, key string) error { return c.reconcile(ctx, key, placement) }, runtime.KeyedWatchOptions{ReconnectDelay: time.Second, KeyedOptions: runtime.KeyedOptions{Metrics: metrics, Capacity: 1024, Workers: 4, ResyncInterval: 10 * time.Second, Timeout: 20 * time.Second, RetryMin: time.Second, RetryMax: 10 * time.Second, Terminal: func(err error) bool {
		return errors.Is(err, runtime.ErrObservationContract) || errors.Is(err, runtime.ErrScanContract) || errors.Is(err, runtime.ErrWatch) || status.Code(err) == codes.PermissionDenied || status.Code(err) == codes.Unauthenticated
	}}})
}

func (c *Controller) reconcile(ctx context.Context, key string, placement func(context.Context, *pb.ManagedDatabase, string) error) error {
	kind, id, ok := strings.Cut(key, ":")
	if !ok {
		return errors.New("namespace work key has no resource kind")
	}
	switch kind {
	case "gateway":
		response, err := c.state.GetGatewayIdentityState(ctx, &control.GetGatewayIdentityStateRequest{Id: id})
		if err != nil {
			return err
		}
		gw := response.GetGateway()
		name, err := gatewayworkload.Namespace(id)
		if err != nil || gw.GetMetadata().GetId() != id || gw.GetNamespace() != name || response.GetResourceVersion() < 1 || response.GetResourceGeneration() < 1 {
			return errors.New("Gateway allocation state is invalid")
		}
		history := response.GetCleanupTargets()["workload"]
		if history == nil {
			return errors.New("Gateway allocation has no cleanup history")
		}
		_, recorded := history.GetTargets()[c.cluster]
		if response.GetDeleted() || gw.GetClusterId() != c.cluster {
			if !recorded {
				return nil
			}
			return c.remove(ctx, "gateway", name, id)
		}
		if !recorded {
			return errors.New("Gateway placement is not recorded")
		}
		return c.allocator.Ensure(ctx, "gateway", name, id)
	case "database":
		read, err := rpc.WithRetainedResourceRead(ctx)
		if err != nil {
			return err
		}
		var header metadata.MD
		response, err := c.databases.GetManagedDatabase(read, &pb.GetManagedDatabaseRequest{Id: id}, grpc.Header(&header))
		if err != nil {
			return err
		}
		db := response.GetManagedDatabase()
		if db.GetMetadata().GetId() != id {
			return errors.New("database allocation state has a different ID")
		}
		_, deleted, err := rpc.ObservedResourceState(header)
		if err != nil {
			return err
		}
		if db.GetProvider() != gateways.ProviderDeployment {
			return nil
		}
		name, err := gateways.DatabaseNamespace(id)
		if err != nil || db.GetNamespace() != name {
			return errors.New("database allocation namespace is invalid")
		}
		if err := placement(ctx, db, c.cluster); err != nil {
			return err
		}
		if deleted {
			return c.remove(ctx, "database", name, id)
		}
		return c.allocator.Ensure(ctx, "database", name, id)
	default:
		return errors.New("namespace work key has an unknown resource kind")
	}
}
func (c *Controller) remove(ctx context.Context, profile, name, id string) error {
	done, err := c.allocator.Delete(ctx, profile, name, id)
	if err != nil {
		return err
	}
	if !done {
		return ErrPending
	}
	return nil
}
