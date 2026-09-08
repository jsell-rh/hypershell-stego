package acceptance

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

	"github.com/jsell-rh/hypershell-stego/out/events"
	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"
)

type Config = events.Config

type testIdentity struct {
	config Config
	server *tls.Config
}

func identity(t *testing.T, hostname string) testIdentity {
	t.Helper()
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	rootDER, err := x509.CreateCertificate(rand.Reader, root, root, &rootKey.PublicKey, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	rootPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER})
	makeCert := func(serial int64, client bool) ([]byte, []byte) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		cert := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "Test identity"},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}
		if client {
			cert.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
		} else {
			cert.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
			if hostname == "localhost" {
				cert.IPAddresses = []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
				cert.DNSNames = []string{"localhost"}
			} else {
				cert.DNSNames = []string{hostname}
			}
		}
		der, err := x509.CreateCertificate(rand.Reader, cert, root, &key.PublicKey, rootKey)
		if err != nil {
			t.Fatal(err)
		}
		keyDER, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	}
	serverCert, serverKey := makeCert(2, false)
	clientCert, clientKey := makeCert(3, true)
	pair, err := tls.X509KeyPair(serverCert, serverKey)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(rootPEM)
	directory := t.TempDir()
	for name, data := range map[string][]byte{"ca.pem": rootPEM, "client.pem": clientCert, "key.pem": clientKey} {
		if err := os.WriteFile(filepath.Join(directory, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return testIdentity{
		config: Config{Topic: "events", Authentication: "mtls", CAFile: filepath.Join(directory, "ca.pem"),
			ClientCertificateFile: filepath.Join(directory, "client.pem"), ClientKeyFile: filepath.Join(directory, "key.pem")},
		server: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair},
			ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots},
	}
}

func broker(t *testing.T, identity testIdentity, extra ...kfake.Opt) (*kfake.Cluster, Config) {
	t.Helper()
	options := []kfake.Opt{kfake.NumBrokers(1), kfake.TLS(identity.server), kfake.SeedTopics(1, "events")}
	options = append(options, extra...)
	cluster, err := kfake.NewCluster(options...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cluster.Close)
	config := identity.config
	config.Brokers = cluster.ListenAddrs()
	return cluster, config
}

func kafkaConsumer(t *testing.T, config Config) *kgo.Client {
	t.Helper()
	ca, err := os.ReadFile(config.CAFile)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("invalid test CA")
	}
	pair, err := tls.LoadX509KeyPair(config.ClientCertificateFile, config.ClientKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := kgo.NewClient(kgo.SeedBrokers(config.Brokers...), kgo.DialTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, Certificates: []tls.Certificate{pair}}), kgo.ConsumeTopics(config.Topic), kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(consumer.Close)
	return consumer
}
