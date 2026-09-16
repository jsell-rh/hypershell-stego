package acceptance

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"os"
	"strings"
	"testing"
	"time"

	logcollector "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	metriccollector "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	tracecollector "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestBrowserCollectorRequiresCredentialsForAllSignals(t *testing.T) {
	_, settings := newAuthenticatedHTTPDiagnosticCollectorAt(t, "localhost", "127.0.0.1:0")
	values := map[string]string{}
	for _, entry := range settings {
		key, value, _ := strings.Cut(entry, "=")
		values[key] = value
	}
	ca, err := os.ReadFile(values["OTEL_EXPORTER_OTLP_CERTIFICATE"])
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("collector trust is missing")
	}
	token, err := os.ReadFile(values["STEGO_OTEL_TOKEN_FILE"])
	if err != nil {
		t.Fatal(err)
	}
	defer clear(token)
	connection, err := grpc.NewClient(strings.TrimPrefix(values["OTEL_EXPORTER_OTLP_ENDPOINT"], "https://"), grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: "localhost"})))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	for _, signal := range []struct {
		name string
		send func(context.Context) error
	}{
		{"traces", func(ctx context.Context) error {
			_, err := tracecollector.NewTraceServiceClient(connection).Export(ctx, &tracecollector.ExportTraceServiceRequest{})
			return err
		}},
		{"metrics", func(ctx context.Context) error {
			_, err := metriccollector.NewMetricsServiceClient(connection).Export(ctx, &metriccollector.ExportMetricsServiceRequest{})
			return err
		}},
		{"logs", func(ctx context.Context) error {
			_, err := logcollector.NewLogsServiceClient(connection).Export(ctx, &logcollector.ExportLogsServiceRequest{})
			return err
		}},
	} {
		t.Run(signal.name, func(t *testing.T) {
			for _, probe := range []struct {
				name   string
				values []string
				want   codes.Code
			}{
				{"missing", nil, codes.Unauthenticated},
				{"incorrect", []string{"Bearer invalid"}, codes.Unauthenticated},
				{"repeated", []string{"Bearer " + string(token), "Bearer " + string(token)}, codes.Unauthenticated},
				{"valid", []string{"Bearer " + string(token)}, codes.OK},
			} {
				t.Run(probe.name, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					ctx = metadata.NewOutgoingContext(ctx, metadata.MD{"authorization": probe.values})
					if got := status.Code(signal.send(ctx)); got != probe.want {
						t.Fatal("collector authorization differs", got, probe.want)
					}
				})
			}
		})
	}
}
