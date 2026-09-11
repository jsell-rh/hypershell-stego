package acceptance

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/hex"
	"net"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	collector "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestGatewayGRPCTracingAcrossWatchRestartAndCollectorFailure(t *testing.T) {
	f := database(t)
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
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"), "STEGO_GRPC_STREAM_TIMEOUT=3s", "OTEL_EXPORTER_OTLP_ENDPOINT=https://"+listener.Addr().String(), "OTEL_EXPORTER_OTLP_PROTOCOL=grpc", "OTEL_EXPORTER_OTLP_CERTIFICATE="+cert.config.CAFile, "OTEL_SERVICE_NAME=hypershell-api-server", "OTEL_TRACES_SAMPLER_ARG=1")
	binary := buildApplication(t)
	stop, _, address := startBoth(t, binary, f.dsn, brokerConfig, settings...)
	defer func() { stop() }()
	client, connection := grpcClient(t, address, cert)
	owner := token(t, key, "alice")
	creator := token(t, key, "alice", "gateway:creator")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	call := func(traceID, credential string) context.Context {
		md := metadata.Pairs("traceparent", "00-"+traceID+"-2222222222222222-01", "baggage", "user=private-trace-user", "tracestate", "vendor=private-trace-state")
		if credential != "" {
			md.Set("authorization", "Bearer "+credential)
		}
		return metadata.NewOutgoingContext(ctx, md)
	}
	pending := map[string]*tracepb.Span{}
	private := []string{owner, creator, "private-trace-user", "private-trace-state", "private-grpc-gateway"}
	await := func(traceID, method, code string, minimum time.Duration) {
		t.Helper()
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		for pending[traceID] == nil {
			select {
			case request := <-sink.received:
				raw, _ := proto.Marshal(request)
				for _, value := range private {
					if bytes.Contains(raw, []byte(value)) {
						t.Fatal("trace contains private Gateway data")
					}
				}
				for _, resource := range request.ResourceSpans {
					attrs := resource.GetResource().GetAttributes()
					telemetryInstance(t, attrs, "hypershell-api-server")
					for _, scope := range resource.ScopeSpans {
						if scope.Scope.GetName() != "stego/grpc" {
							continue
						}
						for _, span := range scope.Spans {
							pending[hex.EncodeToString(span.TraceId)] = span
						}
					}
				}
			case <-timer.C:
				t.Fatal("Gateway gRPC trace was not exported")
			}
		}
		span := pending[traceID]
		delete(pending, traceID)
		if span.Name != "hypershell.v1.GatewayService/"+method || span.Kind != tracepb.Span_SPAN_KIND_SERVER || hex.EncodeToString(span.ParentSpanId) != "2222222222222222" || span.TraceState != "" || len(span.Events) != 0 || len(span.Links) != 0 {
			t.Fatal("invalid gRPC span")
		}
		want := map[string]string{"rpc.system.name": "grpc", "rpc.method": span.Name, "rpc.response.status_code": code}
		if code == "DEADLINE_EXCEEDED" {
			want["error.type"] = code
			if span.Status.GetCode() != tracepb.Status_STATUS_CODE_ERROR {
				t.Fatal("deadline did not mark span failure")
			}
		}
		if len(span.Attributes) != len(want) {
			t.Fatal("unexpected trace attributes")
		}
		for _, attr := range span.Attributes {
			if value, ok := want[attr.Key]; !ok || attr.Value.GetStringValue() != value {
				t.Fatal("invalid RPC trace field")
			}
		}
		if span.EndTimeUnixNano < span.StartTimeUnixNano || time.Duration(span.EndTimeUnixNano-span.StartTimeUnixNano) < minimum {
			t.Fatal("stream trace ended before stream completion")
		}
	}
	const watchID = "11111111111111111111111111111111"
	watch := watchGateways(t, client, call(watchID, owner))
	const createID = "33333333333333333333333333333333"
	created, err := client.CreateGateway(call(createID, creator), &pb.CreateGatewayRequest{Name: "private-grpc-gateway", ClusterId: f.cluster, ReleaseId: f.release})
	if err != nil {
		t.Fatal(err)
	}
	gateway := created.GetGateway()
	id := gateway.GetMetadata().GetId()
	if id == "" {
		t.Fatal("missing Gateway ID")
	}
	private = append(private, id)
	watch.expect(t, pb.EventType_EVENT_TYPE_CREATED, id, gateway.Name)
	await(createID, "CreateGateway", "OK", 0)
	result := watch.next(t)
	if status.Code(result.err) != codes.DeadlineExceeded {
		t.Fatalf("watch did not expire: %v", result.err)
	}
	await(watchID, "WatchGateways", "DEADLINE_EXCEEDED", 2*time.Second)
	const deniedID = "44444444444444444444444444444444"
	if _, err := client.GetGateway(call(deniedID, ""), &pb.GetGatewayRequest{Id: id}); status.Code(err) != codes.Unauthenticated {
		t.Fatal("missing token was accepted", err)
	}
	await(deniedID, "GetGateway", "UNAUTHENTICATED", 0)
	const hiddenID = "55555555555555555555555555555555"
	if _, err := client.GetGateway(call(hiddenID, token(t, key, "bob")), &pb.GetGatewayRequest{Id: id}); status.Code(err) != codes.NotFound {
		t.Fatal("Gateway access was not hidden", err)
	}
	await(hiddenID, "GetGateway", "NOT_FOUND", 0)
	connection.Close()
	stop()
	stop, _, address = startBoth(t, binary, f.dsn, brokerConfig, settings...)
	client, connection = grpcClient(t, address, cert)
	defer connection.Close()
	const restartID = "66666666666666666666666666666666"
	got, err := client.GetGateway(call(restartID, owner), &pb.GetGatewayRequest{Id: id})
	if err != nil || got.GetGateway().GetName() != gateway.Name {
		t.Fatal("Gateway did not survive restart", err)
	}
	await(restartID, "GetGateway", "OK", 0)
	server.Stop()
	if _, err := client.GetGateway(call("77777777777777777777777777777777", owner), &pb.GetGatewayRequest{Id: id}); err != nil {
		t.Fatal("collector failure blocked Gateway access", err)
	}
	started := time.Now()
	stop()
	if time.Since(started) > 8*time.Second {
		t.Fatal("collector failure blocked API shutdown")
	}
}
