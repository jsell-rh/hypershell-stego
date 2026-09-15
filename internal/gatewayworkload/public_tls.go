package gatewayworkload

import (
	"context"
	"crypto/tls"
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
		if o.PublicIssuer != "" || o.PublicCAFile != "" {
			return nil, errors.New("public Gateway TLS requires a domain and issuer")
		}
		return nil, nil
	}
	if len(o.PublicDomain) > 223 || net.ParseIP(o.PublicDomain) != nil || !dnsLabel.MatchString(o.PublicIssuer) {
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
	if !filepath.IsAbs(o.PublicCAFile) {
		return nil, errors.New("public Gateway CA file must be absolute")
	}
	file, err := os.Open(o.PublicCAFile)
	if err != nil {
		return nil, errors.New("public Gateway CA file is unavailable")
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, (512<<10)+1))
	if err != nil || len(raw) > 512<<10 {
		return nil, errors.New("public Gateway CA file is invalid")
	}
	bundle, err := certificateBundle(raw)
	if err != nil {
		return nil, errors.New("public Gateway CA file must contain certificates")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(bundle) {
		return nil, errors.New("public Gateway CA file is invalid")
	}
	return roots, nil
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
	if !owner(id).Matches(secret) || kube.String(secret, "metadata", "uid") == "" || kube.String(secret, "metadata", "resourceVersion") == "" {
		return nil, errors.New("public Gateway TLS Secret identity differs")
	}
	if kube.String(secret, "metadata", "deletionTimestamp") != "" {
		return nil, ErrPending
	}
	certificate, err := data(secret, "tls.crt")
	if err != nil {
		return nil, err
	}
	key, err := data(secret, "tls.key")
	if err != nil {
		return nil, err
	}
	if err := verifyPublicCertificate(certificate, key, host, k.publicRoots); err != nil {
		return nil, err
	}
	return certificate, nil
}

func verifyPublicCertificate(certificate, key []byte, host string, roots *x509.CertPool) error {
	if roots == nil {
		return errors.New("public Gateway trust is unavailable")
	}
	pair, err := tls.X509KeyPair(certificate, key)
	if err != nil {
		return errors.New("public Gateway TLS key does not match its certificate")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return errors.New("public Gateway certificate is invalid")
	}
	intermediates := x509.NewCertPool()
	for _, raw := range pair.Certificate[1:] {
		cert, err := x509.ParseCertificate(raw)
		if err != nil {
			return errors.New("public Gateway certificate chain is invalid")
		}
		intermediates.AddCert(cert)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, DNSName: host, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		return errors.New("public Gateway certificate does not match its hostname or configured trust")
	}
	return nil
}
