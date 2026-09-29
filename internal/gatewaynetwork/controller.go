// Package gatewaynetwork reconciles GatewayNetwork topology references
// through public generated transports. The verdict is stored in the network's
// control-plane-owned status field.
package gatewaynetwork

import (
	"context"
	"errors"
	"fmt"
	"time"

	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const QueueCapacity = 1024
const Workers = 2
const ResyncInterval = 30 * time.Second
const ReconcileTimeout = 20 * time.Second

const (
	statusValid      = "Valid"
	statusInvalid    = "Invalid"
	topologyMesh     = "mesh"
	topologyHubSpoke = "hub-spoke"
)

// Controller reconciles one network at a time per key. Every action reads
// authoritative state before it writes.
type Controller struct {
	networks pb.GatewayNetworkServiceClient
	gateways pb.GatewayServiceClient
	identity control.GatewayIdentityServiceClient
}

func New(networks pb.GatewayNetworkServiceClient, gateways pb.GatewayServiceClient, identity control.GatewayIdentityServiceClient) (*Controller, error) {
	if networks == nil || gateways == nil || identity == nil {
		return nil, errors.New("GatewayNetwork controller dependencies are required")
	}
	return &Controller{networks: networks, gateways: gateways, identity: identity}, nil
}

// Run connects domain state and actions to the generated controller runtime.
func (c *Controller) Run(ctx context.Context) error {
	return c.RunWithMetrics(ctx, nil)
}

// RunWithMetrics connects optional generated diagnostics to the controller.
func (c *Controller) RunWithMetrics(ctx context.Context, metrics *runtime.Metrics) error {
	return runtime.RunKeyedWatch(ctx, runtime.Source[string]{Watch: c.watch, Scan: c.seed}, c.reconcile, runtime.KeyedWatchOptions{
		ReconnectDelay: time.Second,
		KeyedOptions: runtime.KeyedOptions{
			Metrics:  metrics,
			Capacity: QueueCapacity, Workers: Workers, ResyncInterval: ResyncInterval,
			Timeout: ReconcileTimeout, RetryMin: time.Second, RetryMax: 10 * time.Second,
			Terminal: func(err error) bool {
				return errors.Is(err, runtime.ErrObservationContract) || errors.Is(err, runtime.ErrScanContract) || status.Code(err) == codes.PermissionDenied || status.Code(err) == codes.Unauthenticated
			},
		},
	})
}

func (c *Controller) watch(ctx context.Context) (func() (string, error), error) {
	stream, err := c.networks.WatchGatewayNetworks(ctx, &pb.WatchGatewayNetworksRequest{})
	if err != nil {
		return nil, err
	}
	if _, err := stream.Header(); err != nil {
		return nil, err
	}
	return func() (string, error) {
		event, err := stream.Recv()
		if err != nil {
			return "", err
		}
		if event.GetResourceId() == "" {
			return "", errors.New("GatewayNetwork watch returned an empty ID")
		}
		return event.GetResourceId(), nil
	}, nil
}

// The seed enumerates retained networks so a restart recovers missed events.
func (c *Controller) seed(ctx context.Context, enqueue func(string) error) error {
	for page := int32(1); ; page++ {
		call, stop := context.WithTimeout(ctx, ReconcileTimeout)
		result, err := c.networks.ListGatewayNetworks(call, &pb.ListGatewayNetworksRequest{Page: page, Size: 100})
		stop()
		if err != nil {
			return err
		}
		if result == nil || len(result.GetItems()) > 100 {
			return errors.New("invalid GatewayNetwork catalog")
		}
		for _, item := range result.GetItems() {
			id := item.GetMetadata().GetId()
			if id == "" {
				return errors.New("GatewayNetwork catalog has invalid identity")
			}
			if err := enqueue(id); err != nil {
				return err
			}
		}
		if len(result.GetItems()) < 100 {
			return nil
		}
	}
}

func (c *Controller) reconcile(ctx context.Context, id string) error {
	response, err := c.networks.GetGatewayNetwork(ctx, &pb.GetGatewayNetworkRequest{Id: id})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			// A deletion event is terminal: the network owns no cluster
			// resources that need cleanup.
			return nil
		}
		return err
	}
	network := response.GetGatewayNetwork()
	if network.GetMetadata().GetId() != id {
		return errors.New("GatewayNetwork response does not match the request")
	}
	desired, err := c.validate(ctx, network)
	if err != nil {
		return err
	}
	if network.GetStatus() == desired {
		// No redundant status write.
		return nil
	}
	version := network.GetMetadata().GetUpdatedAt().AsTime().Unix()
	if version < 1 {
		return errors.New("GatewayNetwork state has no resource version")
	}
	writeContext, err := rpc.WithResourceVersion(ctx, version)
	if err != nil {
		return err
	}
	_, err = c.identity.ObserveGatewayNetworkStatus(writeContext, &control.ObserveGatewayNetworkStatusRequest{Id: id, Status: desired})
	if status.Code(err) == codes.Aborted {
		// The network changed during reconciliation. Retry from current state.
		return err
	}
	return err
}

// validate applies the network's structural and referential coherence rules
// and returns the deterministic desired status. A definitive not-found for the
// hub gateway is a deterministic Invalid, not an error; a transient lookup
// failure is surfaced so the action retries.
func (c *Controller) validate(ctx context.Context, network *pb.GatewayNetwork) (string, error) {
	invalid := func(reason string) string {
		return fmt.Sprintf("%s: %s", statusInvalid, reason)
	}
	topology := network.GetTopology()
	switch topology {
	case "":
		return invalid("topology is required"), nil
	case topologyMesh, topologyHubSpoke:
	default:
		return invalid(fmt.Sprintf("unrecognized topology %q", topology)), nil
	}
	hubID := network.GetHubGatewayId()
	if topology == topologyHubSpoke && hubID == "" {
		return invalid("hub-spoke network requires a hub_gateway_id"), nil
	}
	if hubID != "" {
		if _, err := c.gateways.GetGateway(ctx, &pb.GetGatewayRequest{Id: hubID}); err != nil {
			if status.Code(err) == codes.NotFound {
				return invalid(fmt.Sprintf("hub gateway %q does not exist", hubID)), nil
			}
			return "", err
		}
	}
	return statusValid, nil
}
