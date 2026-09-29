package grpctls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeCert writes a self-signed certificate and key pair for a new
// throwaway CA. The serial number makes each pair distinct.
func writeCert(t *testing.T, dir string, serial int64) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber:          big.NewInt(serial),
		Subject:               pkix.Name{CommonName: "grpctls-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}

func TestWrapRequiresReadableFiles(t *testing.T) {
	if _, err := Wrap(nil, Config{}); err == nil {
		t.Fatal("empty configuration was accepted")
	}
	if _, err := Wrap(nil, Config{CertFile: "missing.pem", KeyFile: "missing.pem"}); err == nil {
		t.Fatal("unreadable files were accepted")
	}
}

// TestWrapServesInitialCertificateAndReloads drives the wrapped listener
// with a real TLS client. The client must negotiate TLS 1.3 and HTTP/2 ALPN.
// A replaced pair must serve the new certificate; a corrupt replacement must
// keep the previous certificate.
func TestWrapServesInitialCertificateAndReloads(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writeCert(t, dir, 1)
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := Wrap(inner, Config{CertFile: certFile, KeyFile: keyFile})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			// The TLS listener completes the handshake itself; a raw
			// accept loop keeps the connection until the client closes.
			go func() {
				buf := make([]byte, 1)
				for {
					if _, err := conn.Read(buf); err != nil {
						conn.Close()
						return
					}
				}
			}()
		}
	}()
	leaf := func() *x509.Certificate {
		conn, err := tls.Dial("tcp", listener.Addr().String(), &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"h2"}})
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if conn.ConnectionState().Version != tls.VersionTLS13 {
			t.Fatalf("negotiated %v, want TLS 1.3", conn.ConnectionState().Version)
		}
		if conn.ConnectionState().NegotiatedProtocol != "h2" {
			t.Fatalf("negotiated ALPN %q, want h2", conn.ConnectionState().NegotiatedProtocol)
		}
		certs := conn.ConnectionState().PeerCertificates
		if len(certs) != 1 {
			t.Fatalf("server sent %d certificates, want 1", len(certs))
		}
		return certs[0]
	}
	if got := leaf().SerialNumber.Int64(); got != 1 {
		t.Fatalf("initial serial %d, want 1", got)
	}
	// Replace the pair; the stamp check must reload on the next handshake.
	writeCert(t, dir, 2)
	if got := leaf().SerialNumber.Int64(); got != 2 {
		t.Fatalf("reloaded serial %d, want 2", got)
	}
	// Corrupt the pair; the reload must fail and keep the previous pair.
	if err := os.WriteFile(certFile, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := leaf().SerialNumber.Int64(); got != 2 {
		t.Fatalf("failed reload changed the certificate: serial %d, want 2", got)
	}
}
