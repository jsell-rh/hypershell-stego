package gatewayworkload

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	protocol "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestPublicRouteChecksBeforeAndAfterProbe(t *testing.T) {
	for _, scenario := range []string{"ready", "pending", "foreign owner", "alternate backend", "probe failure", "replaced", "changed"} {
		t.Run(scenario, func(t *testing.T) {
			gw, _ := records(t)
			host := publicHostname(gw.Namespace, "example.test")
			current := definition("route.openshift.io/v1", "Route", Name, gw.Metadata.Id)
			current["metadata"].(object)["namespace"] = gw.Namespace
			current["metadata"].(object)["uid"] = "route-one"
			current["metadata"].(object)["resourceVersion"] = "1"
			current["spec"] = object{"host": host, "wildcardPolicy": "None", "to": object{"kind": "Service", "name": Name, "weight": 100}, "port": object{"targetPort": "grpc"}, "tls": object{"termination": "passthrough", "insecureEdgeTerminationPolicy": "None"}}
			condition := "True"
			if scenario == "pending" {
				condition = "False"
			}
			current["status"] = object{"ingress": []object{{"host": host, "routerName": "selected", "conditions": []object{{"type": "Admitted", "status": condition}}}}}
			if scenario == "foreign owner" {
				current["metadata"].(object)["labels"] = object{ownerLabel: "other", managerLabel: manager}
			}
			if scenario == "alternate backend" {
				current["spec"].(object)["alternateBackends"] = []object{{"kind": "Service", "name": "other"}}
			}
			var reads atomic.Int32
			k := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/apis/route.openshift.io/v1/namespaces/"+gw.Namespace+"/routes/"+Name {
					t.Error("unexpected Route request")
					w.WriteHeader(500)
					return
				}
				if reads.Add(1) > 1 {
					if scenario == "changed" {
						current["metadata"].(object)["resourceVersion"] = "2"
					}
					if scenario == "replaced" {
						current["metadata"].(object)["uid"] = "route-two"
					}
				}
				_ = json.NewEncoder(w).Encode(current)
			})
			k.options.PublicDomain = "example.test"
			k.options.PublicRouter = "selected"
			calls := 0
			certificate := []byte("checked-certificate")
			err := k.ensurePublicRoute(context.Background(), gw, certificate, func(_ context.Context, address string, got []byte) error {
				calls++
				if address != host+":443" || !bytes.Equal(got, certificate) {
					t.Error("probe target differs from Route")
				}
				if scenario == "probe failure" {
					return errors.New("private probe failure")
				}
				return nil
			})
			if scenario == "ready" {
				if err != nil || calls != 1 || reads.Load() != 2 {
					t.Fatal("verified Route was not accepted", err)
				}
				return
			}
			if err == nil {
				t.Fatal("unverified Route accepted")
			}
			if scenario == "pending" || scenario == "foreign owner" || scenario == "alternate backend" {
				if calls != 0 {
					t.Fatal("probe ran before Route validation")
				}
			}
			if scenario == "changed" || scenario == "replaced" {
				if !errors.Is(err, ErrPending) || calls != 1 {
					t.Fatal("changed Route was not observed again", err)
				}
			}
		})
	}
}

func TestPublicRouteRemovalContinuesAfterAddressCleared(t *testing.T) {
	gw, _ := records(t)
	current := definition("route.openshift.io/v1", "Route", Name, gw.Metadata.Id)
	current["metadata"].(object)["uid"] = "route-one"
	current["metadata"].(object)["resourceVersion"] = "1"
	var deletes atomic.Int32
	k := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			if deletes.Load() > 0 {
				w.WriteHeader(404)
				return
			}
			_ = json.NewEncoder(w).Encode(current)
		case "DELETE":
			var request struct {
				Preconditions struct{ UID, ResourceVersion string }
			}
			if json.NewDecoder(r.Body).Decode(&request) != nil || request.Preconditions.UID != "route-one" || request.Preconditions.ResourceVersion != "1" {
				t.Error("Route deletion lost its identity condition")
			}
			deletes.Add(1)
			w.WriteHeader(202)
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Error("unexpected Route removal request")
			w.WriteHeader(500)
		}
	})
	if err := k.ensurePublicRoute(context.Background(), gw, nil, nil); !errors.Is(err, ErrPending) {
		t.Fatal("submitted deletion was treated as absence", err)
	}
	if err := k.ensurePublicRoute(context.Background(), gw, nil, nil); err != nil || deletes.Load() != 1 {
		t.Fatal("cleared API address stopped Route cleanup", err)
	}
}

