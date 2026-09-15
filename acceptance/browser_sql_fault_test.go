package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	"google.golang.org/protobuf/proto"
)

// Only the isolated test fixture has this account. The worker uses its limited
// installation account. Never put connection strings or SQL values in logs.
func (w *browserGatewayWorkload) fixtureSQL() *pgx.Conn {
	w.t.Helper()
	config := w.sqlFixtureConfig()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		w.t.Fatal("SQL fault fixture connection failed")
	}
	w.t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		connection.Close(ctx)
	})
	return connection
}

func (w *browserGatewayWorkload) checkSQLFaultRecovery(id string) {
	w.t.Helper()
	before := w.checkSQLIsolation()
	owner := w.identity.browserLogin(w.t, w.audience(id), "console-alice")
	provider, err := w.call("GetProvider", owner, `{"name":"browser-provider"}`)
	if err != nil {
		w.t.Fatal("SQL fault setup could not read provider")
	}
	admin := w.fixtureSQL()
	names := databaseNames(w.t, w.f.cluster, id)
	role := pgx.Identifier{names.User}.Sanitize()
	otherID := ""
	for _, candidate := range w.gatewayIDs {
		if candidate != id {
			otherID = candidate
		}
	}
	if otherID == "" {
		w.t.Fatal("SQL session isolation requires another Gateway")
	}
	open := func(gatewayID string) *pgx.Conn {
		w.t.Helper()
		config := w.sqlFixtureConfig()
		options, _ := w.sqlOptions(gatewayID)
		config.Database, config.User, config.Password = options.Database, options.User, options.Password
		config.ConnectTimeout = 5 * time.Second
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		connection, err := pgx.ConnectConfig(ctx, config)
		if err != nil {
			w.t.Fatal("Gateway SQL session setup failed")
		}
		w.t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			connection.Close(ctx)
		})
		var one int
		if err := connection.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil || one != 1 {
			w.t.Fatal("Gateway SQL session precondition failed")
		}
		return connection
	}
	exec := func(query string) {
		w.t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, query); err != nil {
			w.t.Fatal("SQL fault fixture change failed")
		}
	}
	for _, fault := range []struct{ apply, restore string }{
		{"ALTER ROLE " + role + " CREATEDB", "ALTER ROLE " + role + " NOCREATEDB"},
		{"GRANT pg_read_all_data TO " + role, "REVOKE pg_read_all_data FROM " + role},
	} {
		func() {
			ownedSession, otherSession := open(id), open(otherID)
			// Restore the operator-owned fault even if the assertion stops this test.
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if _, err := admin.Exec(ctx, fault.restore); err != nil {
					w.t.Error("SQL fault fixture restore failed")
				}
			}()
			exec(fault.apply)
			deadline := time.Now().Add(90 * time.Second)
			for {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				var disabled bool
				err := admin.QueryRow(ctx, "SELECT NOT rolcanlogin FROM pg_catalog.pg_roles WHERE rolname=$1", names.User).Scan(&disabled)
				cancel()
				response := w.owner.api(w.t, "GET", "/gateways/"+id, nil)
				var row httpapi.Gateway
				if err == nil && disabled && response.StatusCode == 200 && json.Unmarshal(response.Body, &row) == nil && row.Status != nil && *row.Status == "WorkloadUnavailable" {
					break
				}
				if time.Now().After(deadline) {
					w.t.Fatal("unsafe Gateway SQL permissions did not disable login and report failure")
				}
				time.Sleep(time.Second)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			var one int
			err := ownedSession.QueryRow(ctx, "SELECT 1").Scan(&one)
			cancel()
			var stopped *pgconn.PgError
			if !errors.As(err, &stopped) || stopped.Code != "57P01" {
				w.t.Fatal("unsafe Gateway SQL session was not terminated by the server")
			}
			ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
			err = otherSession.QueryRow(ctx, "SELECT 1").Scan(&one)
			otherSession.Close(ctx)
			cancel()
			if err != nil || one != 1 {
				w.t.Fatal("SQL quarantine interrupted another Gateway session")
			}
			exec(fault.restore)
			w.check(id)
		}()
	}
	// Authentication repair must reuse the retained password and encryption keys.
	exec("ALTER ROLE " + role + " PASSWORD 'temporary-acceptance-password'")
	deadline := time.Now().Add(90 * time.Second)
	for {
		// A new connection is required. An old Gateway SQL session is not evidence.
		config := w.sqlFixtureConfig()
		options, _ := w.sqlOptions(id)
		config.Database, config.User, config.Password = options.Database, options.User, options.Password
		config.ConnectTimeout = 5 * time.Second
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		connection, err := pgx.ConnectConfig(ctx, config)
		if err == nil {
			connection.Close(ctx)
			cancel()
			break
		}
		cancel()
		if time.Now().After(deadline) {
			w.t.Fatal("Gateway SQL password did not recover")
		}
		time.Sleep(time.Second)
	}
	w.check(id)
	if after := w.checkSQLIsolation(); !reflect.DeepEqual(before, after) {
		w.t.Fatal("SQL fault recovery changed retained identities")
	}
	after, err := w.call("GetProvider", owner, `{"name":"browser-provider"}`)
	if err != nil || !proto.Equal(provider, after) {
		w.t.Fatal("SQL fault recovery changed provider data")
	}
	w.t.Log("Unsafe SQL privileges and role membership disabled Gateway login and terminated its existing session; the other Gateway session remained open. Operator repair and password recovery preserved credentials, keys, and provider data")
}
