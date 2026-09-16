package sandboxcount

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gatewayworkload"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	"github.com/jsell-rh/hypershell-stego/out/deploy/allocation"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Source interface {
	kube.CollectionObserver
}
type NamespaceSource interface {
	NamespaceUID(context.Context, string, string, string) (string, error)
}
type Controller struct {
	watches    *kube.WatchSet
	allocation NamespaceSource
	gateways   pb.GatewayServiceClient
	counts     control.GatewayIdentityServiceClient
	cluster    string
	resync     time.Duration
	mu         sync.Mutex
	states     map[string]*observation
}

func New(source Source, namespaces NamespaceSource, gateways pb.GatewayServiceClient, counts control.GatewayIdentityServiceClient, cluster string, resync time.Duration) (*Controller, error) {
	if _, err := gatewayworkload.Namespace(cluster); err != nil || source == nil || namespaces == nil || gateways == nil || counts == nil {
		return nil, errors.New("sandbox count controller requires clients and a cluster ID")
	}
	if resync == 0 {
		resync = 2 * time.Minute
	}
	if resync < time.Second || resync > 5*time.Minute {
		return nil, errors.New("sandbox count resync must be between one second and five minutes")
	}
	watches, err := kube.NewWatchSet(source, kube.WatchSetOptions{})
	if err != nil {
		return nil, err
	}
	return &Controller{watches: watches, allocation: namespaces, gateways: gateways, counts: counts, cluster: cluster, resync: resync, states: map[string]*observation{}}, nil
}
func denied(err error) bool {
	return errors.Is(err, kube.ErrWatchSetContract) || status.Code(err) == codes.Unauthenticated || status.Code(err) == codes.PermissionDenied
}
func (c *Controller) Run(ctx context.Context) error {
	return c.RunWithMetrics(ctx, nil)
}

// RunWithMetrics connects optional generated diagnostics to the controller.
func (c *Controller) RunWithMetrics(ctx context.Context, metrics *runtime.Metrics) error {
	return runtime.RunKeyed(ctx, runtime.KeyedSource[string]{Observe: c.observe, Scan: c.refresh}, c.reconcile, runtime.KeyedOptions{
		Metrics:  metrics,
		Capacity: kube.MaxObservedObjects, ResyncInterval: c.resync, Timeout: 5 * time.Second,
		RetryMin: time.Second, RetryMax: 16 * time.Second, Terminal: denied,
		Observe: func(event runtime.Event) {
			if event.Phase == "scan_failed" {
				slog.Warn("Sandbox count catalog needs another pass")
			}
			if event.Phase == "reconcile_failed" {
				slog.Warn("Sandbox count write needs another pass")
			}
		},
	})
}

// The cache defines the count. The generated sink schedules changed namespaces.
func (c *Controller) observe(ctx context.Context, sink *runtime.KeySink[string]) error {
	c.mu.Lock()
	clear(c.states)
	c.mu.Unlock()
	// The catalog can run with an empty cache. Each namespace has its own
	// baseline check in reconcile; a missing scope never means a zero count.
	sink.SetReady(true)
	defer sink.SetReady(false)
	return c.watches.Run(ctx, func(event kube.ScopedChange) error {
		ns := strings.TrimSuffix(strings.TrimPrefix(event.Scope.Collection.Path, "/api/v1/namespaces/"), "/pods")
		c.mu.Lock()
		if event.Change.Type == "REMOVED" {
			delete(c.states, ns)
			c.mu.Unlock()
			return nil
		}
		if event.Change.Type == "RESET" {
			c.states[ns] = newObservation()
			c.mu.Unlock()
			if event.Err != nil {
				slog.Warn("Sandbox Pod watch needs a new assignment check")
			}
			return nil
		}
		state := c.states[ns]
		if state == nil {
			c.mu.Unlock()
			return errors.New("sandbox namespace has no baseline reset")
		}
		err := state.consume(event.Change)
		changed := event.Change.Type == "REPLACE" || state.changed[ns]
		clear(state.changed)
		c.mu.Unlock()
		if err != nil {
			return err
		}
		if changed {
			return sink.Add(ns)
		}
		return nil
	})
}
func (c *Controller) reconcile(ctx context.Context, ns string) error {
	c.mu.Lock()
	state := c.states[ns]
	if state == nil {
		c.mu.Unlock()
		return nil
	}
	ready, count := state.ready, state.counts[ns]
	c.mu.Unlock()
	if !ready {
		return runtime.ErrNotReady
	}
	_, err := c.counts.SetObservedSandboxCount(ctx, &control.SetObservedSandboxCountRequest{Namespace: ns, ClusterId: c.cluster, Count: count})
	if status.Code(err) == codes.FailedPrecondition {
		return nil
	}
	return err
}

// Catalog refresh finds zero counts too. Pod state always comes from the watch
// cache. Events and refreshes share one writer, so an old write cannot overtake
// this controller's newer observation. The count remains advisory.
func (c *Controller) refresh(ctx context.Context, enqueue func(string) error) error {
	namespaces := []string{}
	scopes := []kube.CollectionScope{}
	seen := map[string]bool{}
	for page := int32(1); ; page++ {
		call, stop := context.WithTimeout(ctx, 5*time.Second)
		result, err := c.gateways.ListGateways(call, &pb.ListGatewaysRequest{Page: page, Size: 100})
		stop()
		if err != nil {
			return err
		}
		if result == nil || len(result.Items) > 100 {
			return errors.New("invalid Gateway count catalog")
		}
		for _, gw := range result.Items {
			id := gw.GetMetadata().GetId()
			ns, err := gatewayworkload.Namespace(id)
			if err != nil || ns != gw.GetNamespace() || seen[id] {
				return errors.New("Gateway count catalog has invalid identity")
			}
			seen[id] = true
			if len(seen) > kube.MaxObservedObjects {
				return errors.New("Gateway count catalog exceeds its limit")
			}
			if gw.GetClusterId() == c.cluster {
				// Public lists retain pending deletions. Only stored control-plane
				// state can decide whether this namespace still needs a watch.
				call, stop := context.WithTimeout(ctx, 5*time.Second)
				state, err := c.counts.GetGatewayIdentityState(call, &control.GetGatewayIdentityStateRequest{Id: id})
				stop()
				if err != nil {
					return err
				}
				current := state.GetGateway()
				if state == nil || state.GetResourceVersion() < 1 || current.GetMetadata().GetId() != id || current.GetNamespace() != ns {
					return fmt.Errorf("%w: Gateway state identity could not be verified", kube.ErrWatchSetContract)
				}
				if state.GetDeleted() || current.GetClusterId() != c.cluster {
					continue
				}
				call, stop = context.WithTimeout(ctx, 5*time.Second)
				uid, err := c.allocation.NamespaceUID(call, "gateway", ns, id)
				stop()
				if errors.Is(err, allocation.ErrPending) {
					continue
				}
				if err != nil {
					return fmt.Errorf("%w: Gateway namespace identity could not be verified", kube.ErrWatchSetContract)
				}
				scopes = append(scopes, kube.CollectionScope{Collection: kube.Collection{Path: "/api/v1/namespaces/" + ns + "/pods", LabelSelector: SandboxLabel}, Identity: uid})
				namespaces = append(namespaces, ns)
			}
		}
		if len(result.Items) < 100 {
			break
		}
	}
	if err := c.watches.Replace(ctx, scopes); err != nil {
		return err
	}
	for _, ns := range namespaces {
		if err := enqueue(ns); err != nil {
			return err
		}
	}
	return nil
}
