package gatewayworkload

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

func publicHostname(namespace, domain string) string { return "gw-" + namespace + "." + domain }

func publicTrust(o Options) (*x509.CertPool, error) {
	if o.PublicDomain == "" {
		if o.PublicIssuer != "" || o.PublicCAFile != "" || o.PublicRouter != "" {
			return nil, errors.New("public Gateway TLS requires a domain and issuer")
		}
		return nil, nil
	}
	if len(o.PublicDomain) > 223 || net.ParseIP(o.PublicDomain) != nil || !dnsLabel.MatchString(o.PublicIssuer) || !dnsLabel.MatchString(o.PublicRouter) {
		return nil, errors.New("public Gateway TLS configuration is invalid")
	}
	for _, label := range strings.Split(o.PublicDomain, ".") {
		if !dnsLabel.MatchString(label) {
			return nil, errors.New("public Gateway domain is invalid")
		}
	}
	if o.PublicCAFile == "" {
		roots, err := x509.SystemCertPool()
		if err != nil || roots == nil {
			return nil, errors.New("public Gateway system trust is unavailable")
		}
		return roots, nil
	}
	return gatewayTrustFile(o.PublicCAFile)
}

func gatewayTrustFile(name string) (*x509.CertPool, error) {
	_, roots, err := gatewayTrustMaterial(name)
	return roots, err
}

func gatewayTrustMaterial(name string) ([]byte, *x509.CertPool, error) {
	if !filepath.IsAbs(name) {
		return nil, nil, errors.New("Gateway CA file must be absolute")
	}
	file, err := os.Open(name)
	if err != nil {
		return nil, nil, errors.New("Gateway CA file is unavailable")
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, (512<<10)+1))
	if err != nil || len(raw) > 512<<10 {
		return nil, nil, errors.New("Gateway CA file is invalid")
	}
	roots, err := kube.ParseServerTLSRoots(raw)
	if err != nil {
		return nil, nil, errors.New("Gateway CA file is invalid")
	}
	return raw, roots, nil
}

// The pinned Gateway image selects this certificate only for the public SNI.
// Internal clients retain their separate certificate and private CA trust.
func (k *Kubernetes) ensurePublicTLS(ctx context.Context, gw *pb.Gateway) ([]byte, error) {
	if k.options.PublicDomain == "" {
		return nil, nil
	}
	if k.publicRoots == nil {
		return nil, errors.New("public Gateway trust is unavailable")
	}
	id, ns := gw.GetMetadata().GetId(), gw.GetNamespace()
	host := publicHostname(ns, k.options.PublicDomain)
	cert := definition("cert-manager.io/v1", "Certificate", "openshell-public-tls", id)
	cert["spec"] = object{"secretName": "openshell-public-tls", "issuerRef": object{"name": k.options.PublicIssuer, "kind": "ClusterIssuer"}, "dnsNames": []string{host}, "privateKey": object{"algorithm": "ECDSA", "size": 256, "rotationPolicy": "Always"}, "usages": []string{"server auth"}, "duration": "2160h", "renewBefore": "360h", "secretTemplate": object{"labels": object{ownerLabel: id, managerLabel: manager}}}
	if _, err := k.ensure(ctx, "/apis/cert-manager.io/v1/namespaces/"+ns+"/certificates", cert, id); err != nil {
		return nil, err
	}
	secret, code, err := k.client.Request(ctx, http.MethodGet, "/api/v1/namespaces/"+ns+"/secrets/openshell-public-tls", nil)
	if err != nil {
		return nil, err
	}
	if code == http.StatusNotFound {
		return nil, ErrPending
	}
	if kube.String(secret, "metadata", "deletionTimestamp") != "" {
		return nil, ErrPending
	}
	return kube.VerifyServerTLSSecret(secret, owner(id), kube.ServerTLSSecretTarget{
		Namespace: ns, Name: "openshell-public-tls", DNSName: host, Roots: k.publicRoots,
	})
}
