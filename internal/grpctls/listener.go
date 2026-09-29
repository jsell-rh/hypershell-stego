// Package grpctls wraps a listener with a TLS layer that reloads the
// certificate and key files before each handshake. A failed reload keeps
// the previous certificate. Application-owned listeners use this wrapper;
// the generated runtime keeps its own listener configuration.
package grpctls

import (
	"crypto/tls"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"
)

// Config names the certificate and key files. Both files must be readable
// at construction time.
type Config struct {
	CertFile string
	KeyFile  string
}

// Wrap returns a TLS listener over inner. Every handshake checks whether
// the certificate or key file changed and reloads both files as one unit.
// ALPN advertises HTTP/2 only. The minimum version is TLS 1.3, the version
// the generated runtime pins.
func Wrap(inner net.Listener, cfg Config) (net.Listener, error) {
	reloader, err := newReloader(cfg)
	if err != nil {
		return nil, err
	}
	return tls.NewListener(inner, &tls.Config{
		GetCertificate: reloader.getCertificate,
		MinVersion:     tls.VersionTLS13,
		NextProtos:     []string{"h2"},
	}), nil
}

func newReloader(cfg Config) (*certReloader, error) {
	if cfg.CertFile == "" || cfg.KeyFile == "" {
		return nil, errMissingFiles
	}
	reloader := &certReloader{certFile: cfg.CertFile, keyFile: cfg.KeyFile}
	if err := reloader.reload(); err != nil {
		return nil, err
	}
	return reloader, nil
}

type certReloader struct {
	certFile string
	keyFile  string

	mux   sync.Mutex
	cert  *tls.Certificate
	stamp fileStamp
}

func (r *certReloader) getCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.mux.Lock()
	defer r.mux.Unlock()
	if stamp, err := r.currentStamp(); err != nil {
		slog.Warn("Gateway TLS certificate check failed; keeping the previous certificate", "cert", r.certFile, "key", r.keyFile, "error", err)
	} else if stamp != r.stamp {
		if err := r.reload(); err != nil {
			slog.Warn("Gateway TLS certificate reload failed; keeping the previous certificate", "cert", r.certFile, "key", r.keyFile, "error", err)
		} else {
			slog.Info("Gateway TLS certificate reloaded", "cert", r.certFile, "key", r.keyFile)
		}
	}
	return r.cert, nil
}

func (r *certReloader) reload() error {
	cert, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
	if err != nil {
		return err
	}
	stamp, err := r.currentStamp()
	if err != nil {
		return err
	}
	r.cert = &cert
	r.stamp = stamp
	return nil
}

// fileStamp captures the modification time and size of both files. A change
// in either value triggers a reload.
type fileStamp struct {
	modTime time.Time
	size    int64
}

func (r *certReloader) currentStamp() (fileStamp, error) {
	certInfo, err := os.Stat(r.certFile)
	if err != nil {
		return fileStamp{}, err
	}
	keyInfo, err := os.Stat(r.keyFile)
	if err != nil {
		return fileStamp{}, err
	}
	return fileStamp{modTime: certInfo.ModTime(), size: certInfo.Size() + keyInfo.Size()}, nil
}

var errMissingFiles = &configError{}

type configError struct{}

func (*configError) Error() string { return "grpctls requires a certificate file and a key file" }
