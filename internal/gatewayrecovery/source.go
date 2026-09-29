// Package gatewayrecovery maps the private Gateway recovery API to cursor pages.
package gatewayrecovery

import (
	"context"
	"fmt"

	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	"github.com/segmentio/ksuid"
)

const PageSize = 100

// Source includes live and retained deleted Gateway IDs. The private API checks
// the control-plane identity. Domain actions must read current trusted state.
// A non-empty cluster restricts pages to that managed cluster.
func Source(api control.GatewayIdentityServiceClient, cluster string) runtime.CursorSource[string] {
	return func(ctx context.Context, after string, limit int) (runtime.CursorPage[string], error) {
		var page runtime.CursorPage[string]
		if api == nil || limit != PageSize {
			return page, fmt.Errorf("%w: invalid Gateway recovery source", runtime.ErrScanContract)
		}
		request := &control.ListGatewayReconcileIDsRequest{AfterId: after, ClusterId: cluster}
		response, err := api.ListGatewayReconcileIDs(ctx, request)
		if err != nil {
			return page, err
		}
		if response == nil || len(response.Ids) > limit {
			return page, fmt.Errorf("%w: invalid Gateway recovery response", runtime.ErrScanContract)
		}
		for _, id := range response.Ids {
			parsed, err := ksuid.Parse(id)
			if err != nil || parsed == ksuid.Nil || parsed.String() != id {
				return runtime.CursorPage[string]{}, fmt.Errorf("%w: invalid Gateway recovery ID", runtime.ErrScanContract)
			}
			page.Items = append(page.Items, runtime.CursorItem[string]{Cursor: id, Value: id})
		}
		page.More = len(response.Ids) == PageSize
		return page, nil
	}
}
