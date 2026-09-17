package acceptance

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"time"

	protocol "github.com/jsell-rh/hypershell-stego/contracts/gateway"
	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	"github.com/jsell-rh/hypershell-stego/out/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

func (w *browserGatewayWorkload) checkRPC(id string) {
	w.t.Helper()
	response := w.owner.api(w.t, "GET", "/gateways/"+id, nil)
	var gateway httpapi.Gateway
	if response.StatusCode != 200 || json.Unmarshal(response.Body, &gateway) != nil {
		w.t.Fatal("Gateway read failed")
	}
	address := "openshell-gateway." + gateway.Namespace + ".svc.cluster.local:8080"
	roots := w.internalRoots
	if w.public != nil {
		address = w.publicRPCAddress(gateway)
		roots = w.public.roots
	}
	if roots == nil {
		w.t.Fatal("operator-supplied Gateway trust is missing")
	}
	newConnection := func() (*grpc.ClientConn, error) {
		return grpc.NewClient("passthrough:///"+address, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots})), grpc.WithDisableRetry(), grpc.WithDisableServiceConfig(), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(64<<10), grpc.MaxCallSendMsgSize(64<<10)))
	}
	connection, err := newConnection()
	if err != nil {
		w.t.Fatal("Gateway RPC connection failed")
	}
	w.t.Cleanup(func() { connection.Close() })
	service, err := protocol.Load(context.Background())
	if err != nil {
		w.t.Fatal(err)
	}
	connectionCall := func(connection *grpc.ClientConn) gatewayCall {
		return func(method, bearer, input string) (*dynamicpb.Message, error) {
			w.t.Helper()
			descriptor := service.Methods().ByName(protoreflect.Name(method))
			if descriptor == nil {
				w.t.Fatal("Gateway method absent")
			}
			request := dynamicpb.NewMessage(descriptor.Input())
			if err := protojson.Unmarshal([]byte(input), request); err != nil {
				w.t.Fatal(err)
			}
			response := dynamicpb.NewMessage(descriptor.Output())
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if bearer != "" {
				ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+bearer))
			}
			options := []grpc.CallOption{}
			if method == "GetProvider" {
				options = append(options, grpc.WaitForReady(true))
			}
			err := connection.Invoke(ctx, "/openshell.v1.OpenShell/"+method, request, response, options...)
			return response, err
		}
	}
	w.call = connectionCall(connection)
	owner := w.identity.browserLogin(w.t, w.audience(id), "console-alice")
	other := w.identity.browserLogin(w.t, w.audience(id), "console-bob")
	apiToken := w.identity.browserLogin(w.t, "hypershell", "console-alice")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	keys, err := w.identity.http.Do(ctx, "GET", "/realms/workflow/protocol/openid-connect/certs", nil, nil)
	cancel()
	if err != nil || keys.StatusCode != 200 {
		w.t.Fatal("provider keys unavailable for the negative audience check")
	}
	config := auth.Config{Issuer: w.identity.options.ServerURL + "/realms/workflow", Audience: "hypershell", RolesClaim: "resource_access.hypershell.roles"}
	if _, err := auth.VerifyWithJWKS(config, apiToken, keys.Body); err != nil {
		w.t.Fatal("negative audience fixture is not a valid API token")
	}
	config.Audience = w.audience(id)
	if _, err := auth.VerifyWithJWKS(config, apiToken, keys.Body); err == nil {
		w.t.Fatal("negative audience fixture contains the Gateway audience")
	}
	for _, test := range []struct{ name, bearer string }{{"missing", ""}, {"malformed", "forged"}, {"wrong-audience", apiToken}} {
		if _, err := w.call("GetCurrentUser", test.bearer, `{}`); status.Code(err) != codes.Unauthenticated {
			w.t.Fatal("Gateway accepted invalid identity", test.name, status.Code(err))
		}
	}
	if _, err := w.call("GetProvider", other, `{"name":"browser-provider"}`); status.Code(err) != codes.PermissionDenied {
		w.t.Fatal("Gateway accepted ungranted caller", status.Code(err))
	}
	if _, err := w.call("CreateProvider", owner, `{"provider":{"metadata":{"name":"browser-provider"},"type":"openai","credentials":{"OPENAI_API_KEY":"acceptance-only-upstream-secret"},"config":{"acceptance":"persisted"}}}`); err != nil {
		w.t.Fatal("Gateway provider creation failed", status.Code(err))
	}
	before, err := w.call("GetProvider", owner, `{"name":"browser-provider"}`)
	if err != nil {
		w.t.Fatal("Gateway provider read failed", status.Code(err))
	}
	selector := "hypershell.redhat.io/gateway-id=" + id
	var old struct {
		Items []struct{ Metadata struct{ UID string } }
	}
	if json.Unmarshal(w.p.command(nil, "--namespace="+gateway.Namespace, "get", "pods", "-l", selector, "-o", "json"), &old) != nil || len(old.Items) != 1 {
		w.t.Fatal("Gateway Pod identity absent")
	}
	w.p.command(nil, "--namespace="+gateway.Namespace, "delete", "pods", "-l", selector, "--wait=true", "--timeout=60s")
	w.p.command(nil, "--namespace="+gateway.Namespace, "rollout", "status", "deployment/openshell-gateway", "--timeout=120s")
	var current struct {
		Items []struct{ Metadata struct{ UID string } }
	}
	if json.Unmarshal(w.p.command(nil, "--namespace="+gateway.Namespace, "get", "pods", "-l", selector, "-o", "json"), &current) != nil || len(current.Items) != 1 || current.Items[0].Metadata.UID == old.Items[0].Metadata.UID {
		w.t.Fatal("Gateway Pod was not replaced")
	}
	after, err := w.call("GetProvider", owner, `{"name":"browser-provider"}`)
	if err != nil {
		originalState := connection.GetState()
		w.recordReadinessFailure(id)
		// One read on a new connection can separate connection recovery from
		// persistent data failure. It cannot turn the original failure into a pass.
		probeConnection, probeSetupErr := newConnection()
		if probeSetupErr != nil {
			w.t.Log("Gateway recovery probe connection setup failed")
		} else {
			probe, probeErr := connectionCall(probeConnection)("GetProvider", owner, `{"name":"browser-provider"}`)
			w.t.Logf("Gateway recovery probe: original_state=%s fresh_state=%s fresh_status=%s saved_data_matches=%t", originalState, probeConnection.GetState(), status.Code(probeErr), probeErr == nil && proto.Equal(before, probe))
			_ = probeConnection.Close()
		}
		w.t.Fatal("Gateway provider read failed after replacement", status.Code(err))
	}
	if !proto.Equal(before, after) {
		w.t.Fatal("Gateway provider data changed after replacement")
	}
	w.recordPublicRPC(gateway)
	w.t.Log("Browser-created OpenShell Gateway passed verified RPC, denied calls, and provider data recovery after Pod replacement")
}

func (w *browserGatewayWorkload) checkCredential(token string) {
	w.t.Helper()
	if w.call == nil {
		w.t.Fatal("real Gateway RPC was not checked")
	}
	if _, err := w.call("GetProvider", token, `{"name":"browser-provider"}`); err != nil {
		w.t.Fatal("browser credential cannot read the real Gateway", status.Code(err))
	}
	w.t.Log("Browser-issued service credential used the actual OpenShell Gateway")
}
