package acceptance

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	collector "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/proto"
)

type workflowTraceCollector struct {
	collector.UnimplementedTraceServiceServer
	received chan *collector.ExportTraceServiceRequest
}

func (c *workflowTraceCollector) Export(ctx context.Context, value *collector.ExportTraceServiceRequest) (*collector.ExportTraceServiceResponse, error) {
	select {
	case c.received <- value:
		return &collector.ExportTraceServiceResponse{}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func TestGatewayHTTPTracingAcrossRestartAndCollectorFailure(t *testing.T) {
	f := database(t)
	gateway, err := f.service.Create(context.Background(), principal("alice", "gateway:creator"), f.request("private-trace-gateway"))
	if err != nil {
		t.Fatal(err)
	}
	cert := identity(t, "localhost")
	directory := filepath.Dir(cert.config.CAFile)
	pair, err := tls.LoadX509KeyPair(filepath.Join(directory, "server.pem"), filepath.Join(directory, "server-key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{pair}})))
	sink := &workflowTraceCollector{received: make(chan *collector.ExportTraceServiceRequest, 64)}
	collector.RegisterTraceServiceServer(server, sink)
	go server.Serve(listener)
	defer server.Stop()
	_, brokerConfig := broker(t, identity(t, "localhost"))
	key, settings := issuer(t)
	settings = append(settings, "OTEL_EXPORTER_OTLP_ENDPOINT=https://"+listener.Addr().String(), "OTEL_EXPORTER_OTLP_PROTOCOL=grpc", "OTEL_EXPORTER_OTLP_CERTIFICATE="+cert.config.CAFile, "OTEL_SERVICE_NAME=hypershell-api-server", "OTEL_TRACES_SAMPLER_ARG=1")
	binary := buildApplication(t)
	stop, address := startApplication(t, binary, f.dsn, brokerConfig, settings...)
	defer func() { stop() }()
	owner := token(t, key, "alice")
	client := &http.Client{Timeout: time.Second}
	call := func(traceID, credential string, want int) {
		t.Helper()
		request, err := http.NewRequest("GET", address+"/api/hypershell/v1/gateways/"+gateway.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("traceparent", "00-"+traceID+"-2222222222222222-01")
		request.Header.Set("baggage", "user=private-telemetry-user")
		request.Header.Set("tracestate", "vendor=private-telemetry-state")
		if credential != "" {
			request.Header.Set("Authorization", "Bearer "+credential)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != want {
			t.Fatal("tracing changed Gateway response", response.StatusCode, want)
		}
	}
	await := func(traceID string, wantStatus int) {
		t.Helper()
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		for {
			select {
			case request := <-sink.received:
				raw, _ := proto.Marshal(request)
				for _, private := range []string{owner, gateway.ID, gateway.Name, "private-query", "private-telemetry-user", "private-telemetry-state"} {
					if bytes.Contains(raw, []byte(private)) {
						t.Fatal("trace contains private Gateway data")
					}
				}
				for _, resource := range request.ResourceSpans {
					attributes := resource.GetResource().GetAttributes()
					if len(attributes) != 1 || attributes[0].Key != "service.name" || attributes[0].Value.GetStringValue() != "hypershell-api-server" {
						t.Fatal("trace lost its declared service identity")
					}
					for _, scope := range resource.ScopeSpans {
						for _, span := range scope.Spans {
							if hex.EncodeToString(span.TraceId) == traceID {
								if hex.EncodeToString(span.ParentSpanId) != "2222222222222222" || span.TraceState != "" || len(span.Events) != 0 || len(span.Links) != 0 {
									t.Fatal("trace lost its parent or added undeclared fields")
								}
								if span.Name != "GET /api/hypershell/v1/gateways/{id}" || span.Kind != tracepb.Span_SPAN_KIND_SERVER || len(span.Attributes) != 3 {
									t.Fatal("invalid HTTP span")
								}
								found := false
								for _, attribute := range span.Attributes {
									if attribute.Key == "http.response.status_code" && attribute.Value.GetIntValue() == int64(wantStatus) {
										found = true
									}
								}
								if !found {
									t.Fatal("trace lost response status")
								}
								return
							}
						}
					}
				}
			case <-timer.C:
				t.Fatal("Gateway trace was not exported")
			}
		}
	}
	first := "11111111111111111111111111111111"
	call(first, owner, 200)
	await(first, 200)
	denied := "33333333333333333333333333333333"
	call(denied, "", 401)
	await(denied, 401)
	stop()
	stop, address = startApplication(t, binary, f.dsn, brokerConfig, settings...)
	after := "44444444444444444444444444444444"
	call(after, owner, 200)
	await(after, 200)
	server.Stop()
	call("55555555555555555555555555555555", owner, 200)
	started := time.Now()
	stop()
	if time.Since(started) > 8*time.Second {
		t.Fatal("collector loss blocked API shutdown")
	}
}
