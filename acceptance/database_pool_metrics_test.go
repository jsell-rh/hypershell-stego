package acceptance

import (
	"bytes"
	"strings"
	"testing"
	"time"

	metricpb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"
)

type gatewayPoolSnapshot struct {
	Collected                uint64
	Instance                 string
	Used, Idle, Limit, Waits int64
	WaitSeconds              float64
}

func awaitGatewayPoolMetrics(t *testing.T, collector *httpDiagnosticCollector, private []string, accept func(gatewayPoolSnapshot) bool) gatewayPoolSnapshot {
	t.Helper()
	deadline := time.NewTimer(4 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case batch := <-collector.metrics.received:
			for _, resource := range batch.ResourceMetrics {
				for _, scope := range resource.ScopeMetrics {
					if scope.Scope.Name != "stego/database" {
						continue
					}
					metrics := map[string]*metricpb.Metric{}
					for _, metric := range scope.Metrics {
						if !strings.HasPrefix(metric.Name, "stego.db.pool.") {
							continue
						}
						if metrics[metric.Name] != nil {
							t.Fatal("duplicate pool metric")
						}
						raw, err := proto.Marshal(metric)
						if err != nil {
							t.Fatal(err)
						}
						for _, value := range private {
							if value != "" && bytes.Contains(raw, []byte(value)) {
								t.Fatal("pool metric contains private data")
							}
						}
						metrics[metric.Name] = metric
					}
					if len(metrics) == 0 {
						continue
					}
					if len(metrics) != 5 {
						t.Fatal("pool metric set differs")
					}
					snapshot := gatewayPoolSnapshot{Instance: telemetryInstance(t, resource.Resource.Attributes, "hypershell-pool-api")}
					connections := metrics["stego.db.pool.connections"]
					if connections == nil || connections.Unit != "{connection}" || len(connections.GetGauge().GetDataPoints()) != 2 {
						t.Fatal("connection gauges differ")
					}
					states := map[string]bool{}
					for _, point := range connections.GetGauge().DataPoints {
						state := signalAttribute(point.Attributes, "state").GetStringValue()
						if len(point.Attributes) != 1 || states[state] || point.GetAsInt() < 0 {
							t.Fatal("connection state differs")
						}
						states[state] = true
						switch state {
						case "used":
							snapshot.Used = point.GetAsInt()
						case "idle":
							snapshot.Idle = point.GetAsInt()
						default:
							t.Fatal("unknown connection state")
						}
					}
					limit := metrics["stego.db.pool.limit"]
					if limit == nil || limit.Unit != "{connection}" || len(limit.GetGauge().GetDataPoints()) != 1 || len(limit.GetGauge().DataPoints[0].Attributes) != 0 {
						t.Fatal("pool limit differs")
					}
					snapshot.Limit = limit.GetGauge().DataPoints[0].GetAsInt()
					snapshot.Collected = limit.GetGauge().DataPoints[0].TimeUnixNano
					if snapshot.Collected == 0 {
						t.Fatal("pool metric has no collection time")
					}
					if snapshot.Limit != 2 || snapshot.Used+snapshot.Idle > snapshot.Limit {
						t.Fatal("pool metrics exceed the configured budget")
					}
					for name, unit := range map[string]string{"stego.db.pool.waits": "{wait}", "stego.db.pool.wait.duration": "s", "stego.db.pool.connections.closed": "{connection}"} {
						metric := metrics[name]
						if metric == nil || metric.Unit != unit || !metric.GetSum().GetIsMonotonic() || metric.GetSum().GetAggregationTemporality() != metricpb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE {
							t.Fatal("pool counter differs", name)
						}
						points := metric.GetSum().DataPoints
						if name == "stego.db.pool.connections.closed" {
							if len(points) != 3 {
								t.Fatal("retirement counters differ")
							}
							reasons := map[string]bool{}
							for _, point := range points {
								reason := signalAttribute(point.Attributes, "reason").GetStringValue()
								if len(point.Attributes) != 1 || reasons[reason] || point.GetAsInt() < 0 || reason != "idle_limit" && reason != "idle_time" && reason != "lifetime" {
									t.Fatal("retirement reason differs")
								}
								reasons[reason] = true
							}
						} else {
							if len(points) != 1 || len(points[0].Attributes) != 0 {
								t.Fatal("wait counter attributes differ")
							}
							if name == "stego.db.pool.waits" {
								snapshot.Waits = points[0].GetAsInt()
							} else {
								snapshot.WaitSeconds = points[0].GetAsDouble()
							}
						}
					}
					if snapshot.Waits < 0 || snapshot.WaitSeconds < 0 {
						t.Fatal("negative wait counter")
					}
					if accept(snapshot) {
						return snapshot
					}
				}
			}
		case <-collector.logs.received:
		case <-collector.traces.received:
		case <-deadline.C:
			t.Fatal("required Gateway pool metrics were not delivered")
			return gatewayPoolSnapshot{}
		}
	}
}
