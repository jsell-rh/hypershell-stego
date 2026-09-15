package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
	postgres "github.com/jsell-rh/hypershell-stego/out/postgres"
)

// The test installation supplies PostgreSQL before it starts any controller.
// Only this fixture has server administration rights. Workers get a separate,
// non-superuser provisioning account through their declared file Secret.
func (w *browserGatewayWorkload) prepareDatabase(sessions *fixture) {
	w.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	config, err := pgx.ParseConfig(os.Getenv("STEGO_TEST_POSTGRES_DSN"))
	if err != nil || config.TLSConfig == nil || config.TLSConfig.InsecureSkipVerify || len(config.Fallbacks) != 0 {
		w.t.Fatal("supplied SQL fixture requires verified TLS")
	}
	config.ConnectTimeout = 5 * time.Second
	connection, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		w.t.Fatal("supplied SQL fixture is unavailable")
	}
	defer connection.Close(ctx)
	known := map[string]bool{"postgres": true, "template0": true, "template1": true}
	for _, f := range []*fixture{w.f, sessions} {
		c, e := pgx.ParseConfig(f.dsn)
		if e != nil {
			w.t.Fatal("component database configuration is invalid")
		}
		known[c.Database] = true
	}
	rows, err := connection.Query(ctx, "SELECT datname FROM pg_catalog.pg_database")
	if err != nil {
		w.t.Fatal("cannot inspect the test installation")
	}
	var databases []string
	for rows.Next() {
		var name string
		if rows.Scan(&name) != nil {
			rows.Close()
			w.t.Fatal("cannot inspect the test database")
		}
		if !known[name] {
			rows.Close()
			w.t.Fatal("the supplied SQL fixture has a foreign database")
		}
		databases = append(databases, name)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		w.t.Fatal("test installation inspection failed")
	}
	password, err := postgres.NewDatabasePassword()
	if err != nil {
		w.t.Fatal(err)
	}
	admin := "browser_sql_" + password[:12]
	ledger := admin + "_ledger"
	quote := func(value string) string { return pgx.Identifier{value}.Sanitize() }
	exec := func(query string) {
		w.t.Helper()
		if _, err := connection.Exec(ctx, query); err != nil {
			w.t.Fatal("test installation SQL failed")
		}
	}
	exec("CREATE ROLE " + quote(admin) + " LOGIN NOSUPERUSER CREATEDB CREATEROLE PASSWORD '" + password + "'")
	exec("CREATE DATABASE " + quote(ledger) + " OWNER " + quote(admin))
	// Cleanup is registered before worker cleanup. Workers stop first. The
	// normal workflow must prove SQL deletion before this fallback can run.
	w.t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		conn, err := pgx.ConnectConfig(cleanup, config)
		if err != nil {
			w.t.Error("test SQL cleanup connection failed")
			return
		}
		defer conn.Close(cleanup)
		for _, id := range w.gatewayIDs {
			names, err := postgres.DatabaseNames(postgres.DatabaseKey{Scope: w.f.cluster, Resource: id})
			if err != nil {
				w.t.Error(err)
				continue
			}
			for _, q := range []string{"DROP DATABASE IF EXISTS " + quote(names.Database) + " WITH (FORCE)", "DROP ROLE IF EXISTS " + quote(names.User), "DROP ROLE IF EXISTS " + quote(names.Owner)} {
				if _, err := conn.Exec(cleanup, q); err != nil {
					w.t.Error("owned Gateway SQL fixture cleanup failed")
				}
			}
		}
		for _, q := range []string{"DROP DATABASE " + quote(ledger) + " WITH (FORCE)", "DROP ROLE " + quote(admin)} {
			if _, err := conn.Exec(cleanup, q); err != nil {
				w.t.Error("owned SQL installation cleanup failed")
			}
		}
	})
	databases = append(databases, ledger)
	for _, name := range databases {
		if name != "template0" {
			exec("REVOKE CONNECT,TEMPORARY ON DATABASE " + quote(name) + " FROM PUBLIC")
		}
	}
	ca, err := os.ReadFile(os.Getenv("STEGO_TEST_POSTGRES_CA_FILE"))
	if err != nil {
		w.t.Fatal("supplied SQL CA is unavailable")
	}
	w.databaseOptions = postgres.Options{Host: w.p.host("fixture"), Port: 5432, Database: ledger, User: admin, Password: password, CA: ca}
	// Network plugins can apply egress rules before or after Service address
	// translation. Bind only this fixture's Service and Pod addresses.
	service, code, err := w.kubernetes.Request(ctx, "GET", "/api/v1/namespaces/"+w.p.namespace+"/services/fixture", nil)
	ip := net.ParseIP(kube.String(service, "spec", "clusterIP"))
	if err != nil || code != 200 || ip == nil {
		w.t.Fatal("supplied SQL Service address is unavailable")
	}
	w.databaseEndpoints = append(w.databaseEndpoints, net.JoinHostPort(ip.String(), "5432"))
	podName := os.Getenv("HOSTNAME")
	if podName == "" {
		w.t.Fatal("supplied SQL fixture Pod name is unavailable")
	}
	pod, code, err := w.kubernetes.Request(ctx, "GET", "/api/v1/namespaces/"+w.p.namespace+"/pods/"+podName, nil)
	ip = net.ParseIP(kube.String(pod, "status", "podIP"))
	if err != nil || code != 200 || ip == nil || kube.String(pod, "metadata", "labels", "app") != "stego-fixture" || kube.String(pod, "metadata", "deletionTimestamp") != "" {
		w.t.Fatal("supplied SQL fixture Pod address is unavailable")
	}
	w.databaseEndpoints = append(w.databaseEndpoints, net.JoinHostPort(ip.String(), "5432"))
	w.databaseConfig, err = json.Marshal(map[string]any{"host": w.databaseOptions.Host, "port": w.databaseOptions.Port, "database": ledger, "user": admin, "password": password, "ca": string(ca)})
	if err != nil {
		w.t.Fatal("cannot encode the supplied SQL configuration")
	}
	// Keep a record outside all Gateway databases to prove server retention.
	provision := config.Copy()
	provision.Database, provision.User, provision.Password = ledger, admin, password
	sentinel, err := pgx.ConnectConfig(ctx, provision)
	if err != nil {
		w.t.Fatal("provisioning fixture login failed")
	}
	defer sentinel.Close(ctx)
	if _, err = sentinel.Exec(ctx, "CREATE TABLE public.installation_data(value text NOT NULL); INSERT INTO public.installation_data VALUES ('preserve')"); err != nil {
		w.t.Fatal("installation data setup failed")
	}
	w.requireInstallationData(ctx)
}

func (w *browserGatewayWorkload) requireInstallationData(ctx context.Context) {
	w.t.Helper()
	var preserved string
	err := postgres.ReadRow(ctx, w.databaseOptions, "SELECT value FROM public.installation_data", nil, &preserved)
	if err != nil {
		var failure *postgres.Error
		if errors.As(err, &failure) {
			w.t.Fatal("installation data read failed", failure.Stage, failure.SQLState)
		}
		w.t.Fatal("installation data read failed", err)
	}
	if preserved != "preserve" {
		w.t.Fatal("Gateway deletion changed installation data")
	}
}

func databaseNames(t *testing.T, cluster, id string) postgres.DatabaseIdentity {
	t.Helper()
	names, err := postgres.DatabaseNames(postgres.DatabaseKey{Scope: cluster, Resource: id})
	if err != nil {
		t.Fatal(err)
	}
	return names
}
