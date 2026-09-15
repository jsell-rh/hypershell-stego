package acceptance

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"os"
	"regexp"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

// The operator supplies this private file only for the CNPG installation test.
// It is not an API resource or a Gateway controller configuration file.
type browserCNPGFixture struct {
	Namespace    string `json:"namespace"`
	NamespaceUID string `json:"namespace_uid"`
	Cluster      string `json:"cluster"`
	ClusterUID   string `json:"cluster_uid"`
	Password     string `json:"password"`
	CA           string `json:"ca"`
}

func (w *browserGatewayWorkload) sqlFixtureConfig() *pgx.ConnConfig {
	w.t.Helper()
	if w.sqlFixture != nil {
		return w.sqlFixture.Copy()
	}
	config, err := pgx.ParseConfig(os.Getenv("STEGO_TEST_POSTGRES_DSN"))
	if err != nil || config.TLSConfig == nil || config.TLSConfig.InsecureSkipVerify || config.TLSConfig.RootCAs == nil || len(config.Fallbacks) != 0 || config.Host != "127.0.0.1" {
		w.t.Fatal("SQL fixture requires verified loopback TLS")
	}
	config.ConnectTimeout = 5 * time.Second
	path := os.Getenv("STEGO_TEST_GATEWAY_SQL_FIXTURE_FILE")
	if path == "" {
		w.sqlFixture = config
		return config.Copy()
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		w.t.Fatal("CNPG fixture file is unavailable")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 131072 || info.Mode().Perm()&0137 != 0 {
		w.t.Fatal("CNPG fixture file has an invalid type, size, or mode")
	}
	raw, err := io.ReadAll(io.LimitReader(file, 131073))
	if err != nil || len(raw) > 131072 {
		w.t.Fatal("CNPG fixture file exceeds its bound")
	}
	defer clear(raw)
	var fixture browserCNPGFixture
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&fixture) != nil || decoder.Decode(new(any)) != io.EOF ||
		!regexp.MustCompile(`^stego-cnpg-database-[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`).MatchString(fixture.Namespace) || len(fixture.Namespace) > 63 ||
		fixture.Cluster != "gateway-database" || fixture.NamespaceUID == "" || fixture.ClusterUID == "" || len(fixture.Password) < 32 || len(fixture.Password) > 256 {
		w.t.Fatal("CNPG fixture declaration is invalid")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(fixture.CA)) {
		w.t.Fatal("CNPG fixture CA is invalid")
	}
	w.cnpgFixture = &fixture
	w.requireCNPGInstallation()
	config.Host = fixture.Cluster + "-rw." + fixture.Namespace + ".svc"
	config.Port, config.Database, config.User, config.Password = 5432, "postgres", "postgres", fixture.Password
	config.TLSConfig = &tls.Config{RootCAs: roots, ServerName: config.Host, MinVersion: tls.VersionTLS12}
	config.Fallbacks = nil
	w.sqlFixture = config
	return config.Copy()
}

func (w *browserGatewayWorkload) requireCNPGInstallation() kube.Object {
	w.t.Helper()
	fixture := w.cnpgFixture
	if fixture == nil {
		w.t.Fatal("CNPG fixture is not selected")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	namespace, code, err := w.kubernetes.Request(ctx, "GET", "/api/v1/namespaces/"+fixture.Namespace, nil)
	if err != nil || code != 200 || kube.String(namespace, "metadata", "uid") != fixture.NamespaceUID ||
		kube.String(namespace, "metadata", "labels", "stego.test/cnpg-run") != w.p.namespace ||
		kube.String(namespace, "metadata", "labels", "stego.dev/allocator") != "" || kube.String(namespace, "metadata", "deletionTimestamp") != "" {
		w.t.Fatal("CNPG installation namespace identity changed")
	}
	cluster, code, err := w.kubernetes.Request(ctx, "GET", "/apis/postgresql.cnpg.io/v1/namespaces/"+fixture.Namespace+"/clusters/"+fixture.Cluster, nil)
	if err != nil || code != 200 || kube.String(cluster, "metadata", "uid") != fixture.ClusterUID ||
		kube.String(cluster, "metadata", "labels", "stego.test/cnpg-run") != w.p.namespace || kube.String(cluster, "metadata", "deletionTimestamp") != "" {
		w.t.Fatal("CNPG installation Cluster identity changed")
	}
	return cluster
}
