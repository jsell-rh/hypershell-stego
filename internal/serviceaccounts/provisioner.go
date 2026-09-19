package serviceaccounts

import (
	"context"
	"errors"
	"os"

	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/provisioner/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type rpcProvisioner struct {
	inventory pb.GatewayAccountInventoryServiceClient
	client    pb.OpenShellGatewayServiceAccountProvisionerServiceClient
}

// This marker permits a bounded repeat of an authorized cleanup action. It
// carries no provider response. Public service behavior still uses ErrUnavailable.
var errRetryableCleanup = errors.New("service-account cleanup can be retried")

type retryableCleanupFailure struct{}

func (retryableCleanupFailure) Error() string        { return ErrUnavailable.Error() }
func (retryableCleanupFailure) Unwrap() error        { return ErrUnavailable }
func (retryableCleanupFailure) Is(target error) bool { return target == errRetryableCleanup }

func ProvisionerFromEnvironment() (Provisioner, func(), error) {
	options := rpc.Options{Address: os.Getenv("HYPERSHELL_SERVICE_ACCOUNT_PROVISIONER_ADDR"), CAFile: os.Getenv("HYPERSHELL_SERVICE_ACCOUNT_PROVISIONER_CA_FILE"), TokenFile: os.Getenv("HYPERSHELL_SERVICE_ACCOUNT_PROVISIONER_TOKEN_FILE")}
	if options.Address == "" {
		if options.CAFile != "" || options.TokenFile != "" {
			return nil, nil, errors.New("provisioner address is required with credentials")
		}
		return nil, func() {}, nil
	}
	client, err := rpc.New(options)
	if err != nil {
		return nil, nil, err
	}
	return &rpcProvisioner{client: pb.NewOpenShellGatewayServiceAccountProvisionerServiceClient(client), inventory: pb.NewGatewayAccountInventoryServiceClient(client)}, client.Close, nil
}
func (p *rpcProvisioner) Provision(ctx context.Context, spec Spec) (Credential, error) {
	response, err := p.client.Provision(ctx, &pb.ProvisionRequest{Spec: &pb.ServiceAccountSpec{ClientId: spec.ClientID, DisplayName: spec.DisplayName, GatewayClientId: spec.GatewayClientID, GatewayId: spec.GatewayID, ServiceAccountId: spec.ServiceAccountID, CreatorUserId: spec.CreatorUserID, Role: spec.Role, ExpectedIssuer: spec.ExpectedIssuer, AccessTokenLifetimeSeconds: spec.AccessTokenLifetimeSeconds}})
	if err != nil {
		return Credential{}, ErrUnavailable
	}
	return Credential{ClientID: response.GetClientId(), ClientUUID: response.GetClientUuid(), Subject: response.GetSubject(), Secret: response.GetClientSecret()}, nil
}
func (p *rpcProvisioner) Revoke(ctx context.Context, gatewayID, id, clientUUID string) error {
	// A delayed update can enable a disabled client. Deletion prevents that
	// update from restoring the identity. The domain retains metadata and audit.
	return p.Delete(ctx, gatewayID, id, clientUUID)
}
func (p *rpcProvisioner) Delete(ctx context.Context, gatewayID, id, clientUUID string) error {
	var err error
	if clientUUID == "" {
		_, err = p.client.DeleteManaged(ctx, &pb.DeleteManagedRequest{GatewayId: gatewayID, ServiceAccountId: id})
	} else {
		_, err = p.client.Delete(ctx, &pb.DeleteRequest{GatewayId: gatewayID, ServiceAccountId: id, ClientUuid: clientUUID})
	}
	return terminalError(err)
}
func terminalError(err error) error {
	if err == nil || status.Code(err) == codes.NotFound {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return ErrUnavailable
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return retryableCleanupFailure{}
	}
	switch status.Code(err) {
	case codes.Aborted, codes.Unavailable, codes.ResourceExhausted, codes.DeadlineExceeded:
		return retryableCleanupFailure{}
	}
	return ErrUnavailable
}

func (p *rpcProvisioner) Reconcile(ctx context.Context, spec Spec, clientUUID, subject string) error {
	_, err := p.client.Reconcile(ctx, &pb.ReconcileRequest{Spec: &pb.ServiceAccountSpec{ClientId: spec.ClientID, DisplayName: spec.DisplayName, GatewayClientId: spec.GatewayClientID, GatewayId: spec.GatewayID, ServiceAccountId: spec.ServiceAccountID, CreatorUserId: spec.CreatorUserID, Role: spec.Role, ExpectedIssuer: spec.ExpectedIssuer, AccessTokenLifetimeSeconds: spec.AccessTokenLifetimeSeconds}, ClientUuid: clientUUID, ExpectedSubject: subject, Enabled: true})
	if err != nil {
		return ErrUnavailable
	}
	return nil
}

func (p *rpcProvisioner) DeleteGateway(ctx context.Context, gatewayID string) error {
	_, err := p.client.DeleteGateway(ctx, &pb.DeleteGatewayRequest{GatewayId: gatewayID})
	return terminalError(err)
}

func (p *rpcProvisioner) InventorySource(ctx context.Context, id string) (string, error) {
	value, err := p.inventory.GetSource(ctx, &pb.GatewayAccountInventorySourceRequest{GatewayId: id})
	if err != nil {
		return "", ErrUnavailable
	}
	return value.GetSourceVersion(), nil
}
func (p *rpcProvisioner) InventoryPage(ctx context.Context, id, version, after string, limit int) (runtime.CursorPage[string], error) {
	result := runtime.CursorPage[string]{}
	if limit < 1 || limit > 100 {
		return result, runtime.ErrScanContract
	}
	value, err := p.inventory.ReadPage(ctx, &pb.GatewayAccountInventoryPageRequest{GatewayId: id, SourceVersion: version, After: after, Limit: int32(limit)})
	if err != nil {
		return result, ErrUnavailable
	}
	if value == nil {
		return result, runtime.ErrScanContract
	}
	if value.GetWindowLimit() {
		if len(value.GetCandidates()) != 0 || value.GetMore() {
			return result, runtime.ErrScanContract
		}
		return result, runtime.ErrScanWindowLimit
	}
	if len(value.GetCandidates()) > limit {
		return result, runtime.ErrScanContract
	}
	for _, item := range value.GetCandidates() {
		if item == nil || !validInventoryProviderID(item.GetProviderId()) {
			return runtime.CursorPage[string]{}, runtime.ErrScanContract
		}
		result.Items = append(result.Items, runtime.CursorItem[string]{Cursor: item.GetCursor(), Value: item.GetProviderId()})
	}
	result.More = value.GetMore()
	return result, nil
}
func (p *rpcProvisioner) PrepareInventoryCandidate(ctx context.Context, id, version, providerID string) (bool, error) {
	value, err := p.inventory.PrepareCandidate(ctx, &pb.PrepareGatewayAccountCandidateRequest{GatewayId: id, SourceVersion: version, ProviderId: providerID})
	if err != nil {
		return false, ErrUnavailable
	}
	return value.GetOwned(), nil
}