func TestPublicRouteCreationWaitsForAdmission(t *testing.T) {
	gw, _ := records(t)
	created := false
	k := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			w.WriteHeader(404)
			return
		}
		if r.Method != "POST" || r.URL.Path != "/apis/route.openshift.io/v1/namespaces/"+gw.Namespace+"/routes" {
			t.Error("unexpected Route creation request")
			w.WriteHeader(500)
			return
		}
		var route object
		if json.NewDecoder(r.Body).Decode(&route) != nil {
			t.Error("invalid Route request")
			w.WriteHeader(400)
			return
		}
		encoded, _ := json.Marshal(route["spec"])
		var spec struct {
			Host, WildcardPolicy string
			To                   struct {
				Kind, Name string
				Weight     int
			}
			Port struct{ TargetPort string }
			TLS  struct{ Termination, InsecureEdgeTerminationPolicy, Certificate, Key string }
		}
		if json.Unmarshal(encoded, &spec) != nil || spec.Host != publicHostname(gw.Namespace, "example.test") || spec.WildcardPolicy != "None" || spec.To.Kind != "Service" || spec.To.Name != Name || spec.To.Weight != 100 || spec.Port.TargetPort != "grpc" || spec.TLS.Termination != "passthrough" || spec.TLS.InsecureEdgeTerminationPolicy != "None" || spec.TLS.Certificate != "" || spec.TLS.Key != "" {
			t.Error("Route does not use the assigned passthrough backend")
		}
		meta := route["metadata"].(map[string]any)
		meta["namespace"], meta["uid"], meta["resourceVersion"] = gw.Namespace, "route-one", "1"
		route["status"] = object{"ingress": []any{}}
		created = true
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(route)
	})
	k.options.PublicDomain, k.options.PublicRouter = "example.test", "selected"
	err := k.ensurePublicRoute(context.Background(), gw, nil, func(context.Context, string, []byte) error { t.Error("unadmitted Route reached the probe"); return nil })
	if !created || !errors.Is(err, ErrPending) {
		t.Fatal("new Route did not wait for admission", err)
	}
}

type publicProbeServer struct {
	protocol.UnimplementedOpenShellServer
	scenario         string
	health, identity atomic.Int32
	metadata         chan metadata.MD
}

func (s *publicProbeServer) capture(ctx context.Context) {
	md, _ := metadata.FromIncomingContext(ctx)
	s.metadata <- md.Copy()
}
func (s *publicProbeServer) Health(ctx context.Context, _ *protocol.HealthRequest) (*protocol.HealthResponse, error) {
	s.health.Add(1)
	s.capture(ctx)
	response := &protocol.HealthResponse{Status: protocol.ServiceStatus_SERVICE_STATUS_HEALTHY, Version: "test"}
	if s.scenario == "unhealthy" {
		response.Status = protocol.ServiceStatus_SERVICE_STATUS_UNHEALTHY
	}
	if s.scenario == "missing version" {
		response.Version = ""
	}
	if s.scenario == "long version" {
		response.Version = strings.Repeat("x", 129)
	}
	return response, nil
}
func (s *publicProbeServer) GetCurrentUser(ctx context.Context, _ *protocol.GetCurrentUserRequest) (*protocol.GetCurrentUserResponse, error) {
	s.identity.Add(1)
	s.capture(ctx)
	if s.scenario == "authentication disabled" {
		return &protocol.GetCurrentUserResponse{}, nil
	}
	return nil, status.Error(codes.Unauthenticated, "authentication required")
}
func TestPublicGatewayUsesVerifiedProbeAndGeneratedProtocol(t *testing.T) {
	for _, scenario := range []string{"ready", "unhealthy", "missing version", "long version", "authentication disabled", "different certificate", "untrusted"} {
		t.Run(scenario, func(t *testing.T) {
			certificate := httptest.NewTLSServer(http.NotFoundHandler())
			defer certificate.Close()
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			service := &publicProbeServer{scenario: scenario, metadata: make(chan metadata.MD, 2)}
			server := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS13, Certificates: certificate.TLS.Certificates})))
			protocol.RegisterOpenShellServer(server, service)
			done := make(chan struct{})
			go func() { defer close(done); _ = server.Serve(listener) }()
			defer func() { server.Stop(); <-done }()
			roots := x509.NewCertPool()
			if scenario != "untrusted" {
				roots.AddCert(certificate.Certificate())
			}
			expected := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Certificate().Raw})
			if scenario == "different certificate" {
				_, expected, _ = publicTestCertificate(t, "other.example.test", time.Now().Add(time.Hour), x509.ExtKeyUsageServerAuth)
			}
			k := &Kubernetes{publicRoots: roots}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "private-api-token", "cookie", "private-cookie"))
			err = k.probePublicGateway(ctx, listener.Addr().String(), expected)
			if scenario == "ready" {
				if err != nil || service.health.Load() != 1 || service.identity.Load() != 1 {
					t.Fatal("verified Gateway was not accepted", err)
				}
			} else if err == nil {
				t.Fatal("unsafe Gateway was accepted")
			}
			if scenario == "unhealthy" || scenario == "missing version" || scenario == "long version" {
				if service.identity.Load() != 0 {
					t.Fatal("identity call ran after invalid health")
				}
			}
			if scenario == "different certificate" || scenario == "untrusted" {
				if service.health.Load() != 0 {
					t.Fatal("RPC reached an unverified peer")
				}
			}
			for len(service.metadata) > 0 {
				md := <-service.metadata
				if len(md.Get("authorization")) != 0 || len(md.Get("cookie")) != 0 {
					t.Fatal("public Gateway received API credentials")
				}
			}
		})
	}
}
