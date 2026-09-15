package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jsell-rh/hypershell-stego/internal/gatewayworkload"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
	postgres "github.com/jsell-rh/hypershell-stego/out/postgres"
)

// Remove one permission on the target login. The other Gateway's permissions
// stay in place. Restore the original grant before normal cleanup continues.
func (w *browserGatewayWorkload) beginSQLCleanupDenial(id string) func() {
	w.t.Helper()
	admin := w.fixtureSQL()
	names := databaseNames(w.t, w.f.cluster, id)
	quote := func(s string) string { return pgx.Identifier{s}.Sanitize() }
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	var grantor string
	var inherit, set, allowed bool
	err := admin.QueryRow(ctx, `SELECT g.rolname,m.inherit_option,m.set_option,m.admin_option
FROM pg_catalog.pg_auth_members m JOIN pg_catalog.pg_roles r ON r.oid=m.roleid
JOIN pg_catalog.pg_roles u ON u.oid=m.member JOIN pg_catalog.pg_roles g ON g.oid=m.grantor
WHERE r.rolname=$1 AND u.rolname=$2`, names.User, w.databaseOptions.User).Scan(&grantor, &inherit, &set, &allowed)
	cancel()
	if err != nil || grantor == "" || !allowed {
		w.t.Fatal("SQL cleanup denial needs the original administrator grant")
	}
	state, err := gatewayworkload.StateNamespace(id)
	if err != nil {
		w.t.Fatal(err)
	}
	readSource := func() kube.Object {
		w.t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		object, code, err := w.kubernetes.Request(ctx, "GET", "/api/v1/namespaces/"+state+"/secrets/openshell-gateway-state", nil)
		if err != nil || code != 200 || kube.String(object, "metadata", "uid") == "" || object["data"] == nil {
			w.t.Fatal("SQL cleanup denial lost retained source state", code)
		}
		return object
	}
	before := readSource()
	objects := func() [3]string {
		w.t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var value [3]string
		err := admin.QueryRow(ctx, `SELECT d.oid::text,r.oid::text,d.datdba::text FROM pg_catalog.pg_database d
JOIN pg_catalog.pg_roles r ON r.rolname=$2 WHERE d.datname=$1`, names.Database, names.User).Scan(&value[0], &value[1], &value[2])
		if err != nil {
			w.t.Fatal("SQL cleanup denial lost recorded SQL objects")
		}
		return value
	}
	beforeObjects := objects()
	restored := false
	restore := func() {
		if restored {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := admin.Exec(ctx, "GRANT "+quote(names.User)+" TO "+quote(w.databaseOptions.User)+" WITH ADMIN TRUE GRANTED BY "+quote(grantor))
		if err != nil {
			w.t.Error("SQL cleanup administrator grant restore failed")
			return
		}
		restored = true
	}
	w.t.Cleanup(restore)
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	_, err = admin.Exec(ctx, "REVOKE ADMIN OPTION FOR "+quote(names.User)+" FROM "+quote(w.databaseOptions.User)+" GRANTED BY "+quote(grantor)+" RESTRICT")
	cancel()
	if err != nil {
		w.t.Fatal("SQL cleanup permission fault failed")
	}
	// Prove the server denies the same role operation. Roll back even if an
	// unexpected grant permits it, so this probe cannot change the login.
	config, err := pgx.ParseConfig(os.Getenv("STEGO_TEST_POSTGRES_DSN"))
	if err != nil || config.TLSConfig == nil || config.TLSConfig.InsecureSkipVerify || config.TLSConfig.RootCAs == nil || config.Host != "127.0.0.1" {
		w.t.Fatal("SQL cleanup denial requires the verified loopback fixture")
	}
	config.Database, config.User, config.Password = w.databaseOptions.Database, w.databaseOptions.User, w.databaseOptions.Password
	config.ConnectTimeout = 5 * time.Second
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	connection, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		cancel()
		w.t.Fatal("SQL cleanup permission probe could not connect")
	}
	tx, err := connection.Begin(ctx)
	if err == nil {
		_, err = tx.Exec(ctx, "ALTER ROLE "+quote(names.User)+" NOLOGIN")
		_ = tx.Rollback(ctx)
	}
	_ = connection.Close(ctx)
	cancel()
	var denied *pgconn.PgError
	if !errors.As(err, &denied) || denied.Code != "42501" {
		w.t.Fatal("SQL cleanup role operation was not denied by PostgreSQL")
	}
	return func() {
		w.t.Helper()
		defer restore()
		deadline := time.Now().Add(120 * time.Second)
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			var phase string
			err := postgres.ReadRow(ctx, w.databaseOptions, `SELECT state FROM stego_provisioning.resources WHERE scope=$1 AND resource=$2`, []any{w.f.cluster, id}, &phase)
			cancel()
			if err != nil {
				w.t.Fatal("SQL cleanup ledger read failed")
			}
			if phase == "deleting" {
				break
			}
			if phase != "ready" || time.Now().After(deadline) {
				w.t.Fatal("SQL cleanup did not retain a pending deletion")
			}
			time.Sleep(time.Second)
		}
		// The ledger proves the generated deletion path reached SQL. A 404 from
		// the public API alone cannot prove that cleanup ran or retained keys.
		after := readSource()
		if kube.String(before, "metadata", "uid") != kube.String(after, "metadata", "uid") || !reflect.DeepEqual(before["data"], after["data"]) || beforeObjects != objects() {
			w.t.Fatal("denied SQL cleanup changed retained state")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		var pending bool
		err := w.f.db.QueryRowContext(ctx, `SELECT deleted_at IS NOT NULL AND stego_cleanup_targets->'sql'->>$2='false' AND stego_cleanup_targets->'workload'->>$2='false' FROM gateways WHERE id=$1`, id, w.f.cluster).Scan(&pending)
		cancel()
		if err != nil || !pending {
			w.t.Fatal("denied SQL cleanup was recorded as complete")
		}
		ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
		var currentInherit, currentSet, currentAdmin bool
		err = admin.QueryRow(ctx, `SELECT m.inherit_option,m.set_option,m.admin_option FROM pg_catalog.pg_auth_members m JOIN pg_catalog.pg_roles r ON r.oid=m.roleid JOIN pg_catalog.pg_roles u ON u.oid=m.member JOIN pg_catalog.pg_roles g ON g.oid=m.grantor WHERE r.rolname=$1 AND u.rolname=$2 AND g.rolname=$3`, names.User, w.databaseOptions.User, grantor).Scan(&currentInherit, &currentSet, &currentAdmin)
		cancel()
		if err != nil || currentInherit != inherit || currentSet != set || currentAdmin {
			w.t.Fatal("SQL cleanup changed the operator's permission fault")
		}
		for _, other := range w.gatewayIDs {
			if other != id {
				w.check(other)
			}
		}
		if directory := os.Getenv("STEGO_BROWSER_ARTIFACT_DIR"); directory != "" {
			record := map[string]any{"gateway_id": id, "sqlstate": "42501", "ledger_state": "deleting", "source_secret_uid": kube.String(after, "metadata", "uid"), "source_data_unchanged": true, "sql_object_ids_unchanged": true, "cleanup_pending": true, "other_gateway_ready": true}
			data, err := json.MarshalIndent(record, "", "  ")
			if err != nil || os.WriteFile(filepath.Join(directory, "sql-cleanup-denial.json"), append(data, '\n'), 0600) != nil {
				w.t.Fatal("cannot write SQL cleanup denial evidence")
			}
		}
		w.t.Log("Denied SQL cleanup retained source keys and SQL objects, kept cleanup pending, and preserved the other Gateway")
	}
}
