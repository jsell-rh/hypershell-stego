package sandboxcount

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gatewayworkload"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Source interface {
	Observe(context.Context, kube.Collection, func(kube.Change) error) error
}
type Controller struct {
	source   Source
	gateways pb.GatewayServiceClient
	counts   control.GatewayIdentityServiceClient
	cluster  string
	resync   time.Duration
	state    *observation
}

func New(source Source, gateways pb.GatewayServiceClient, counts control.GatewayIdentityServiceClient, cluster string, resync time.Duration) (*Controller, error) {
	if _, err := gatewayworkload.Namespace(cluster); err != nil || source == nil || gateways == nil || counts == nil {
		return nil, errors.New("sandbox count controller requires clients and a cluster ID")
	}
	if resync == 0 {
		resync = 2 * time.Minute
	}
	if resync < time.Second || resync > 5*time.Minute {
		return nil, errors.New("sandbox count resync must be between one second and five minutes")
	}
	return &Controller{source, gateways, counts, cluster, resync, newObservation()}, nil
}
func denied(err error) bool {
	return status.Code(err) == codes.Unauthenticated || status.Code(err) == codes.PermissionDenied
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
	return c.source.Observe(ctx, kube.Collection{Path: "/api/v1/pods", LabelSelector: SandboxLabel}, func(change kube.Change) error {
		err := c.state.consume(change)
		c.state.mu.Lock()
		defer c.state.mu.Unlock()
		if err != nil {
			sink.SetReady(false)
			return err
		}
		if c.state.ready {
			for ns := range c.state.changed {
				if err := sink.Add(ns); err != nil {
					sink.SetReady(false)
					return err
				}
			}
			clear(c.state.changed)
		}
		sink.SetReady(c.state.ready)
		return nil
	})
}
func (c *Controller) reconcile(ctx context.Context, ns string) error {
	c.state.mu.Lock()
	ready, count := c.state.ready, c.state.counts[ns]
	c.state.mu.Unlock()
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
				namespaces = append(namespaces, ns)
			}
		}
		if len(result.Items) < 100 {
			break
		}
	}
	for _, ns := range namespaces {
		if err := enqueue(ns); err != nil {
			return err
		}
	}
	return nil
}
