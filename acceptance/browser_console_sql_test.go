package acceptance

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/url"
	"reflect"
	"strconv"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gatewayworkload"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
	postgres "github.com/jsell-rh/hypershell-stego/out/postgres"
)

// Keep private values in memory for comparison. Never write them to evidence.
type consoleSQLState struct {
	options                       postgres.Options
	sessionKey                    []byte
	namespaceUID, sourceUID       string
	database, login, owner, table string
}

func (w *browserGatewayWorkload) consoleSQLState(id string) consoleSQLState {
	w.t.Helper()
	ns, err := gatewayworkload.Namespace(id)
	if err != nil {
		w.t.Fatal(err)
	}
	state, err := gatewayworkload.ConsoleStateNamespace(id)
	if err != nil {
		w.t.Fatal(err)
	}
	names, err := postgres.DatabaseNames(postgres.DatabaseKey{Scope: w.f.cluster, Resource: "console:" + id})
	if err != nil {
		w.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	read := func(path string) kube.Object {
		object, code, err := w.kubernetes.Request(ctx, "GET", path, nil)
		if err != nil || code != 200 || kube.String(object, "metadata", "uid") == "" {
			w.t.Fatal("console SQL fixture object is unavailable", code)
		}
		return object
	}
	value := func(object kube.Object, field string) []byte {
		data, err := base64.StdEncoding.DecodeString(kube.String(object, "data", field))
		if err != nil || len(data) == 0 {
			w.t.Fatal("console SQL fixture field is unavailable")
		}
		return data
	}
	namespace := read("/api/v1/namespaces/" + state)
	source := read("/api/v1/namespaces/" + state + "/secrets/gateway-console-state")
	files := read("/api/v1/namespaces/" + ns + "/secrets/hypershell-gateway-console-files")
	address, err := url.Parse(string(value(files, "database-url")))
	if err != nil || address.Scheme != "postgresql" || address.User == nil {
		w.t.Fatal("console SQL URL is invalid")
	}
	password, hasPassword := address.User.Password()
	port, err := strconv.ParseUint(address.Port(), 10, 16)
	if err != nil || port == 0 || !hasPassword || address.User.Username() != names.User || address.Path != "/"+names.Database || password != string(value(source, "database-password")) || address.Hostname() != w.databaseOptions.Host || uint16(port) != w.databaseOptions.Port || address.Query().Get("sslmode") != "verify-full" || address.Query().Get("sslrootcert") != "/var/run/stego/database-ca.pem" {
		w.t.Fatal("console SQL credential differs from its assigned identity")
	}
	ca := value(files, "database-ca.pem")
	key, err := base64.StdEncoding.DecodeString(string(value(files, "session-key")))
	if err != nil || len(key) != 32 || !bytes.Equal(key, value(source, "session-key")) || !bytes.Equal(ca, w.databaseOptions.CA) {
		w.t.Fatal("console session key or SQL trust differs from retained state")
	}
	server, err := postgres.DatabaseServerIdentity(ctx, w.databaseOptions)
	if err != nil || server != string(value(source, "database-server")) {
		w.t.Fatal("console SQL server identity changed")
	}
	result := consoleSQLState{
		options:    postgres.Options{Host: address.Hostname(), Port: uint16(port), User: names.User, Password: password, Database: names.Database, CA: ca},
		sessionKey: key, namespaceUID: kube.String(namespace, "metadata", "uid"), sourceUID: kube.String(source, "metadata", "uid"),
	}
	var secure, limited, schema bool
	err = postgres.ReadRow(ctx, result.options, `SELECT s.ssl,
		NOT(r.rolsuper OR r.rolcreatedb OR r.rolcreaterole OR r.rolreplication OR r.rolbypassrls OR r.rolinherit)
		AND r.rolcanlogin AND r.rolconnlimit=32
		AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_auth_members m WHERE m.member=r.oid),
		d.datdba=own.oid AND c.relowner=own.oid AND NOT own.rolcanlogin AND own.oid<>r.oid AND d.datconnlimit=32
		AND NOT pg_catalog.has_database_privilege(r.oid,d.oid,'CREATE')
		AND NOT pg_catalog.has_database_privilege(r.oid,d.oid,'TEMP')
		AND NOT pg_catalog.has_schema_privilege(r.oid,'public','CREATE')
		AND NOT pg_catalog.has_table_privilege(r.oid,c.oid,'TRUNCATE')
		AND pg_catalog.has_table_privilege(r.oid,c.oid,'SELECT')
		AND pg_catalog.has_table_privilege(r.oid,c.oid,'INSERT')
		AND pg_catalog.has_table_privilege(r.oid,c.oid,'UPDATE')
		AND pg_catalog.has_table_privilege(r.oid,c.oid,'DELETE'),
		d.oid::text,r.oid::text,own.oid::text,c.oid::text
		FROM pg_catalog.pg_roles r JOIN pg_catalog.pg_database d ON d.datname=current_database()
		JOIN pg_catalog.pg_roles own ON own.rolname=$1
		JOIN pg_catalog.pg_class c ON c.oid='public.stego_browser_sessions'::regclass
		JOIN pg_catalog.pg_stat_ssl s ON s.pid=pg_backend_pid() WHERE r.rolname=current_user`,
		[]any{names.Owner}, &secure, &limited, &schema, &result.database, &result.login, &result.owner, &result.table)
	if err != nil || !secure || !limited || !schema {
		w.t.Fatal("console SQL TLS, ownership, or runtime grants differ")
	}
	return result
}

func (w *browserGatewayWorkload) checkConsoleSQLIsolation() {
	w.t.Helper()
	if w.public == nil {
		return
	}
	for _, id := range w.gatewayIDs {
		current := w.consoleSQLState(id)
		databases := []string{w.databaseOptions.Database, "postgres"}
		for _, other := range w.gatewayIDs {
			native := databaseNames(w.t, w.f.cluster, other)
			databases = append(databases, native.Database)
			if other != id {
				console, err := postgres.DatabaseNames(postgres.DatabaseKey{Scope: w.f.cluster, Resource: "console:" + other})
				if err != nil {
					w.t.Fatal(err)
				}
				databases = append(databases, console.Database)
			}
		}
		for _, database := range databases {
			options := current.options
			options.Database = database
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			var one int
			err := postgres.ReadRow(ctx, options, "SELECT 1", nil, &one)
			cancel()
			var failure *postgres.Error
			if !errors.As(err, &failure) || failure.Stage != "connect" || failure.SQLState != "42501" {
				w.t.Fatal("console SQL cross-database access was not denied")
			}
		}
	}
}

func (w *browserGatewayWorkload) checkConsoleSQLRecovery() func() {
	w.t.Helper()
	before := map[string]consoleSQLState{}
	if w.public != nil {
		for _, id := range w.gatewayIDs {
			before[id] = w.consoleSQLState(id)
		}
	}
	return func() {
		w.t.Helper()
		for id, original := range before {
			if !reflect.DeepEqual(original, w.consoleSQLState(id)) {
				w.t.Fatal("recovery changed console SQL objects, credentials, or retained session keys")
			}
		}
	}
}
