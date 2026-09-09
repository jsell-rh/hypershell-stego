// Package gatewayidentity reconciles Gateway login configuration through public
// generated transports. It does not deploy Gateway workloads.
package gatewayidentity

import (
	"context"
	"errors"
	"log/slog"
	"time"

	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const QueueCapacity = 1024
const ResyncInterval = 30 * time.Second
const ReconcileTimeout = 20 * time.Second

type Provider interface {
	EnsureGateway(context.Context, string, string) (string, error)
	DeleteGateway(context.Context, string) error
	GatewayIDs(context.Context) ([]string, error)
	ReconcileGatewayUser(context.Context, string, string, string, string) error
}
type Controller struct {
	gateways  pb.GatewayServiceClient
	state     control.GatewayIdentityServiceClient
	provider  Provider
	userScans map[string]userScan
}

func New(gateways pb.GatewayServiceClient, state control.GatewayIdentityServiceClient, provider Provider) (*Controller, error) {
	if gateways == nil || state == nil || provider == nil {
		return nil, errors.New("Gateway controller dependencies are required")
	}
	return &Controller{gateways: gateways, state: state, provider: provider, userScans: make(map[string]userScan)}, nil
}

// Run connects domain state and actions to the generated controller runtime.
func (c *Controller) Run(ctx context.Context) error {
	return runtime.Run(ctx, runtime.Source[string]{Watch: c.watch, Scan: c.seed}, c.reconcile, runtime.Options{
		QueueCapacity: QueueCapacity, ResyncInterval: ResyncInterval,
		ReconcileTimeout: ReconcileTimeout, ReconnectDelay: time.Second,
		Terminal: func(err error) bool {
			return status.Code(err) == codes.PermissionDenied || status.Code(err) == codes.Unauthenticated
		},
		Observe: func(event runtime.Event) {
			switch event.Phase {
			case "watch_started":
				slog.Info("Gateway identity watch started")
			case "scan_completed":
				slog.Info("Gateway identity scan completed")
			case "reconnect":
				slog.Warn("Gateway identity watch will reconnect")
			case "reconcile_failed":
				slog.Warn("Gateway identity needs another pass")
			}
		},
	})
}
func (c *Controller) watch(ctx context.Context) (func() (string, error), error) {
	stream, err := c.gateways.WatchGateways(ctx, &pb.WatchGatewaysRequest{})
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
			return "", errors.New("Gateway watch returned an empty ID")
		}
		return event.GetResourceId(), nil
	}, nil
}
func (c *Controller) seed(ctx context.Context, enqueue func(string) error) error {
	// Small pages keep responses within the generated message limit. The scan
	// has a fixed resource bound. Capacity beyond this limit requires measurement.
	for page := int32(1); page <= 10000; page++ {
		response, err := c.gateways.ListGateways(ctx, &pb.ListGatewaysRequest{Page: page, Size: 1})
		if err != nil {
			return err
		}
		if len(response.GetItems()) == 0 {
			break
		}
		if len(response.GetItems()) != 1 {
			return errors.New("invalid Gateway list size")
		}
		id := response.Items[0].GetMetadata().GetId()
		if id == "" {
			return errors.New("Gateway list returned an empty ID")
		}
		if err := enqueue(id); err != nil {
			return err
		}
		if page == 10000 {
			return errors.New("Gateway scan exceeds its limit")
		}
	}
	ids, err := c.provider.GatewayIDs(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := enqueue(id); err != nil {
			return err
		}
	}
	return nil
}
func (c *Controller) reconcile(ctx context.Context, id string) error {
	state, err := c.state.GetGatewayIdentityState(ctx, &control.GetGatewayIdentityStateRequest{Id: id})
	if err != nil {
		return err
	}
	gateway := state.GetGateway()
	if gateway.GetMetadata().GetId() != id {
		return errors.New("Gateway state does not match the request")
	}
	if state.GetDeleted() {
		delete(c.userScans, id)
		return c.provider.DeleteGateway(ctx, id)
	}
	oidc, err := c.provider.EnsureGateway(ctx, id, gateway.GetName())
	if err != nil {
		return err
	}
	if gateway.GetOidc() != oidc {
		if _, err = c.gateways.UpdateGateway(ctx, &pb.UpdateGatewayRequest{Id: id, Oidc: &oidc}); err != nil {
			return err
		}
	}
	return c.reconcileUsers(ctx, id)
}

type userScan struct {
	page   int32
	offset int
}

// The cursor retains progress when one pass reaches its time limit.
// Each provider write still requires a fresh, matching API state.
func (c *Controller) reconcileUsers(ctx context.Context, id string) error {
	cursor := c.userScans[id]
	if cursor.page == 0 {
		cursor.page = 1
	}
	var failures []error
	for ; cursor.page <= 100; cursor.page++ {
		result, err := c.state.ListGatewayIdentityUsers(ctx, &control.ListGatewayIdentityUsersRequest{GatewayId: id, Page: cursor.page})
		if err != nil {
			return err
		}
		if result == nil || len(result.UserIds) > 100 || (result.HasMore && len(result.UserIds) == 0) {
			return errors.New("invalid Gateway user page")
		}
		seen := map[string]bool{}
		for i, userID := range result.UserIds {
			if userID == "" {
				return errors.New("Gateway user page has an empty ID")
			}
			duplicate := seen[userID]
			seen[userID] = true
			if i < cursor.offset || duplicate {
				continue
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			state, err := c.state.GetGatewayIdentityUser(ctx, &control.GetGatewayIdentityUserRequest{GatewayId: id, UserId: userID})
			if err == nil && (state == nil || state.GatewayId != id || state.UserId != userID || state.Issuer == "" || state.Subject == "") {
				err = errors.New("Gateway user state does not match the request")
			}
			if err == nil {
				err = c.provider.ReconcileGatewayUser(ctx, id, state.Issuer, state.Subject, state.Role)
			}
			cursor.offset = i + 1
			if ctx.Err() != nil && err != nil {
				cursor.offset = i
			}
			c.userScans[id] = cursor
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				failures = append(failures, err)
			}
		}
		if !result.HasMore {
			delete(c.userScans, id)
			return errors.Join(failures...)
		}
		cursor.offset = 0
		c.userScans[id] = userScan{page: cursor.page + 1}
	}
	delete(c.userScans, id)
	return errors.New("Gateway user scan exceeds 10000 grant references")
}
