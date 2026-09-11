package acceptance

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func checkRuntimeLogs(t *testing.T, output, service string, private ...string) string {
	t.Helper()
	counts := map[string]int{}
	instance := ""
	for _, line := range strings.Split(output, "\n") {
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal("invalid structured process log", err)
		}
		for _, secret := range private {
			if strings.Contains(line, secret) {
				t.Fatal("process log exposed private data")
			}
		}
		// Database events have a separate field and correlation contract.
		if record["event.name"] == "db.client.operation.completed" {
			continue
		}
		event, ok := record["event.name"].(string)
		if !ok {
			continue
		}
		if event != "telemetry.runtime.started" && event != "telemetry.runtime.stopped" && event != "telemetry.shutdown.incomplete" {
			t.Fatal("unexpected process event", event)
		}
		severity := "INFO"
		if event == "telemetry.shutdown.incomplete" {
			severity = "WARN"
		}
		if len(record) != 6 || record["service.name"] != service || record["severity"] != severity {
			t.Fatal("invalid process event fields")
		}
		id, ok := record["service.instance.id"].(string)
		if !ok || !telemetryInstancePattern.MatchString(id) || (instance != "" && instance != id) {
			t.Fatal("local runtime identity is missing or changed")
		}
		instance = id
		timestamp, ok := record["timestamp"].(string)
		if !ok {
			t.Fatal("missing process event time")
		}
		if _, err := time.Parse(time.RFC3339Nano, timestamp); err != nil {
			t.Fatal("invalid process event time")
		}
		counts[event]++
	}
	if counts["telemetry.runtime.started"] != 1 || counts["telemetry.runtime.stopped"] != 1 {
		t.Fatal("missing or duplicate runtime lifecycle logs", counts)
	}
	return instance
}

func TestGatewayServiceLogsWithoutCollector(t *testing.T) {
	f := database(t)
	_, config := broker(t, identity(t, "localhost"))
	binary := buildApplication(t)
	row, err := f.service.Create(context.Background(), principal("alice", "gateway:creator"), f.request("private-local-log-gateway"))
	if err != nil {
		t.Fatal(err)
	}
	consumer := kafkaConsumer(t, config)
	stop, _, _, _, output := startBothWithLogs(t, binary, f.dsn, config, "OTEL_EXPORTER_OTLP_ENDPOINT=", "OTEL_SERVICE_NAME=hypershell-local")
	if readEvent(t, consumer, row.ID) == "" {
		t.Fatal("Gateway event was not delivered")
	}
	awaitQueueEmpty(t, f)
	stop()
	checkRuntimeLogs(t, output(), "hypershell-local", row.ID, "private-local-log-gateway")
}
