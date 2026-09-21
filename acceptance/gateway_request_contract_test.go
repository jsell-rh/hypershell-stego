package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protowire"
)

func TestGatewayRequestsRejectRetiredDatabaseField(t *testing.T) {
	f := database(t)
	_, config := broker(t, identity(t, "localhost"))
	key, settings := issuer(t)
	tlsIdentity := identity(t, "localhost")
	directory := filepath.Dir(tlsIdentity.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"))
	stop, address, grpcAddress := startBoth(t, buildApplication(t), f.dsn, config, settings...)
	defer stop()
	creator := token(t, key, "contract-owner", "gateway:creator")
	base := address + "/api/hypershell/v1/gateways"
	body, _ := json.Marshal(f.request("without-database-field"))
	code, output := requestJSON(t, "POST", base, creator, body)
	var row gatewayResponse
	if code != 201 || json.Unmarshal(output, &row) != nil || row.ID == "" {
		t.Fatal("creation without the retired field failed", code)
	}
	var responseFields map[string]json.RawMessage
	if json.Unmarshal(output, &responseFields) != nil {
		t.Fatal("invalid Gateway response")
	}
	if _, present := responseFields["database_id"]; present {
		t.Fatal("Gateway response contains the retired field")
	}
	var catalog, column bool
	if err := f.db.QueryRow(`SELECT to_regclass('public.managed_databases') IS NOT NULL, EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='gateways' AND column_name='database_id')`).Scan(&catalog, &column); err != nil || catalog || column {
		t.Fatal("fresh schema retains the database catalog", err)
	}
	retiredRequest, err := http.NewRequest(http.MethodGet, address+"/api/hypershell/v1/managed_databases", nil)
	if err != nil {
		t.Fatal(err)
	}
	retiredRequest.Header.Set("Authorization", "Bearer "+creator)
	retiredResponse, err := (&http.Client{Timeout: 5 * time.Second}).Do(retiredRequest)
	if err != nil {
		t.Fatal(err)
	}
	retiredResponse.Body.Close()
	if retiredResponse.StatusCode != http.StatusNotFound {
		t.Fatal("retired database API is still registered", retiredResponse.StatusCode)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, _ := grpcClient(t, grpcAddress, tlsIdentity)
	call := func(bearer string) context.Context {
		return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+bearer))
	}
	created, err := client.CreateGateway(call(creator), &pb.CreateGatewayRequest{Name: "grpc-without-database", ClusterId: f.cluster, ReleaseId: f.release})
	if err != nil || created.GetGateway().GetMetadata().GetId() == "" {
		t.Fatal("gRPC creation without the retired field failed", err)
	}
	awaitQueueEmpty(t, f)
	// Keep an insert record after delivery removes each outbox row.
	if _, err := f.db.Exec(`CREATE TABLE request_contract_events(kind text NOT NULL);
CREATE FUNCTION public.audit_request_contract() RETURNS trigger LANGUAGE plpgsql
SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
BEGIN INSERT INTO public.request_contract_events VALUES (NEW.kind); RETURN NEW; END $$;
REVOKE ALL ON FUNCTION public.audit_request_contract() FROM PUBLIC;
CREATE TRIGGER audit_request_contract AFTER INSERT ON stego_outbox.messages
FOR EACH ROW EXECUTE FUNCTION audit_request_contract()`); err != nil {
		t.Fatal(err)
	}
	requireFixtureRuntimePermissionDenied(t, f, "INSERT INTO public.request_contract_events(kind) VALUES ('direct-runtime-write')")
	before := map[string]int{}
	for _, table := range []string{"gateways", "users", "role_bindings", "request_contract_events"} {
		before[table] = count(t, f.db, table)
	}
	for _, value := range []string{`""`, `null`, `"retired-server"`} {
		create := []byte(fmt.Sprintf(`{"name":"rejected","cluster_id":%q,"release_id":%q,"database_id":%s}`, f.cluster, f.release, value))
		if code, _ := requestJSON(t, "POST", base, creator, create); code != 400 {
			t.Fatal("REST creation accepted the retired field", code)
		}
		patch := []byte(fmt.Sprintf(`{"name":"must-not-change","database_id":%s}`, value))
		if code, _ := requestJSON(t, "PATCH", base+"/"+row.ID, creator, patch); code != 400 {
			t.Fatal("REST patch accepted the retired field", code)
		}
	}
	for _, value := range []string{"", "retired-server"} {
		create := &pb.CreateGatewayRequest{Name: "rejected", ClusterId: f.cluster, ReleaseId: f.release}
		create.ProtoReflect().SetUnknown(protowire.AppendString(protowire.AppendTag(nil, 5, protowire.BytesType), value))
		if _, err := client.CreateGateway(call(creator), create); status.Code(err) != codes.InvalidArgument {
			t.Fatal("gRPC creation accepted the retired wire field", err)
		}
		patch := &pb.UpdateGatewayRequest{Id: row.ID}
		patch.ProtoReflect().SetUnknown(protowire.AppendString(protowire.AppendTag(nil, 6, protowire.BytesType), value))
		if _, err := client.UpdateGateway(call(creator), patch); status.Code(err) != codes.InvalidArgument {
			t.Fatal("gRPC patch accepted the retired wire field", err)
		}
	}
	for table, expected := range before {
		if count(t, f.db, table) != expected {
			t.Fatal("rejected request changed stored state", table)
		}
	}
	read, err := client.GetGateway(call(creator), &pb.GetGatewayRequest{Id: row.ID})
	if err != nil || read.GetGateway().GetName() != row.Name {
		t.Fatal("rejected patch changed the Gateway", err)
	}
}
