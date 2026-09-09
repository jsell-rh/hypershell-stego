package sandboxcount

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gatewayworkload"
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
func (c *Controller) Run(parent context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	stopped := make(chan error, 1)
	workers.Go(func() {
		stopped <- c.source.Observe(ctx, kube.Collection{Path: "/api/v1/pods", LabelSelector: SandboxLabel}, c.state.consume)
	})
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	nextRefresh := time.Time{}
	retry := map[string]time.Time{}
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-stopped:
			if ctx.Err() != nil {
				return nil
			}
			if err == nil {
				err = errors.New("sandbox watch stopped")
			}
			return err
		case <-c.state.wake:
		case <-ticker.C:
		}
		c.state.mu.Lock()
		ready := c.state.ready
		c.state.mu.Unlock()
		if !ready {
			continue
		}
		if !time.Now().Before(nextRefresh) {
			err := c.refresh(ctx)
			if denied(err) {
				return err
			}
			if err != nil {
				slog.Warn("Sandbox count catalog needs another pass")
				nextRefresh = time.Now().Add(time.Second)
			} else {
				nextRefresh = time.Now().Add(c.resync)
			}
		}
		c.state.mu.Lock()
		names := []string{}
		for ns := range c.state.dirty {
			if !time.Now().Before(retry[ns]) {
				names = append(names, ns)
			}
		}
		c.state.mu.Unlock()
		sort.Strings(names)
		for _, ns := range names {
			c.state.mu.Lock()
			if !c.state.ready {
				c.state.mu.Unlock()
				break
			}
			count := c.state.counts[ns]
			delete(c.state.dirty, ns)
			c.state.mu.Unlock()
			call, stop := context.WithTimeout(ctx, 5*time.Second)
			_, err := c.counts.SetObservedSandboxCount(call, &control.SetObservedSandboxCountRequest{Namespace: ns, ClusterId: c.cluster, Count: count})
			stop()
			if denied(err) {
				return err
			}
			if err != nil && status.Code(err) != codes.FailedPrecondition {
				c.state.mu.Lock()
				c.state.dirty[ns] = true
				c.state.mu.Unlock()
				retry[ns] = time.Now().Add(time.Second)
				slog.Warn("Sandbox count write needs another pass", "namespace", ns)
			} else {
				delete(retry, ns)
			}
		}
	}
}

// Catalog refresh finds zero counts too. Pod state always comes from the watch
// cache. Events and refreshes share one writer, so an old write cannot overtake
// this controller's newer observation. The count remains advisory.
func (c *Controller) refresh(ctx context.Context) error {
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
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	added := 0
	for _, ns := range namespaces {
		if !c.state.dirty[ns] {
			added++
		}
	}
	if len(c.state.dirty)+added > kube.MaxObservedObjects {
		return errors.New("sandbox count queue exceeds its limit")
	}
	for _, ns := range namespaces {
		c.state.dirty[ns] = true
	}
	return nil
}
