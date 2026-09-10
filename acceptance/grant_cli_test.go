package acceptance

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	command "github.com/jsell-rh/hypershell-stego/out/cli/command"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"github.com/segmentio/ksuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestGeneratedCLIGrantWorkflow(t *testing.T) {
	f := database(t)
	_, brokerConfig := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, brokerConfig)
	key, settings := issuer(t)
	rpcIdentity := identity(t, "localhost")
	tlsDir := filepath.Dir(rpcIdentity.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(tlsDir, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(tlsDir, "server-key.pem"))
	api, cli := buildApplication(t), buildProgram(t, "./out/cli/cmd")
	stop, address, rpcAddress := startBoth(t, api, f.dsn, brokerConfig, settings...)
	defer func() { stop() }()
	var writes atomic.Int32
	var backend atomic.Value
	backend.Store(address)
	proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			writes.Add(1)
		}
		target, err := url.Parse(backend.Load().(string))
		if err != nil {
			t.Error(err)
			return
		}
		httputil.NewSingleHostReverseProxy(target).ServeHTTP(w, r)
	}))
	proxy.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
	proxy.StartTLS()
	defer proxy.Close()
	directory := t.TempDir()
	ca := filepath.Join(directory, "api-ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: proxy.Certificate().Raw}), 0644); err != nil {
		t.Fatal(err)
	}
	tokens := map[string]string{"alice": token(t, key, "alice", "gateway:creator"), "bob": token(t, key, "bob"), "carol": token(t, key, "carol")}
	configs := map[string]string{}
	run := func(user string, args ...string) ([]byte, string, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, cli, args...)
		cmd.Env = append(os.Environ(), "HYPERSHELL_CONFIG="+configs[user], "GORACE=atexit_sleep_ms=0")
		var output, problem bytes.Buffer
		cmd.Stdout, cmd.Stderr = &output, &problem
		err := cmd.Run()
		if strings.Contains(problem.String(), "DATA RACE") {
			t.Fatal("CLI data race")
		}
		for _, token := range tokens {
			if bytes.Contains(output.Bytes(), []byte(token)) || bytes.Contains(problem.Bytes(), []byte(token)) {
				t.Fatal("CLI exposed a token")
			}
		}
		return output.Bytes(), problem.String(), err
	}
	success := func(user string, args ...string) []byte {
		t.Helper()
		data, problem, err := run(user, args...)
		if err != nil {
			t.Fatalf("CLI request failed: %v %s", err, problem)
		}
		return data
	}
	denied := func(user, code string, args ...string) {
		t.Helper()
		data, problem, err := run(user, args...)
		if err == nil || len(data) != 0 || !strings.Contains(problem, "HTTP "+code) {
			t.Fatalf("CLI request did not return HTTP %s: %v %s", code, err, problem)
		}
	}
	users := map[string]httpapi.CurrentUser{}
	for _, name := range []string{"alice", "bob", "carol"} {
		configs[name] = filepath.Join(directory, name+".json")
		file := filepath.Join(directory, name+".token")
		if err := os.WriteFile(file, []byte(tokens[name]), 0600); err != nil {
			t.Fatal(err)
		}
		success(name, "login", "--url", proxy.URL, "--token-file", file, "--ca-file", ca)
		var user httpapi.CurrentUser
		if json.Unmarshal(success(name, "get", "current-user"), &user) != nil || user.ID == "" || user.Username != name {
			t.Fatal("CLI current user differs from the authenticated user")
		}
		if _, err := ksuid.Parse(user.ID); err != nil {
			t.Fatal("CLI did not return an application user ID")
		}
		users[name] = user
	}
	roles := map[string]string{}
	for _, name := range []string{"gateway:owner", "gateway:viewer"} {
		var list httpapi.RoleList
		if json.Unmarshal(success("alice", "list", "roles", "--search", "name = '"+name+"'", "--size", "1"), &list) != nil || list.Total != 1 || len(list.Items) != 1 || list.Items[0].Name != name {
			t.Fatal("CLI could not discover the role")
		}
		roles[name] = list.Items[0].ID
		var role httpapi.Role
		if json.Unmarshal(success("alice", "get", "roles", roles[name]), &role) != nil || role.Name != name {
			t.Fatal("CLI role read differs from its list")
		}
	}
	var gateway httpapi.Gateway
	data := success("alice", "create", "gateway", "--name", "cli-grants", "--cluster-id", f.cluster, "--release-id", f.release, "--database-id", "")
	if json.Unmarshal(data, &gateway) != nil || gateway.ID == "" {
		t.Fatal("CLI did not create the Gateway")
	}
	readEvent(t, consumer, gateway.ID)
	checkAccess := func(visible bool) {
		t.Helper()
		if visible {
			success("bob", "get", "gateway", gateway.ID)
		} else {
			denied("bob", "404", "get", "gateway", gateway.ID)
		}
		var list httpapi.GatewayList
		want := int64(0)
		if visible {
			want = 1
		}
		if json.Unmarshal(success("bob", "list", "gateways", "--size", "1"), &list) != nil || list.Total != want || len(list.Items) != int(want) {
			t.Fatal("CLI access filter differs from the current grant")
		}
		rpc, connection := grpcClient(t, rpcAddress, rpcIdentity)
		defer connection.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+tokens["bob"]))
		got, err := rpc.GetGateway(ctx, &pb.GetGatewayRequest{Id: gateway.ID})
		if visible {
			if err != nil || got.GetGateway().GetMetadata().GetId() != gateway.ID {
				t.Fatal("gRPC did not observe the CLI grant", err)
			}
		} else if status.Code(err) != codes.NotFound {
			t.Fatal("gRPC retained access without a grant", err)
		}
	}
	checkAccess(false)
	grantArgs := []string{"create", "roleBinding", "--gateway-id", gateway.ID, "--role-id", roles["gateway:viewer"], "--scope", "gateway", "--user-id", users["bob"].ID}
	applyFile := func(label, id, user, role string) string {
		t.Helper()
		metadata := map[string]string{"name": label}
		if id != "" {
			metadata["id"] = id
		}
		data, err := json.Marshal(map[string]any{"apiVersion": "hypershell/v1", "kind": "RoleBinding", "metadata": metadata, "spec": map[string]string{"gateway_id": gateway.ID, "role_id": role, "user_id": user, "scope": "gateway"}})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(directory, label+".json")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	bindingFile := applyFile("bob-viewer", "", users["bob"].ID, roles["gateway:viewer"])
	applyResult := func(path, want string) string {
		t.Helper()
		var results []command.ApplyResult
		if err := json.Unmarshal(success("alice", "apply", "-f", path, "-o", "json"), &results); err != nil || len(results) != 1 || results[0].Status != want || results[0].ID == "" {
			t.Fatal("invalid binding apply result", results, err)
		}
		return results[0].ID
	}
	bindingID := applyResult(bindingFile, "created")
	var grant grantResponse
	if json.Unmarshal(success("alice", "get", "role-binding", bindingID), &grant) != nil || grant.GatewayID != gateway.ID || grant.RoleID != roles["gateway:viewer"] || grant.UserID != users["bob"].ID || grant.Scope != "gateway" || grant.Kind != "RoleBinding" || grant.Href != "/api/hypershell/v1/role_bindings/"+grant.ID || grant.CreatedAt.IsZero() {
		t.Fatal("CLI grant response differs from the API contract")
	}
	if _, err := ksuid.Parse(grant.ID); err != nil {
		t.Fatal(err)
	}
	readGrantEvent(t, consumer, grant.ID, gateway.ID, "Create", "rolebinding.created")
	checkAccess(true)
	beforeRepeat := writes.Load()
	if applyResult(bindingFile, "unchanged") != grant.ID || writes.Load() != beforeRepeat {
		t.Fatal("repeat apply wrote a grant")
	}
	selected := applyFile("selected-binding", grant.ID, users["bob"].ID, roles["gateway:viewer"])
	if applyResult(selected, "unchanged") != grant.ID || writes.Load() != beforeRepeat {
		t.Fatal("selected binding changed")
	}
	mismatched := applyFile("changed-identity", grant.ID, users["carol"].ID, roles["gateway:viewer"])
	if _, _, err := run("alice", "apply", "-f", mismatched, "-o", "json"); err == nil || writes.Load() != beforeRepeat {
		t.Fatal("apply replaced immutable identity")
	}
	unauthorized := applyFile("unauthorized", "", users["carol"].ID, roles["gateway:viewer"])
	if _, problem, err := run("carol", "apply", "-f", unauthorized, "-o", "json"); err == nil || !strings.Contains(problem, "HTTP 404") {
		t.Fatal("denied grant apply succeeded", err, problem)
	}
	success("bob", "get", "roleBindings", grant.ID)
	denied("carol", "404", "get", "role-binding", grant.ID)
	var foreign struct {
		Total int
		Items []grantResponse
	}
	if json.Unmarshal(success("carol", "list", "role-bindings"), &foreign) != nil || foreign.Total != 0 || len(foreign.Items) != 0 {
		t.Fatal("CLI disclosed another user's grants")
	}
	denied("alice", "409", grantArgs...)
	denied("bob", "404", "create", "role-binding", "--gateway-id", gateway.ID, "--role-id", roles["gateway:owner"], "--scope", "gateway", "--user-id", users["bob"].ID)
	denied("bob", "404", "delete", "roleBinding", grant.ID, "--yes")
	denied("carol", "404", "delete", "role-binding", grant.ID, "--yes")
	var owners struct {
		Total int
		Items []grantResponse
	}
	search := "gateway_id = '" + gateway.ID + "' and role_id = '" + roles["gateway:owner"] + "'"
	if json.Unmarshal(success("alice", "list", "roleBindings", "--search", search, "--order-by", "id asc", "--size", "1"), &owners) != nil || owners.Total != 1 || len(owners.Items) != 1 {
		t.Fatal("CLI did not find the owner grant")
	}
	denied("alice", "409", "delete", "roleBinding", owners.Items[0].ID, "--yes")
	if _, _, err := run("alice", "delete", "roleBinding", grant.ID); err == nil {
		t.Fatal("grant deletion did not require confirmation")
	}
	awaitQueueEmpty(t, f)
	if _, err := f.db.Exec("ALTER TABLE stego_outbox.messages ADD CONSTRAINT reject_cli_grant_delete CHECK (kind <> 'rolebinding.deleted') NOT VALID"); err != nil {
		t.Fatal(err)
	}
	denied("alice", "500", "delete", "roleBinding", grant.ID, "--yes")
	checkAccess(true)
	if count(t, f.db, "stego_outbox.messages") != 0 {
		t.Fatal("failed grant deletion left an event")
	}
	if _, err := f.db.Exec("ALTER TABLE stego_outbox.messages DROP CONSTRAINT reject_cli_grant_delete"); err != nil {
		t.Fatal(err)
	}
	stop()
	stop, address, rpcAddress = startBoth(t, api, f.dsn, brokerConfig, settings...)
	backend.Store(address)
	checkAccess(true)
	beforeRestartApply := writes.Load()
	if applyResult(bindingFile, "unchanged") != grant.ID || writes.Load() != beforeRestartApply {
		t.Fatal("restart duplicated grant")
	}

	success("alice", "delete", "role-binding", grant.ID, "--yes")
	readGrantEvent(t, consumer, grant.ID, gateway.ID, "Delete", "rolebinding.deleted")
	checkAccess(false)
	denied("alice", "404", "get", "roleBinding", grant.ID)
	var restored grantResponse
	if json.Unmarshal(success("alice", grantArgs...), &restored) != nil || restored.ID == grant.ID || restored.ID == "" {
		t.Fatal("CLI could not restore a deleted grant")
	}
	readGrantEvent(t, consumer, restored.ID, gateway.ID, "Create", "rolebinding.created")
	checkAccess(true)
	if applyResult(bindingFile, "unchanged") != restored.ID {
		t.Fatal("apply selected deleted grant")
	}
	concurrent := applyFile("carol-viewer", "", users["carol"].ID, roles["gateway:viewer"])
	type concurrentResult struct {
		data    []byte
		problem string
		err     error
	}
	results := make([]concurrentResult, 2)
	var workers sync.WaitGroup
	start := make(chan struct{})
	for i := range results {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			<-start
			results[i].data, results[i].problem, results[i].err = run("alice", "apply", "-f", concurrent, "-o", "json")
		}(i)
	}
	close(start)
	workers.Wait()
	var concurrentID string
	created := 0
	for _, result := range results {
		var values []command.ApplyResult
		if result.err != nil || json.Unmarshal(result.data, &values) != nil || len(values) != 1 {
			t.Fatal("concurrent apply failed", result.err, result.problem)
		}
		if values[0].Status == "created" {
			created++
		} else if values[0].Status != "unchanged" {
			t.Fatal(values)
		}
		if concurrentID != "" && concurrentID != values[0].ID {
			t.Fatal("concurrent apply made different grants")
		}
		concurrentID = values[0].ID
	}
	if created != 1 {
		t.Fatal("concurrent apply did not create exactly one binding", created)
	}
	readGrantEvent(t, consumer, concurrentID, gateway.ID, "Create", "rolebinding.created")
	var bindingCount int
	if err := f.db.QueryRow("SELECT count(*) FROM role_bindings WHERE gateway_id=$1 AND user_id=$2 AND role_id=$3 AND deleted_at IS NULL", gateway.ID, users["carol"].ID, roles["gateway:viewer"]).Scan(&bindingCount); err != nil || bindingCount != 1 {
		t.Fatal("duplicate live binding", bindingCount, err)
	}
	for _, user := range []string{"alice", "bob", "carol"} {
		success(user, "logout")
	}
	t.Log("The generated CLI completed role discovery, grant changes, access checks, rollback, event delivery, and restart")
}
