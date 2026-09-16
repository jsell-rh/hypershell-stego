package gatewayworkload

import (
	"context"
	"crypto/sha256"
	"encoding/pem"
	"errors"

	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	protocol "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (k *Kubernetes) DesiredEndpoint(gw *pb.Gateway) *string {
	if k.options.PublicDomain != "" {
		address := "https://" + publicHostname(gw.GetNamespace(), k.options.PublicDomain)
		return &address
	}
	if gw.GetRouteAddress() != "" {
		empty := ""
		return &empty
	}
	return nil
}

func (k *Kubernetes) ensurePublicRoute(ctx context.Context, gw *pb.Gateway, certificate []byte, probe func(context.Context, string, []byte) error) error {
	id, ns := gw.GetMetadata().GetId(), gw.GetNamespace()
	collection := "/apis/route.openshift.io/v1/namespaces/" + ns + "/routes"
	path := collection + "/" + Name
	if k.options.PublicDomain == "" {
		// Check even after a failed observation has cleared the API address.
		// A submitted deletion does not prove that the Route is absent.
		absent, err := k.client.DeleteOwned(ctx, path, owner(id))
		if err != nil {
			return err
		}
		if !absent {
			return ErrPending
		}
		return nil
	}
	host := publicHostname(ns, k.options.PublicDomain)
	if probe == nil {
		return errors.New("public Gateway probe is unavailable")
	}
	ready, err := k.client.EnsurePassthroughRoute(ctx, ns, Name, owner(id), kube.PassthroughRouteTarget{Host: host, Service: Name, Port: "grpc", Router: k.options.PublicRouter}, func(ctx context.Context, address string) error { return probe(ctx, address, certificate) })
	if err != nil {
		return err
	}
	if !ready {
		return ErrPending
	}
	return nil
}

func (k *Kubernetes) probePublicGateway(ctx context.Context, address string, certificate []byte) error {
	// The generated Secret verifier has already checked every PEM block,
	// certificate, key, hostname, and trust anchor before this method runs.
	leaf, _ := pem.Decode(certificate)
	if leaf == nil || leaf.Type != "CERTIFICATE" {
		return errors.New("public Gateway certificate is unavailable")
	}
	probe, err := rpc.NewTLSProbe(rpc.TLSProbeOptions{Address: address, Roots: k.publicRoots, PeerCertificateSHA256: sha256.Sum256(leaf.Bytes)})
	if err != nil {
		return errors.New("public Gateway probe configuration is invalid")
	}
	defer probe.Close()
	var health protocol.HealthResponse
	if err := probe.Invoke(ctx, "/openshell.v1.OpenShell/Health", &protocol.HealthRequest{}, &health); err != nil {
		return publicProbeFailure(err)
	}
	if health.GetStatus() != protocol.ServiceStatus_SERVICE_STATUS_HEALTHY || health.GetVersion() == "" || len(health.GetVersion()) > 128 {
		return errors.New("public Gateway health check failed")
	}
	if err := probe.Invoke(ctx, "/openshell.v1.OpenShell/GetCurrentUser", &protocol.GetCurrentUserRequest{}, &protocol.GetCurrentUserResponse{}); status.Code(err) != codes.Unauthenticated {
		return publicProbeFailure(err)
	}
	return nil
}

func publicProbeFailure(err error) error {
	switch status.Code(err) {
	case codes.Canceled:
		return context.Canceled
	case codes.DeadlineExceeded:
		return context.DeadlineExceeded
	default:
		return errors.New("public Gateway probe failed")
	}
}
