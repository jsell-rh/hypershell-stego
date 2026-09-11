package acceptance

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"github.com/jsell-rh/hypershell-stego/out/sdk"
	"github.com/segmentio/ksuid"
	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kmsg"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type advertisedListener struct {
	net.Listener
	address net.Addr
}

func (l advertisedListener) Addr() net.Addr { return l.address }

type serviceAddress string

func (a serviceAddress) Network() string { return "tcp" }
func (a serviceAddress) String() string  { return string(a) }

// TestGeneratedKubernetesServiceGatewayWorkflow runs against a separate Pod.
// The caller supplies an image digest and a namespace with bounded resources.
func TestGeneratedKubernetesServiceGatewayWorkflow(t *testing.T) {
	if os.Getenv("STEGO_TEST_KUBERNETES_SERVICE") != "1" {
		t.Skip("requires the bounded Kubernetes service fixture")
	}
	namespace, image, group := os.Getenv("STEGO_TEST_NAMESPACE"), os.Getenv("STEGO_TEST_SERVICE_IMAGE"), os.Getenv("STEGO_TEST_FS_GROUP")
	if !strings.HasPrefix(namespace, "stego-service-") || image == "" || group == "" {
		t.Fatal("require a dedicated test namespace, image digest, and file group")
	}
	oc := os.Getenv("STEGO_TEST_OC")
	if oc == "" {
		oc = "oc"
	}
	command := func(input []byte, args ...string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 190*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, oc, append([]string{"--namespace=" + namespace, "--request-timeout=180s"}, args...)...)
		cmd.Stdin = bytes.NewReader(input)
		output, err := cmd.CombinedOutput()
		if err != nil {
			if input != nil {
				var resource struct{ Kind string }
				_ = json.Unmarshal(input, &resource)
				t.Fatal("Kubernetes fixture write failed", resource.Kind, err)
			}
			t.Fatalf("Kubernetes fixture command failed: %v\n%s", err, output)
		}
		return output
	}
	apply := func(resource any) {
		t.Helper()
		data, err := json.Marshal(resource)
		if err != nil {
			t.Fatal(err)
		}
		command(data, "apply", "-f", "-")
	}
	f := database(t)
	fixtureHost := "fixture." + namespace + ".svc"
	apiHost := "hypershell." + namespace + ".svc"
	signals, exports := newHTTPDiagnosticCollectorAt(t, fixtureHost, "0.0.0.0:19093")
	kafkaIdentity := identity(t, fixtureHost)
	_, config := broker(t, kafkaIdentity, kfake.ListenFn(func(network, address string) (net.Listener, error) {
		ln, err := net.Listen("tcp", "0.0.0.0:19092")
		if err != nil {
			return nil, err
		}
		return advertisedListener{ln, serviceAddress(fixtureHost + ":19092")}, nil
	}))
	consumer := kafkaConsumer(t, config)
	key, auth := issuer(t)
	auth = append(auth, `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["gateway-controller"]`)
	auth = withCleanupGrants(t, auth, cleanupGrant("gateway-controller", "Gateway", "identity", ""))
	auth = withControllerWriteGrants(t, auth, writeGrant("gateway-controller", "configure.identity", ""))
	apiIdentity := identity(t, apiHost)
	apiDirectory := filepath.Dir(apiIdentity.config.CAFile)
	cfg, err := pgx.ParseConfig(f.dsn)
	if err != nil {
		t.Fatal(err)
	}
	role := "service_" + strings.TrimPrefix(cfg.Database, "hypershell_test_")
	identifier := pgx.Identifier{role}.Sanitize()
	password := make([]byte, 24)
	if _, err := rand.Read(password); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec("CREATE ROLE " + identifier + " LOGIN PASSWORD '" + hex.EncodeToString(password) + "'"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := f.db.Exec("DROP OWNED BY " + identifier); err != nil {
			t.Error(err)
		}
		if _, err := f.db.Exec("DROP ROLE " + identifier); err != nil {
			t.Error(err)
		}
	})
	for _, sql := range []string{"GRANT CONNECT ON DATABASE " + pgx.Identifier{cfg.Database}.Sanitize() + " TO " + identifier, "GRANT USAGE ON SCHEMA public, stego_outbox TO " + identifier, "GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public, stego_outbox TO " + identifier, "GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public, stego_outbox TO " + identifier} {
		if _, err := f.db.Exec(sql); err != nil {
			t.Fatal(err)
		}
	}
	dsn := &url.URL{Scheme: "postgres", Host: fixtureHost + ":5432", Path: "/" + cfg.Database, User: url.UserPassword(role, hex.EncodeToString(password))}
	dsn.RawQuery = url.Values{"sslmode": {"verify-full"}, "sslrootcert": {"/var/run/stego/database-ca.pem"}, "application_name": {"stego-kubernetes-service"}}.Encode()
	files := map[string][]byte{"database-url": []byte(dsn.String())}
	read := func(name string) []byte {
		t.Helper()
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	for name, source := range map[string]string{"tls.crt": filepath.Join(apiDirectory, "server.pem"), "tls.key": filepath.Join(apiDirectory, "server-key.pem"), "database-ca.pem": os.Getenv("STEGO_TEST_POSTGRES_CA_FILE"), "kafka-ca.pem": config.CAFile, "kafka-client.pem": config.ClientCertificateFile, "kafka-key.pem": config.ClientKeyFile} {
		files[name] = read(source)
	}
	environment := map[string]string{"DATABASE_URL_FILE": "/var/run/stego/database-url", "DATABASE_PROVIDER": "cnpg", "STEGO_KAFKA_BROKERS": fixtureHost + ":19092", "STEGO_KAFKA_TOPIC": config.Topic, "STEGO_KAFKA_AUTHENTICATION": "mtls", "STEGO_KAFKA_CA_FILE": "/var/run/stego/kafka-ca.pem", "STEGO_KAFKA_CLIENT_CERTIFICATE_FILE": "/var/run/stego/kafka-client.pem", "STEGO_KAFKA_CLIENT_KEY_FILE": "/var/run/stego/kafka-key.pem", "OTEL_SERVICE_NAME": "hypershell-deployment"}
	for _, entry := range auth {
		name, value, _ := strings.Cut(entry, "=")
		if name == "STEGO_AUTH_PUBLIC_KEY_FILE" {
			files["issuer.pem"] = read(value)
			value = "/var/run/stego/issuer.pem"
		}
		environment[name] = value
	}
	for _, entry := range exports {
		name, value, _ := strings.Cut(entry, "=")
		if name == "OTEL_METRIC_EXPORT_INTERVAL" {
			// Reduce collector buffer use during provider startup.
			value = "10000"
		}
		if name == "OTEL_EXPORTER_OTLP_ENDPOINT" {
			value = "https://" + fixtureHost + ":19093"
		}
		if name == "OTEL_EXPORTER_OTLP_CERTIFICATE" {
			files["telemetry-ca.pem"] = read(value)
			value = "/var/run/stego/telemetry-ca.pem"
		}
		environment[name] = value
	}
	apply(map[string]any{"apiVersion": "v1", "kind": "Secret", "metadata": map[string]any{"name": "hypershell-files", "namespace": namespace}, "data": files})
	apply(map[string]any{"apiVersion": "v1", "kind": "Secret", "metadata": map[string]any{"name": "hypershell-runtime", "namespace": namespace}, "stringData": environment})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	render := exec.CommandContext(ctx, "go", "run", "-mod=readonly", "../out/deploy/render", "--image", image, "--namespace", namespace, "--fs-group", group)
	manifest, err := render.Output()
	if err != nil {
		t.Fatal("generated deployment renderer failed", err)
	}
	command(manifest, "apply", "-f", "-")
	t.Cleanup(func() {
		command(nil, "delete", "deployment/hypershell", "--wait=true", "--timeout=60s", "--ignore-not-found")
		command(nil, "delete", "secret/hypershell-files", "secret/hypershell-runtime", "service/hypershell", "networkpolicy/hypershell", "serviceaccount/hypershell", "--ignore-not-found")
	})
	ready := func() { t.Helper(); command(nil, "rollout", "status", "deployment/hypershell", "--timeout=180s") }
	ready()
	pod := func() string {
		t.Helper()
		var list struct {
			Items []struct {
				Metadata struct {
					UID               string
					DeletionTimestamp *string
				}
			}
		}
		if err := json.Unmarshal(command(nil, "get", "pods", "-l", "app.kubernetes.io/name=hypershell", "-o", "json"), &list); err != nil {
			t.Fatal(err)
		}
		for _, item := range list.Items {
			if item.Metadata.DeletionTimestamp == nil && item.Metadata.UID != "" {
				return item.Metadata.UID
			}
		}
		t.Fatal("deployment has no active Pod")
		return ""
	}
	firstPod := pod()
	ownerToken, otherToken := token(t, key, "alice", "gateway:creator"), token(t, key, "mallory")
	client := func(token string) *sdk.Client {
		t.Helper()
		c, err := sdk.NewClient(sdk.Options{BaseURL: "https://" + apiHost + ":8443", CAFile: apiIdentity.config.CAFile, Token: token})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(c.Close)
		return c
	}
	owner, other := client(ownerToken), client(otherToken)
	requestContext, cancelRequests := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancelRequests()
	input := sdk.CreateGatewayJSONRequestBody{Name: "deployed-gateway", ClusterId: f.cluster, ReleaseId: f.release, DatabaseId: ""}
	result, err := owner.CreateGatewayWithResponse(requestContext, input)
	if err != nil || result.JSON201 == nil || result.JSON201.Id == nil {
		t.Fatal("deployed Gateway creation failed", err)
	}
	gateway := result.JSON201
	id := *gateway.Id
	parsed, err := ksuid.Parse(id)
	if err != nil || gateway.Namespace == nil || *gateway.Namespace != "openshell-"+hex.EncodeToString(parsed.Payload()[:8]) || gateway.DatabaseId == "" || gateway.CreatedAt == nil || gateway.UpdatedAt == nil {
		t.Fatal("deployed Gateway lost its assigned fields")
	}
	var grants int
	if err := f.db.QueryRow(`SELECT count(*) FROM role_bindings b JOIN roles r ON r.id=b.role_id JOIN users u ON u.id=b.user_id WHERE b.gateway_id=$1 AND b.scope='gateway' AND r.name='gateway:owner' AND u.username='alice'`, id).Scan(&grants); err != nil || grants != 1 {
		t.Fatal("deployed Gateway has no owner grant", err)
	}
	readEvent(t, consumer, id)
	checkRead := func() {
		t.Helper()
		got, err := owner.GetGatewayWithResponse(requestContext, id)
		if err != nil || got.JSON200 == nil || got.JSON200.Id == nil || *got.JSON200.Id != id {
			t.Fatal("deployed Gateway read failed", err)
		}
		denied, err := other.GetGatewayWithResponse(requestContext, id)
		if err != nil || denied.StatusCode() != http.StatusNotFound {
			t.Fatal("deployed Gateway access was not denied", err)
		}
		for _, check := range []struct {
			client *sdk.Client
			total  int
		}{{owner, 1}, {other, 0}} {
			search := "name = 'deployed-gateway'"
			list, err := check.client.ListGatewaysWithResponse(requestContext, &sdk.ListGatewaysParams{Search: &search})
			if err != nil || list.JSON200 == nil || list.JSON200.Total == nil || *list.JSON200.Total != check.total || list.JSON200.Items == nil || len(*list.JSON200.Items) != check.total {
				t.Fatal("deployed list filter differs", err)
			}
		}
		rpc, connection := grpcClient(t, apiHost+":9090", apiIdentity)
		defer connection.Close()
		gotRPC, err := rpc.GetGateway(metadata.NewOutgoingContext(requestContext, metadata.Pairs("authorization", "Bearer "+ownerToken)), &pb.GetGatewayRequest{Id: id})
		if err != nil || gotRPC.GetGateway().GetMetadata().GetId() != id {
			t.Fatal("deployed gRPC read failed", err)
		}
		_, err = rpc.GetGateway(metadata.NewOutgoingContext(requestContext, metadata.Pairs("authorization", "Bearer "+otherToken)), &pb.GetGatewayRequest{Id: id})
		if status.Code(err) != codes.NotFound {
			t.Fatal("deployed gRPC access was not denied", err)
		}
		for _, check := range []struct {
			bearer string
			total  int
		}{{ownerToken, 1}, {otherToken, 0}} {
			list, err := rpc.ListGateways(metadata.NewOutgoingContext(requestContext, metadata.Pairs("authorization", "Bearer "+check.bearer)), &pb.ListGatewaysRequest{Page: 1, Size: 20})
			if err != nil || list.GetMetadata().GetTotal() != int32(check.total) || len(list.GetItems()) != check.total {
				t.Fatal("deployed gRPC list filter differs", err)
			}
		}
	}
	checkRead()
	controllerToken := token(t, key, "gateway-controller")
	reconcile := checkKubernetesGatewayIdentity(t, namespace, apply, command, owner, apiHost, apiIdentity, controllerToken, id, exports)
	awaitQueueEmpty(t, f)
	beforeGrants, beforeDatabases := count(t, f.db, "role_bindings"), count(t, f.db, "managed_databases")
	if _, err := f.db.Exec("ALTER TABLE stego_outbox.messages ADD CONSTRAINT reject_deployment_event CHECK (kind <> 'gateway.created') NOT VALID"); err != nil {
		t.Fatal(err)
	}
	failed := input
	failed.Name = "deployed-rollback"
	response, err := owner.CreateGatewayWithResponse(requestContext, failed)
	if err != nil || response.StatusCode() != 500 {
		t.Fatal("deployed event failure did not reject creation", err)
	}
	if count(t, f.db, "gateways") != 1 || count(t, f.db, "role_bindings") != beforeGrants || count(t, f.db, "managed_databases") != beforeDatabases {
		t.Fatal("deployed failed transaction left records")
	}
	if _, err := f.db.Exec("ALTER TABLE stego_outbox.messages DROP CONSTRAINT reject_deployment_event"); err != nil {
		t.Fatal(err)
	}
	command(nil, "rollout", "restart", "deployment/hypershell")
	ready()
	if firstPod == pod() {
		t.Fatal("deployment did not replace its Pod")
	}
	checkRead()
	reconcile()
	// All prior writers have stopped. Capture the broker offset after their
	// outbox records drain, so an old identity event cannot satisfy this check.
	awaitQueueEmpty(t, f)
	offsetRequest := kmsg.NewPtrListOffsetsRequest()
	partition := kmsg.NewListOffsetsRequestTopicPartition()
	partition.Partition, partition.Timestamp = 0, -1
	offsetRequest.Topics = []kmsg.ListOffsetsRequestTopic{{Topic: config.Topic, Partitions: []kmsg.ListOffsetsRequestTopicPartition{partition}}}
	offsetContext, stopOffset := context.WithTimeout(requestContext, 10*time.Second)
	offsets, err := offsetRequest.RequestWith(offsetContext, consumer)
	stopOffset()
	if err != nil || offsets == nil || len(offsets.Topics) != 1 || offsets.Topics[0].Topic != config.Topic || len(offsets.Topics[0].Partitions) != 1 {
		t.Fatal("cannot read the event boundary", err)
	}
	boundary := offsets.Topics[0].Partitions[0]
	if boundary.ErrorCode != 0 || boundary.Partition != 0 || boundary.Offset < 1 {
		t.Fatal("invalid event boundary")
	}
	imageUpdate := "example.test/gateway:v2"
	updated, err := owner.UpdateGatewayWithResponse(requestContext, id, sdk.UpdateGatewayJSONRequestBody{Image: &imageUpdate})
	if err != nil || updated.JSON200 == nil {
		t.Fatal("deployed update after restart failed", err)
	}
	readGatewayEvent(t, consumer, id, "Update", "gateway.updated", boundary.Offset)
	command(nil, "logs", "deployment/hypershell", "--tail=200")
	command(nil, "delete", "deployment/hypershell", "--wait=true", "--timeout=60s")
	command(nil, "wait", "--for=delete", "pods", "-l", "app.kubernetes.io/name=hypershell", "--timeout=60s")
	checkDeployedSignals(t, signals, []string{ownerToken, otherToken, controllerToken, "acceptance-only-admin-secret", id, input.Name, failed.Name, dsn.String(), hex.EncodeToString(password)})
	t.Log("Generated Deployment passed HTTPS, gRPC, owner grant, rollback, filtered access, event delivery, and Pod replacement")
}

