package acceptance

import (
	"context"
	"encoding/base64"
	"errors"
	"strconv"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gatewayworkload"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
	postgres "github.com/jsell-rh/hypershell-stego/out/postgres"
)

// Read only the credentials published to the Gateway and its retained state.
// Evidence contains object UIDs, never credentials.
func (w *browserGatewayWorkload) sqlOptions(id string) (postgres.Options, map[string]string) {
	w.t.Helper()
	ns, err := gatewayworkload.Namespace(id)
	if err != nil {
		w.t.Fatal(err)
	}
	state, err := gatewayworkload.StateNamespace(id)
	if err != nil {
		w.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	read := func(path string) kube.Object {
		object, code, err := w.kubernetes.Request(ctx, "GET", path, nil)
		if err != nil || code != 200 || kube.String(object, "metadata", "uid") == "" {
			w.t.Fatal("Gateway SQL fixture object is unavailable", code)
		}
		return object
	}
	value := func(object kube.Object, field string) string {
		data, err := base64.StdEncoding.DecodeString(kube.String(object, "data", field))
		if err != nil || len(data) == 0 {
			w.t.Fatal("Gateway SQL fixture field is unavailable")
		}
		return string(data)
	}
	credentials := read("/api/v1/namespaces/" + ns + "/secrets/openshell-gateway-db-credentials")
	keys := read("/api/v1/namespaces/" + ns + "/secrets/openshell-gateway-keys")
	source := read("/api/v1/namespaces/" + state + "/secrets/openshell-gateway-state")
	names := databaseNames(w.t, w.f.cluster, id)
	if value(credentials, "user") != names.User || value(credentials, "dbname") != names.Database || value(credentials, "sslmode") != "verify-full" || value(credentials, "password") != value(source, "database-password") {
		w.t.Fatal("Gateway SQL credential differs from its assigned identity")
	}
	for _, key := range []string{"signing.pem", "public.pem", "kid", "key-encryption-key"} {
		if value(keys, key) != value(source, key) {
			w.t.Fatal("published keys differ from retained source state")
		}
	}
	port, err := strconv.ParseUint(value(credentials, "port"), 10, 16)
	if err != nil || port == 0 {
		w.t.Fatal("Gateway SQL port is invalid")
	}
	server, err := postgres.DatabaseServerIdentity(ctx, w.databaseOptions)
	if err != nil || server != value(source, "database-server") {
		w.t.Fatal("Gateway SQL server identity changed", err)
	}
	return postgres.Options{Host: value(credentials, "host"), Port: uint16(port), User: names.User, Database: names.Database, Password: value(credentials, "password"), CA: []byte(value(credentials, "ca.crt"))}, map[string]string{"credentials": kube.String(credentials, "metadata", "uid"), "keys": kube.String(keys, "metadata", "uid"), "source": kube.String(source, "metadata", "uid"), "server": server}
}

func (w *browserGatewayWorkload) checkSQLIsolation() map[string]map[string]string {
	w.t.Helper()
	if len(w.gatewayIDs) != 2 {
		w.t.Fatal("SQL isolation requires two Gateways")
	}
	identities := map[string]map[string]string{}
	for i, id := range w.gatewayIDs {
		o, identity := w.sqlOptions(id)
		identities[id] = identity
		names := databaseNames(w.t, w.f.cluster, id)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		var secure, limited, owner bool
		err := postgres.ReadRow(ctx, o, `SELECT s.ssl, NOT(r.rolsuper OR r.rolcreatedb OR r.rolcreaterole OR r.rolreplication OR r.rolbypassrls OR r.rolinherit) AND r.rolcanlogin AND r.rolconnlimit=32 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_auth_members m WHERE m.member=r.oid), d.datdba=own.oid AND NOT own.rolcanlogin AND own.oid<>r.oid AND d.datconnlimit=32 AND NOT pg_catalog.has_database_privilege(r.oid,d.oid,'CREATE') FROM pg_catalog.pg_roles r JOIN pg_catalog.pg_database d ON d.datname=current_database() JOIN pg_catalog.pg_roles own ON own.rolname=$1 JOIN pg_catalog.pg_stat_ssl s ON s.pid=pg_backend_pid() WHERE r.rolname=current_user`, []any{names.Owner}, &secure, &limited, &owner)
		cancel()
		if err != nil || !secure || !limited || !owner {
			w.t.Fatal("Gateway SQL TLS, ownership, or login permissions differ", err)
		}
		other := databaseNames(w.t, w.f.cluster, w.gatewayIDs[1-i])
		for _, database := range []string{other.Database, w.databaseOptions.Database, "postgres"} {
			o.Database = database
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			var one int
			err := postgres.ReadRow(ctx, o, "SELECT 1", nil, &one)
			cancel()
			var failure *postgres.Error
			if !errors.As(err, &failure) || failure.Stage != "connect" || failure.SQLState != "42501" {
				w.t.Fatal("Gateway SQL cross-database access was not denied", err)
			}
		}
	}
	w.t.Log("Both Gateway logins use verified TLS and separate databases; cross-database access is denied")
	return identities
}

func (w *browserGatewayWorkload) checkSQLDeletion(id string) {
	w.t.Helper()
	other := ""
	for _, candidate := range w.gatewayIDs {
		if candidate != id {
			other = candidate
		}
	}
	if other == "" {
		w.t.Fatal("remaining Gateway is missing")
	}
	o, _ := w.sqlOptions(other)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	w.requireSQLAbsent(ctx, o, id)
}
func (w *browserGatewayWorkload) requireSQLAbsent(ctx context.Context, o postgres.Options, id string) {
	w.t.Helper()
	names := databaseNames(w.t, w.f.cluster, id)
	var absent bool
	if err := postgres.ReadRow(ctx, o, `SELECT NOT EXISTS(SELECT 1 FROM pg_catalog.pg_roles WHERE rolname IN ($1,$2)) AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_database WHERE datname=$3)`, []any{names.User, names.Owner, names.Database}, &absent); err != nil || !absent {
		w.t.Fatal("deleted Gateway SQL state remains", err)
	}
	ns, err := gatewayworkload.StateNamespace(id)
	if err != nil {
		w.t.Fatal(err)
	}
	if _, code, err := w.kubernetes.Request(ctx, "GET", "/api/v1/namespaces/"+ns, nil); err != nil || code != 404 {
		w.t.Fatal("deleted Gateway durable state remains", code)
	}
}
