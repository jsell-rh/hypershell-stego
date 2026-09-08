package serviceaccounts

import (
	"context"
	"errors"
	"os"

	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/provisioner/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type rpcProvisioner struct {
	client pb.OpenShellGatewayServiceAccountProvisionerServiceClient
}

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
	return &rpcProvisioner{client: pb.NewOpenShellGatewayServiceAccountProvisionerServiceClient(client)}, client.Close, nil
}
func (p *rpcProvisioner) Provision(ctx context.Context, spec Spec) (Credential, error) {
	response, err := p.client.Provision(ctx, &pb.ProvisionRequest{Spec: &pb.ServiceAccountSpec{ClientId: spec.ClientID, DisplayName: spec.DisplayName, GatewayClientId: spec.GatewayClientID, GatewayId: spec.GatewayID, ServiceAccountId: spec.ServiceAccountID, CreatorUserId: spec.CreatorUserID, Role: spec.Role, ExpectedIssuer: spec.ExpectedIssuer, AccessTokenLifetimeSeconds: spec.AccessTokenLifetimeSeconds}})
	if err != nil {
		return Credential{}, ErrUnavailable
	}
	return Credential{ClientID: response.GetClientId(), ClientUUID: response.GetClientUuid(), Subject: response.GetSubject(), Secret: response.GetClientSecret()}, nil
}
func (p *rpcProvisioner) Disable(ctx context.Context, gatewayID, id, clientUUID string) error {
	if clientUUID == "" {
		return p.Delete(ctx, gatewayID, id, "")
	}
	_, err := p.client.Disable(ctx, &pb.DisableRequest{GatewayId: gatewayID, ServiceAccountId: id, ClientUuid: clientUUID})
	return terminalError(err)
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
	return ErrUnavailable
}

func (p *rpcProvisioner) Reconcile(ctx context.Context, spec Spec, clientUUID, subject string) error {
	_, err := p.client.Reconcile(ctx, &pb.ReconcileRequest{Spec: &pb.ServiceAccountSpec{ClientId: spec.ClientID, DisplayName: spec.DisplayName, GatewayClientId: spec.GatewayClientID, GatewayId: spec.GatewayID, ServiceAccountId: spec.ServiceAccountID, CreatorUserId: spec.CreatorUserID, Role: spec.Role, ExpectedIssuer: spec.ExpectedIssuer, AccessTokenLifetimeSeconds: spec.AccessTokenLifetimeSeconds}, ClientUuid: clientUUID, ExpectedSubject: subject, Enabled: true})
	if err != nil {
		return ErrUnavailable
	}
	return nil
}