func checkDeployedSignals(t *testing.T, signals *httpDiagnosticCollector, private []string) {
	t.Helper()
	check := func(message proto.Message) {
		data, err := proto.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range private {
			if bytes.Contains(data, []byte(value)) {
				t.Fatal("deployed telemetry exposed private data")
			}
		}
	}
	services := map[string]string{}
	identity := func(attrs []*commonpb.KeyValue) (string, bool) {
		t.Helper()
		service := signalAttribute(attrs, "service.name").GetStringValue()
		if service != "hypershell-deployment" && service != "hypershell-gateway-identity" {
			t.Fatal("unexpected telemetry service")
		}
		instance := telemetryInstance(t, attrs, service)
		if prior, ok := services[instance]; ok && prior != service {
			t.Fatal("telemetry instance crossed services")
		}
		services[instance] = service
		return instance, service == "hypershell-gateway-identity"
	}
	spans, logs, metrics := map[string]bool{}, map[string]bool{}, map[string]bool{}
	spanIDs, logIDs := map[string]string{}, map[string]string{}
	for len(signals.traces.received) > 0 {
		batch := <-signals.traces.received
		check(batch)
		for _, resource := range batch.ResourceSpans {
			instance, worker := identity(resource.Resource.Attributes)
			for _, scope := range resource.ScopeSpans {
				for _, span := range scope.Spans {
					if (!worker && span.Name != "" && span.Kind == tracepb.Span_SPAN_KIND_SERVER) || (worker && span.Name == "controller.reconcile" && span.Kind == tracepb.Span_SPAN_KIND_INTERNAL) {
						spans[instance] = true
						spanIDs[hex.EncodeToString(span.TraceId)+hex.EncodeToString(span.SpanId)] = instance
					}
				}
			}
		}
	}
	for len(signals.logs.received) > 0 {
		batch := <-signals.logs.received
		check(batch)
		for _, resource := range batch.ResourceLogs {
			instance, worker := identity(resource.Resource.Attributes)
			for _, scope := range resource.ScopeLogs {
				for _, record := range scope.LogRecords {
					if (!worker && record.EventName == "http.server.request.completed") || (worker && record.EventName == "controller.work.completed" && signalAttribute(record.Attributes, "operation").GetStringValue() == "reconcile") {
						logs[instance] = true
						logIDs[hex.EncodeToString(record.TraceId)+hex.EncodeToString(record.SpanId)] = instance
					}
				}
			}
		}
	}
	for len(signals.metrics.received) > 0 {
		batch := <-signals.metrics.received
		check(batch)
		for _, resource := range batch.ResourceMetrics {
			instance, worker := identity(resource.Resource.Attributes)
			for _, scope := range resource.ScopeMetrics {
				for _, metric := range scope.Metrics {
					if (!worker && metric.Name == "http.server.request.duration") || (worker && metric.Name == "stego.controller.work.duration") {
						for _, point := range metric.GetHistogram().GetDataPoints() {
							if point.Count > 0 {
								metrics[instance] = true
							}
						}
					}
				}
			}
		}
	}
	matched := map[string]bool{}
	for key, instance := range spanIDs {
		if logIDs[key] == instance {
			matched[instance] = true
		}
	}
	for _, service := range []string{"hypershell-deployment", "hypershell-gateway-identity"} {
		count := func(set map[string]bool) int {
			n := 0
			for instance := range set {
				if services[instance] == service {
					n++
				}
			}
			return n
		}
		if count(spans) != 2 || count(logs) != 2 || count(metrics) != 2 || count(matched) != 2 {
			t.Fatal("both deployed instances require correlated spans, logs, and metrics", service, count(spans), count(logs), count(metrics), count(matched))
		}
	}
}
