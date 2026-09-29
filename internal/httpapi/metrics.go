package httpapi

import (
	"context"
	"io"
	"net/http"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/out/application/contract"
	"github.com/jsell-rh/hypershell-stego/out/application/transport"
)

const gatewayMetricsPath = "/api/hypershell/v1/metrics/gateways"

func registerGatewayMetrics(mux *http.ServeMux, verifier *requestAuth, service *gateways.Service) error {
	handler, err := endpoint(verifier, func(r *http.Request) (struct{}, error) {
		if r.URL.RawQuery != "" {
			return struct{}{}, transport.ErrRequest
		}
		if r.Body != nil {
			data, err := io.ReadAll(io.LimitReader(r.Body, 1))
			if err != nil || len(data) > 0 {
				return struct{}{}, transport.ErrRequest
			}
		}
		return struct{}{}, nil
	}, func(ctx context.Context, _ struct{}) (*contract.GatewayPhaseCounts, error) {
		counts, err := service.PhaseCounts(ctx, gateways.PrincipalFromContext(ctx))
		if err != nil {
			return nil, err
		}
		response := &contract.GatewayPhaseCounts{Href: contract.Apihypershellv1metricsgateways, Kind: contract.GatewayPhaseCountsKindGatewayPhaseCounts}
		response.Counts.Running = counts["Running"]
		response.Counts.Provisioning = counts["Provisioning"]
		response.Counts.Degraded = counts["Degraded"]
		response.Counts.Failed = counts["Failed"]
		return response, nil
	}, http.StatusOK, writeError)
	if err != nil {
		return err
	}
	mux.Handle("GET "+gatewayMetricsPath, handler)
	return nil
}
