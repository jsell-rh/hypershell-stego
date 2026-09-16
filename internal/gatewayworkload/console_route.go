package gatewayworkload

import (
	"context"
	"crypto/sha256"
	"encoding/pem"
	"errors"
	"net"
	"net/http"
	"strings"

	transport "github.com/jsell-rh/hypershell-stego/out/application/client"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

func (k *Kubernetes) ensureConsolePublicRoute(ctx context.Context, gw *pb.Gateway, version int64) error {
	origin, err := k.consoleOrigin(gw, version)
	if err != nil {
		return err
	}
	id, ns := gw.GetMetadata().GetId(), gw.GetNamespace()
	host := strings.TrimPrefix(origin, "https://")
	current, code, err := k.client.Request(ctx, http.MethodGet, "/api/v1/namespaces/"+ns+"/secrets/"+consoleName+"-tls", nil)
	if err != nil {
		return err
	}
	if code == http.StatusNotFound {
		return ErrPending
	}
	if code != http.StatusOK {
		return errors.New("console TLS read failed")
	}
	certificate, err := kube.VerifyServerTLSSecret(current, consoleOwner(id), kube.ServerTLSSecretTarget{Namespace: ns, Name: consoleName + "-tls", DNSName: host, Roots: k.publicRoots})
	if err != nil {
		return err
	}
	ready, err := k.client.EnsurePassthroughRoute(ctx, ns, consoleName, consoleOwner(id), kube.PassthroughRouteTarget{Host: host, Service: consoleName, Port: "https", Router: k.options.PublicRouter}, func(ctx context.Context, address string) error {
		return k.probePublicConsole(ctx, address, certificate)
	})
	if err != nil {
		return err
	}
	if !ready {
		return ErrPending
	}
	return nil
}

// This probe checks the generated browser boundary through its public TLS
// listener. The selected leaf pin also binds it to the controller certificate.
// Authenticated dashboard behavior requires the separate rendered workflow.
func (k *Kubernetes) probePublicConsole(ctx context.Context, address string, certificate []byte) error {
	failure := errors.New("public console probe failed")
	leaf, _ := pem.Decode(certificate)
	if leaf == nil || leaf.Type != "CERTIFICATE" {
		return failure
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return failure
	}
	authority := address
	if port == "443" {
		authority = host
	}
	pin := sha256.Sum256(leaf.Bytes)
	client, err := transport.New(transport.Options{BaseURL: "https://" + authority, CAFile: k.options.PublicCAFile, PeerCertificateSHA256: &pin})
	if err != nil {
		return failure
	}
	defer client.Close()
	for _, check := range []struct {
		path   string
		status int
		body   string
	}{
		{"/readyz", http.StatusOK, "ok\n"},
		{"/auth/session", http.StatusOK, "{\"authenticated\":false,\"roles\":[]}\n"},
		{"/api/v1/readyz", http.StatusUnauthorized, ""},
	} {
		response, err := client.Do(ctx, http.MethodGet, check.path, nil, nil)
		if err != nil {
			if ctx != nil && ctx.Err() != nil {
				return ctx.Err()
			}
			return failure
		}
		if response.StatusCode != check.status || (check.body != "" && string(response.Body) != check.body) {
			return failure
		}
	}
	return nil
}
