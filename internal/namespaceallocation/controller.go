// Package namespaceallocation maps trusted Hypershell state to STEGO profiles.
package namespaceallocation

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/cleanupmetrics"
	"github.com/jsell-rh/hypershell-stego/internal/gatewayworkload"
	keycloak "github.com/jsell-rh/hypershell-stego/internal/serviceaccountkeycloak"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc/codes"
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
	sources   []runtime.Source[string]
	cluster   string
	console   bool
	sandbox   bool
}

// Options contains operator configuration for this managed cluster.
type Options struct {
	ConsoleDomain  string
	SandboxEnabled bool
}

var ErrPending = errors.New("namespace cleanup is pending")

// New connects Gateway state to the generated queue and telemetry scope.
func New(cluster string, allocator Allocator, gatewaysAPI pb.GatewayServiceClient, state control.GatewayIdentityServiceClient, options Options) (*Controller, error) {
	if _, err := gatewayworkload.Namespace(cluster); err != nil {
		return nil, errors.New("namespace allocator requires a managed cluster ID")
	}
	if allocator == nil || state == nil {
		return nil, errors.New("namespace allocator dependencies are required")
	}
	if options.ConsoleDomain != "" {
		if _, err := keycloak.GatewayConsoleOrigin(cluster, options.ConsoleDomain); err != nil {
			return nil, err
		}
	}
	gatewaySource, err := gatewayworkload.Source(gatewaysAPI, state)
	if err != nil {
		return nil, err
	}
	return &Controller{allocator: allocator, state: state, cluster: cluster, console: options.ConsoleDomain != "", sandbox: options.SandboxEnabled, sources: []runtime.Source[string]{tag("gateway:", gatewaySource)}}, nil
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

// Run checks recorded Gateway placement before allocation. A watch event
// is not authority to create or remove a namespace.
func (c *Controller) Run(ctx context.Context, metrics *runtime.Metrics) error {
	return runtime.RunKeyedWatches(ctx, c.sources, c.reconcile, runtime.KeyedWatchOptions{ReconnectDelay: time.Second, KeyedOptions: runtime.KeyedOptions{Metrics: metrics, Cleanup: cleanupmetrics.Gateway(c.state, "allocation", c.cluster), Capacity: 1024, Workers: 4, ResyncInterval: 10 * time.Second, Timeout: 20 * time.Second, RetryMin: time.Second, RetryMax: 10 * time.Second, Terminal: func(err error) bool {
		return errors.Is(err, runtime.ErrObservationContract) || errors.Is(err, runtime.ErrScanContract) || errors.Is(err, runtime.ErrWatch) || status.Code(err) == codes.PermissionDenied || status.Code(err) == codes.Unauthenticated
	}}})
}

func (c *Controller) reconcile(ctx context.Context, key string) error {
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
		complete, recorded := history.GetTargets()[c.cluster]
		stateName, err := gatewayworkload.StateNamespace(id)
		if err != nil {
			return err
		}
		if response.GetDeleted() || gw.GetClusterId() != c.cluster {
			if !recorded {
				return nil
			}
			remove := func(operation context.Context) error {
				if err := c.remove(operation, "gateway", name, id); err != nil {
					return err
				}
				// Remove retained Sandbox allocations even when new Sandbox
				// creation is disabled. The Gateway must stop creating work first.
				sandboxName, err := gatewayworkload.SandboxNamespace(id)
				if err != nil {
					return err
				}
				if err := c.remove(operation, "sandbox", sandboxName, id); err != nil {
					return err
				}
				sqlHistory := response.GetCleanupTargets()["sql"]
				if sqlHistory == nil {
					return errors.New("Gateway allocation has no SQL cleanup history")
				}
				sqlComplete, sqlRecorded := sqlHistory.GetTargets()[c.cluster]
				if !sqlRecorded {
					return errors.New("Gateway SQL cleanup placement is not recorded")
				}
				if !complete || !sqlComplete {
					return ErrPending
				}
				consoleStateName, err := gatewayworkload.ConsoleStateNamespace(id)
				if err != nil {
					return err
				}
				if err = c.remove(operation, "gateway-console-state", consoleStateName, id); err != nil {
					return err
				}
				return c.remove(operation, "gateway-state", stateName, id)
			}
			if !response.GetDeleted() {
				return remove(ctx)
			}
			history := response.GetCleanupTargets()["allocation"]
			if history == nil {
				return errors.New("Gateway allocation cleanup history is missing")
			}
			prior, present := history.GetTargets()[c.cluster]
			if !present {
				return errors.New("Gateway allocation cleanup placement is not recorded")
			}
			return runtime.RunObservation(ctx, remove, func(commit context.Context, failure error) error {
				complete := failure == nil
				if complete == prior {
					return nil
				}
				write, err := rpc.WithResourceVersion(commit, response.GetResourceVersion())
				if err != nil {
					return err
				}
				_, err = c.state.ObserveGatewayCleanup(write, &control.ObserveGatewayCleanupRequest{Id: id, Owner: "allocation", Target: c.cluster, Complete: complete})
				return err
			}, runtime.ObservationOptions{WorkTimeout: 20 * time.Second, CommitTimeout: 2 * time.Second})
		}
		if !recorded {
			return errors.New("Gateway placement is not recorded")
		}
		if err := c.allocator.Ensure(ctx, "gateway-state", stateName, id); err != nil {
			return err
		}
		// Storage must exist before the workload can publish a ready address.
		if c.console {
			consoleStateName, err := gatewayworkload.ConsoleStateNamespace(id)
			if err != nil {
				return err
			}
			if err = c.allocator.Ensure(ctx, "gateway-console-state", consoleStateName, id); err != nil {
				return err
			}
		}
		if err := c.allocator.Ensure(ctx, "gateway", name, id); err != nil {
			return err
		}
		if c.sandbox {
			sandboxName, err := gatewayworkload.SandboxNamespace(id)
			if err != nil {
				return err
			}
			return c.allocator.Ensure(ctx, "sandbox", sandboxName, id)
		}
		return nil
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
