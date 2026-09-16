package gatewayworkload

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jsell-rh/hypershell-stego/out/deploy/allocation"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
	sql "github.com/jsell-rh/hypershell-stego/out/postgres"
)

// This test uses a real, empty PostgreSQL server. Kubernetes records use an
// HTTPS fixture. It does not prove Pod operation or cluster access rules.
func TestGatewaySQLUsesDurableStateAndRetainsSuppliedServer(t *testing.T) {
	if os.Getenv("STEGO_REQUIRE_GATEWAY_SQL") != "1" {
		t.Skip("requires a dedicated Gateway SQL test server")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	bootstrapConfig, err := pgx.ParseConfig(os.Getenv("STEGO_TEST_POSTGRES_DSN"))
	if err != nil || bootstrapConfig.TLSConfig == nil || bootstrapConfig.TLSConfig.InsecureSkipVerify || len(bootstrapConfig.Fallbacks) != 0 {
		t.Fatal("the SQL fixture requires verified TLS without fallback")
	}
	bootstrapConfig.ConnectTimeout = 5 * time.Second
	bootstrap, err := pgx.ConnectConfig(ctx, bootstrapConfig)
	if err != nil {
		t.Fatal("SQL fixture connection failed")
	}
	defer bootstrap.Close(context.Background())
	execSQL := func(conn *pgx.Conn, query string, args ...any) {
		t.Helper()
		if _, err := conn.Exec(ctx, query, args...); err != nil {
			// SQL errors can contain credentials. Report only the server code.
			t.Fatal("SQL fixture operation failed", sqlState(err))
		}
	}
	var count int
	if err := bootstrap.QueryRow(ctx, "SELECT count(*) FROM pg_catalog.pg_database WHERE datname NOT IN ('postgres','template0','template1')").Scan(&count); err != nil || count != 0 {
		t.Fatal("the Gateway SQL check requires an empty dedicated server")
	}
	password, err := sql.NewDatabasePassword()
	if err != nil {
		t.Fatal(err)
	}
	admin := "gateway_fixture_" + password[:12]
	ledger := admin + "_ledger"
	quote := func(value string) string { return pgx.Identifier{value}.Sanitize() }
	execSQL(bootstrap, "CREATE ROLE "+quote(admin)+" LOGIN NOSUPERUSER CREATEDB CREATEROLE PASSWORD '"+password+"'")
	execSQL(bootstrap, "CREATE DATABASE "+quote(ledger)+" OWNER "+quote(admin))
	// The installation controls server access. Gateway reconciliation cannot
	// change PUBLIC access on unrelated databases.
	execSQL(bootstrap, "REVOKE CONNECT ON DATABASE postgres,template1,"+quote(ledger)+" FROM PUBLIC")
	ca, err := os.ReadFile(os.Getenv("STEGO_TEST_POSTGRES_CA_FILE"))
	if err != nil {
		t.Fatal("SQL fixture CA is unavailable")
	}
	config := databaseConfig{Host: bootstrapConfig.Host, Port: bootstrapConfig.Port, Database: ledger, User: admin, Password: password, CA: string(ca)}
	adminConfig := bootstrapConfig.Copy()
	adminConfig.Database, adminConfig.User, adminConfig.Password = ledger, admin, password
	adminConn, err := pgx.ConnectConfig(ctx, adminConfig)
	if err != nil {
		t.Fatal("provisioning account connection failed")
	}
	defer adminConn.Close(context.Background())
	execSQL(adminConn, "CREATE TABLE installation_data(value text NOT NULL)")
	execSQL(adminConn, "INSERT INTO installation_data VALUES ('keep')")

	var mu sync.Mutex
	objects := map[string]object{}
	put := func(key string, value object) { mu.Lock(); defer mu.Unlock(); objects[key] = value }
	k := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			if strings.HasSuffix(r.URL.Path, "/networkpolicies") {
				value := objects[r.URL.Path+"/stego-allocation"]
				if value == nil {
					w.WriteHeader(404)
					return
				}
				_ = json.NewEncoder(w).Encode(object{"metadata": object{"resourceVersion": "1"}, "items": []any{value}})
				return
			}
			if value := objects[r.URL.Path]; value != nil {
				_ = json.NewEncoder(w).Encode(value)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		case http.MethodPost:
			if !strings.HasSuffix(r.URL.Path, "/secrets") && !strings.HasSuffix(r.URL.Path, "/configmaps") {
				t.Error("Gateway attempted to create a server resource")
				w.WriteHeader(http.StatusForbidden)
				return
			}
			var value object
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 131072)).Decode(&value) != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			meta, ok := value["metadata"].(map[string]any)
			if !ok {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			key := r.URL.Path + "/" + kube.String(value, "metadata", "name")
			if objects[key] != nil {
				w.WriteHeader(http.StatusConflict)
				return
			}
			meta["uid"], meta["resourceVersion"] = key, "1"
			objects[key] = value
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(value)
		default:
			t.Error("unexpected Gateway Kubernetes write", r.Method)
			w.WriteHeader(http.StatusForbidden)
		}
	})
	saveConfig := func(value databaseConfig) {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil || os.WriteFile(k.options.DatabaseConfigFile, raw, 0600) != nil {
			t.Fatal("could not write the controller database file")
		}
	}
	saveConfig(config)
	first, _ := records(t)
	second, _ := records(t)
	names := func(gw *pb.Gateway) sql.DatabaseIdentity {
		t.Helper()
		value, err := sql.DatabaseNames(k.databaseKey(gw))
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	// The Job owns the whole disposable server. Cleanup also runs after a
	// failed assertion, without relying on the behavior under test.
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		conn, err := pgx.ConnectConfig(clean, bootstrapConfig)
		if err != nil {
			t.Error("SQL fixture cleanup connection failed")
			return
		}
		defer conn.Close(clean)
		for _, gw := range []*pb.Gateway{first, second} {
			consoleNames, _ := sql.DatabaseNames(k.consoleDatabaseKey(gw))
			for _, n := range []sql.DatabaseIdentity{names(gw), consoleNames} {
				for _, query := range []string{"DROP DATABASE IF EXISTS " + quote(n.Database) + " WITH (FORCE)", "DROP ROLE IF EXISTS " + quote(n.User), "DROP ROLE IF EXISTS " + quote(n.Owner)} {
					if _, err := conn.Exec(clean, query); err != nil {
						t.Error("Gateway fixture cleanup failed", sqlState(err))
					}
				}
			}
		}
		for _, query := range []string{"DROP DATABASE " + quote(ledger) + " WITH (FORCE)", "DROP ROLE " + quote(admin), "GRANT CONNECT ON DATABASE postgres,template1 TO PUBLIC"} {
			if _, err := conn.Exec(clean, query); err != nil {
				t.Error("installation fixture cleanup failed", sqlState(err))
			}
		}
	})
	seal := func(gw *pb.Gateway) (object, sql.Options) {
		t.Helper()
		ns, _ := StateNamespace(gw.Metadata.Id)
		namespace := k.stateDefinition("Namespace", ns, gw.Metadata.Id)
		meta := namespace["metadata"].(object)
		meta["uid"], meta["resourceVersion"] = ns, "1"
		put("/api/v1/namespaces/"+ns, namespace)
		put("/apis/networking.k8s.io/v1/namespaces/"+ns+"/networkpolicies/stego-allocation", stateNetworkPolicyFixture(k, gw.Metadata.Id))
		if _, _, err := k.localState(ctx, gw); !errors.Is(err, ErrPending) {
			t.Fatal("SQL did not wait for the state seal", err)
		}
		var exists bool
		if err := bootstrap.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_database WHERE datname=$1)", names(gw).Database).Scan(&exists); err != nil || exists {
			t.Fatal("database created before the allocator seal")
		}
		mu.Lock()
		marker := objects["/api/v1/namespaces/"+ns+"/configmaps/"+stateIdentity]
		meta["annotations"] = object{stateAnnotation: kube.String(marker, "data", "sha256")}
		mu.Unlock()
		state, selected, err := k.localState(ctx, gw)
		if err != nil {
			t.Fatal("sealed state was rejected", err)
		}
		return state, selected
	}
	firstState, selected := seal(first)
	secondState, _ := seal(second)
	firstCredentials, err := k.databaseCredentials(ctx, first, firstState, selected)
	if err != nil {
		t.Fatal("first Gateway database failed", err)
	}
	secondCredentials, err := k.databaseCredentials(ctx, second, secondState, selected)
	if err != nil {
		t.Fatal("second Gateway database failed", err)
	}
	clientConfig := func(credentials object) *pgx.ConnConfig {
		t.Helper()
		c := bootstrapConfig.Copy()
		for field, target := range map[string]*string{"dbname": &c.Database, "user": &c.User, "password": &c.Password} {
			value, err := data(object{"data": credentials}, field)
			if err != nil {
				t.Fatal("Gateway credential is invalid")
			}
			*target = string(value)
		}
		return c
	}
	checkData := func(credentials object, want string) {
		t.Helper()
		conn, err := pgx.ConnectConfig(ctx, clientConfig(credentials))
		if err != nil {
			t.Fatal("Gateway SQL connection failed")
		}
		defer conn.Close(ctx)
		var got string
		if err := conn.QueryRow(ctx, "SELECT value FROM gateway_data").Scan(&got); err != nil || got != want {
			t.Fatal("Gateway data changed")
		}
	}
	for i, credentials := range []object{firstCredentials, secondCredentials} {
		conn, err := pgx.ConnectConfig(ctx, clientConfig(credentials))
		if err != nil {
			t.Fatal("Gateway SQL connection failed")
		}
		execSQL(conn, "CREATE TABLE gateway_data(value text NOT NULL)")
		execSQL(conn, "INSERT INTO gateway_data VALUES ($1)", []string{"first", "second"}[i])
		var safe bool
		if err := conn.QueryRow(ctx, `SELECT rolcanlogin AND NOT (rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls OR rolinherit) AND rolconnlimit=32 FROM pg_catalog.pg_roles WHERE rolname=current_user`).Scan(&safe); err != nil || !safe {
			t.Fatal("Gateway login privileges are unsafe")
		}
		conn.Close(ctx)
		for _, target := range []string{ledger, "postgres", []string{names(second).Database, names(first).Database}[i]} {
			denied := clientConfig(credentials)
			denied.Database = target
			if other, err := pgx.ConnectConfig(ctx, denied); err == nil {
				other.Close(ctx)
				t.Fatal("Gateway connected to another database")
			} else if sqlState(err) != "42501" {
				t.Fatal("cross-database check failed without a permission denial", sqlState(err))
			}
		}
	}
	// The same worker provisions the generated browser schema in a separate
	// logical database. Its retained key and runtime login never belong to Gateway.
	consoleNS, _ := ConsoleStateNamespace(first.Metadata.Id)
	consoleNamespace := object{"apiVersion": "v1", "kind": "Namespace", "metadata": object{"name": consoleNS, "uid": consoleNS, "resourceVersion": "1", "labels": k.consoleStateOwner(first.Metadata.Id)}}
	consolePolicy := stateNetworkPolicyFixture(k, first.Metadata.Id)
	consolePolicy["metadata"].(object)["namespace"] = consoleNS
	consolePolicy["metadata"].(object)["labels"] = k.consoleStateOwner(first.Metadata.Id)
	put("/api/v1/namespaces/"+consoleNS, consoleNamespace)
	put("/apis/networking.k8s.io/v1/namespaces/"+consoleNS+"/networkpolicies/stego-allocation", consolePolicy)
	if _, err := k.prepareConsoleDatabase(ctx, first); !errors.Is(err, ErrPending) {
		t.Fatal("console SQL did not wait for its state pin", err)
	}
	consoleNames, _ := sql.DatabaseNames(k.consoleDatabaseKey(first))
	var consolePresent bool
	if err := bootstrap.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_database WHERE datname=$1)", consoleNames.Database).Scan(&consolePresent); err != nil || consolePresent {
		t.Fatal("unregistered console created its database")
	}
	mu.Lock()
	consoleMarker := objects["/api/v1/namespaces/"+consoleNS+"/configmaps/"+consoleStateMarker]
	consoleNamespace["metadata"].(object)["annotations"] = object{consoleStateAnnotation: kube.String(consoleMarker, "data", "sha256")}
	mu.Unlock()
	consoleFiles, err := k.prepareConsoleDatabase(ctx, first)
	if err != nil {
		t.Fatal("generated console SQL schema failed", err)
	}
	urlBytes, err := data(object{"data": consoleFiles}, "database-url")
	if err != nil {
		t.Fatal(err)
	}
	consoleURL, err := url.Parse(string(urlBytes))
	if err != nil {
		t.Fatal("console database URL is invalid")
	}
	consoleConfig := bootstrapConfig.Copy()
	consoleConfig.Database = strings.TrimPrefix(consoleURL.Path, "/")
	consoleConfig.User = consoleURL.User.Username()
	consoleConfig.Password, _ = consoleURL.User.Password()
	consoleRuntime, err := pgx.ConnectConfig(ctx, consoleConfig)
	if err != nil {
		t.Fatal("console runtime connection failed", sqlState(err))
	}
	defer consoleRuntime.Close(context.Background())
	execSQL(consoleRuntime, "INSERT INTO public.stego_browser_sessions(id_hash,payload,state,expires_at) VALUES(decode(repeat('00',32),'hex'),decode(repeat('00',28),'hex'),'active',CURRENT_TIMESTAMP+INTERVAL '1 hour')")
	for _, query := range []string{"CREATE TABLE public.forbidden(value integer)", "CREATE TEMP TABLE forbidden(value integer)", "TRUNCATE public.stego_browser_sessions"} {
		if _, err := consoleRuntime.Exec(ctx, query); sqlState(err) != "42501" {
			t.Fatal("console runtime schema write was not denied", sqlState(err))
		}
	}
	for _, database := range []string{names(first).Database, names(second).Database, ledger} {
		wrong := consoleConfig.Copy()
		wrong.Database = database
		connection, err := pgx.ConnectConfig(ctx, wrong)
		if err == nil {
			connection.Close(ctx)
			t.Fatal("console connected to another database")
		}
		if sqlState(err) != "42501" {
			t.Fatal("console cross-database check failed without denial", sqlState(err))
		}
	}
	// A new adapter must reuse the original keys, credential, and database.
	k.Close()
	k, err = NewKubernetes(k.options)
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	recovered, selected, err := k.readLocalState(ctx, first)
	if err != nil || !reflect.DeepEqual(recovered, firstState) {
		t.Fatal("restart changed Gateway source state", err)
	}
	again, err := k.databaseCredentials(ctx, first, recovered, selected)
	if err != nil || !reflect.DeepEqual(again, firstCredentials) {
		t.Fatal("restart changed Gateway SQL credentials", err)
	}
	recoveredConsoleFiles, err := k.prepareConsoleDatabase(ctx, first)
	if err != nil || !reflect.DeepEqual(consoleFiles, recoveredConsoleFiles) {
		t.Fatal("restart changed console credentials or session key", err)
	}
	if err := consoleRuntime.QueryRow(ctx, "SELECT count(*) FROM public.stego_browser_sessions").Scan(&count); err != nil || count != 1 {
		t.Fatal("restart changed console session rows", sqlState(err))
	}
	checkData(firstCredentials, "first")
	checkData(secondCredentials, "second")
	changed := config
	changed.Database = "postgres"
	saveConfig(changed)
	if _, _, err := k.localState(ctx, first); err == nil {
		t.Fatal("changed SQL destination was accepted")
	}
	if err := k.DeleteDatabase(ctx, first); err == nil {
		t.Fatal("changed SQL destination permitted deletion")
	}
	saveConfig(config)
	ns, _ := StateNamespace(first.Metadata.Id)
	secretPath := "/api/v1/namespaces/" + ns + "/secrets/" + stateSecret
	put(secretPath, nil)
	if _, _, err := k.localState(ctx, first); err == nil {
		t.Fatal("lost credentials were replaced")
	}
	if err := k.DeleteDatabase(ctx, first); err == nil {
		t.Fatal("lost credentials permitted deletion")
	}
	if !k.options.ConsoleSQLBindings.(*consoleBindingFixture).completed {
		t.Fatal("Gateway key loss prevented independent console cleanup")
	}
	if err := bootstrap.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_database WHERE datname=$1)", consoleNames.Database).Scan(&consolePresent); err != nil || consolePresent {
		t.Fatal("console completion preceded actual database deletion")
	}
	if _, err := k.prepareConsoleDatabase(ctx, first); err == nil {
		t.Fatal("late console retry recreated closed state")
	}
	put(secretPath, firstState)
	checkData(firstCredentials, "first")
	// The public cleanup entry point must wait for the workload namespace.
	workload := k.stateDefinition("Namespace", first.Namespace, first.Metadata.Id)
	meta := workload["metadata"].(object)
	meta["uid"], meta["resourceVersion"] = "workload", "1"
	meta["labels"].(object)[allocation.ProfileLabel] = "gateway"
	workloadPath := path.Join("/api/v1/namespaces", first.Namespace)
	put(workloadPath, workload)
	if err := k.DeleteDatabase(ctx, first); !errors.Is(err, ErrPending) {
		t.Fatal("SQL cleanup did not wait for the workload", err)
	}
	put(workloadPath, nil)
	if err := k.DeleteDatabase(ctx, first); err != nil {
		t.Fatal("Gateway SQL cleanup failed", err)
	}
	if err := k.DeleteDatabase(ctx, first); err != nil {
		t.Fatal("Gateway SQL cleanup was not repeatable", err)
	}
	n := names(first)
	if err := bootstrap.QueryRow(ctx, `SELECT (SELECT count(*) FROM pg_catalog.pg_database WHERE datname=$1)+(SELECT count(*) FROM pg_catalog.pg_roles WHERE rolname IN ($2,$3))`, n.Database, n.Owner, n.User).Scan(&count); err != nil || count != 0 {
		t.Fatal("Gateway SQL resources remain")
	}
	if _, err := k.databaseCredentials(ctx, first, firstState, selected); !errors.Is(err, sql.ErrDatabaseDeleted) {
		t.Fatal("a late retry restored deleted SQL", err)
	}
	checkData(secondCredentials, "second")
	var preserved string
	if err := adminConn.QueryRow(ctx, "SELECT value FROM installation_data").Scan(&preserved); err != nil || preserved != "keep" {
		t.Fatal("Gateway cleanup changed installation data")
	}
	if err := k.DeleteDatabase(ctx, second); err != nil {
		t.Fatal("second Gateway SQL cleanup failed", err)
	}
}

func sqlState(err error) string {
	var coded interface{ SQLState() string }
	if errors.As(err, &coded) {
		return coded.SQLState()
	}
	return "unavailable"
}
