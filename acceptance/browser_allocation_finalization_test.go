package acceptance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gatewayworkload"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// The allocator is stopped before SQL access is restored. Other controllers
// can finish their cleanup, but retained namespace removal must still block
// finalization. The caller resumes a new allocator process after this check.
func (w *browserGatewayWorkload) checkPendingAllocationCleanup(id string) func() {
	w.t.Helper()
	record := struct {
		Schema               int               `json:"schema"`
		GatewayID            string            `json:"gateway_id"`
		OtherOwnersComplete  bool              `json:"other_cleanup_owners_complete"`
		AllocationPending    bool              `json:"allocation_pending"`
		NotFinalized         bool              `json:"not_finalized_while_paused"`
		RESTVisible          bool              `json:"rest_deleting_visible"`
		GRPCVisible          bool              `json:"grpc_deleting_visible"`
		RetainedNamespaces   map[string]string `json:"retained_namespace_uids"`
		RecoveredAfterResume bool              `json:"recovered_after_allocator_resume"`
		Complete             bool              `json:"complete"`
	}{Schema: 1, GatewayID: id, RetainedNamespaces: map[string]string{}}
	w.t.Cleanup(func() {
		if directory := os.Getenv("STEGO_BROWSER_ARTIFACT_DIR"); directory != "" {
			data, err := json.MarshalIndent(record, "", "  ")
			if err != nil || os.WriteFile(filepath.Join(directory, "allocation-finalization.json"), append(data, '\n'), 0600) != nil {
				w.t.Error("Cannot save allocation finalization evidence")
			}
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	for {
		var others, pending, finalized bool
		err := w.f.db.QueryRowContext(ctx, `SELECT
COALESCE(stego_cleanup->>'accounts'='true' AND stego_cleanup->>'identity'='true'
 AND stego_cleanup_targets->'workload'->>$2='true'
 AND stego_cleanup_targets->'sql'->>$2='true',false),
COALESCE(deleted_at IS NOT NULL AND stego_cleanup_targets->'allocation'->>$2='false',false),
stego_finalized_at IS NOT NULL FROM gateways WHERE id=$1`, id, w.f.cluster).Scan(&others, &pending, &finalized)
		if err != nil || finalized || !pending {
			w.t.Fatal("Gateway finalized or lost its pending allocation while the allocator was stopped")
		}
		if others {
			record.OtherOwnersComplete, record.AllocationPending, record.NotFinalized = true, true, true
			break
		}
		select {
		case <-ctx.Done():
			w.t.Fatal("Other cleanup owners did not finish while the allocator was stopped")
		case <-time.After(time.Second):
		}
	}
	state, err := gatewayworkload.StateNamespace(id)
	if err != nil {
		w.t.Fatal(err)
	}
	names := []string{state}
	if w.public != nil {
		console, err := gatewayworkload.ConsoleStateNamespace(id)
		if err != nil {
			w.t.Fatal(err)
		}
		names = append(names, console)
	}
	for _, name := range names {
		operation, stop := context.WithTimeout(ctx, 5*time.Second)
		object, code, err := w.kubernetes.Request(operation, "GET", "/api/v1/namespaces/"+name, nil)
		stop()
		if err != nil || code != 200 || kube.String(object, "metadata", "uid") == "" || kube.String(object, "metadata", "deletionTimestamp") != "" {
			w.t.Fatal("Retained state namespace was absent while allocation cleanup was pending", code)
		}
		record.RetainedNamespaces[name] = kube.String(object, "metadata", "uid")
	}
	response := w.owner.api(w.t, "GET", "/gateways/"+id, nil)
	var gateway gatewayResponse
	if response.StatusCode != 200 || json.Unmarshal(response.Body, &gateway) != nil || gateway.ID != id || gateway.Phase == nil || *gateway.Phase != "Deleting" {
		w.t.Fatal("REST hid a Gateway with pending allocation cleanup", response.StatusCode)
	}
	record.RESTVisible = true
	client, connection := grpcClient(w.t, w.apiAddress, testIdentity{config: Config{CAFile: w.apiCA}})
	defer connection.Close()
	bearer := w.identity.browserLogin(w.t, "hypershell", "console-alice")
	operation, stop := context.WithTimeout(ctx, 10*time.Second)
	defer stop()
	operation = metadata.NewOutgoingContext(operation, metadata.Pairs("authorization", "Bearer "+bearer))
	row, err := client.GetGateway(operation, &pb.GetGatewayRequest{Id: id})
	if err != nil || row.GetGateway().GetMetadata().GetId() != id || row.GetGateway().GetPhase() != "Deleting" {
		w.t.Fatal("gRPC hid a Gateway with pending allocation cleanup", status.Code(err))
	}
	record.GRPCVisible = true
	w.t.Log("REST and gRPC retained the deleting Gateway after all other cleanup owners completed; its allocator was stopped and state namespaces remained")
	return func() {
		// The caller first awaits all namespace, binding, SQL, cleanup record,
		// and final public absence checks through the existing workflow.
		record.RecoveredAfterResume, record.Complete = true, true
	}
}
