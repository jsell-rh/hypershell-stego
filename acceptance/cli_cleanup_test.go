package acceptance

import (
	"context"
	"testing"
	"time"

	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// CLI contract tests use a controlled provider. Only its declared cleanup
// observations can release parent records. The live browser gate checks effects.
func cliCleanupSettings(t *testing.T, settings []string, cluster string) []string {
	t.Helper()
	settings = append(settings, `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["cli-cleanup"]`)
	return withCleanupGrants(t, settings, cleanupGrant("cli-cleanup", "Gateway", "workload", cluster), cleanupGrant("cli-cleanup", "Gateway", "sql", cluster))
}

func observeCLIGatewayCleanup(t *testing.T, connection *grpc.ClientConn, bearer, cluster string, ids ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+bearer))
	state := control.NewGatewayIdentityServiceClient(connection)
	for _, id := range ids {
		for _, owner := range []string{"sql", "workload"} {
			current, err := state.GetGatewayIdentityState(ctx, &control.GetGatewayIdentityStateRequest{Id: id})
			if err != nil || !current.GetDeleted() {
				t.Fatal("CLI cleanup requires a deleted Gateway", err)
			}
			write, err := rpc.WithResourceVersion(ctx, current.ResourceVersion)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := state.ObserveGatewayCleanup(write, &control.ObserveGatewayCleanupRequest{Id: id, Owner: owner, Target: cluster, Complete: true}); err != nil {
				t.Fatal("CLI provider cleanup failed", err)
			}
		}
	}
}
