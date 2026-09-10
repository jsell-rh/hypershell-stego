// Package cleanupmetrics maps authorized Hypershell summaries to STEGO metrics.
package cleanupmetrics

import (
	"context"
	"fmt"

	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func Gateway(api control.GatewayIdentityServiceClient, owner, target string) func(context.Context) (runtime.CleanupSample, error) {
	return func(ctx context.Context) (runtime.CleanupSample, error) {
		if api == nil {
			return runtime.CleanupSample{}, runtime.ErrMetricsContract
		}
		response, err := api.GetGatewayCleanupSummary(ctx, &control.GetGatewayCleanupSummaryRequest{Owner: owner, Target: target})
		return sample(response, err, owner, target, "")
	}
}
func Database(api control.DatabaseCleanupServiceClient, provider string) func(context.Context) (runtime.CleanupSample, error) {
	return func(ctx context.Context) (runtime.CleanupSample, error) {
		if api == nil {
			return runtime.CleanupSample{}, runtime.ErrMetricsContract
		}
		response, err := api.GetDatabaseCleanupSummary(ctx, &control.GetDatabaseCleanupSummaryRequest{Owner: "provider", Provider: provider})
		return sample(response, err, "provider", "", provider)
	}
}
func sample(response *control.CleanupSummary, err error, owner, target, provider string) (runtime.CleanupSample, error) {
	var result runtime.CleanupSample
	if err != nil {
		if status.Code(err) == codes.Unimplemented || status.Code(err) == codes.InvalidArgument {
			return result, fmt.Errorf("%w: cleanup summary API is unavailable", runtime.ErrMetricsContract)
		}
		return result, err
	}
	if response == nil || response.Owner != owner || response.Target != target || response.Provider != provider || response.ObservedAt == nil || response.ObservedAt.CheckValid() != nil || (response.OldestPending != nil && response.OldestPending.CheckValid() != nil) {
		return result, runtime.ErrMetricsContract
	}
	result.Pending = response.Pending
	if response.OldestPending != nil {
		value := response.OldestPending.AsTime()
		result.OldestPending = &value
	}
	return result, nil
}
