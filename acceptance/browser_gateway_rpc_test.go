package acceptance

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"

	protocol "github.com/jsell-rh/hypershell-stego/contracts/gateway"
	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	secret, code, err := w.kubernetes.Request(ctx, http.MethodGet, "/api/v1/namespaces/"+gateway.Namespace+"/secrets/openshell-server-tls", nil)
	cancel()
	if err != nil || code != 200 {
		w.t.Fatal("Gateway CA unavailable")
	}
	encoded, ok := secret["data"].(map[string]any)
	if !ok {
		w.t.Fatal("Gateway CA document invalid")
	}
	text, ok := encoded["ca.crt"].(string)
	if !ok {
		w.t.Fatal("Gateway CA absent")
	}
	ca, err := base64.StdEncoding.DecodeString(text)
	roots := x509.NewCertPool()
	if err != nil || !roots.AppendCertsFromPEM(ca) {
		w.t.Fatal("Gateway CA invalid")
	}
	connection, err := grpc.NewClient("passthrough:///openshell-gateway."+gateway.Namespace+".svc.cluster.local:8080", grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots})), grpc.WithDisableRetry(), grpc.WithDisableServiceConfig(), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(64<<10), grpc.MaxCallSendMsgSize(64<<10)))
	if err != nil {
		w.t.Fatal("Gateway RPC connection failed")
	}
	w.t.Cleanup(func() { connection.Close() })
	service, err := protocol.Load(context.Background())
	if err != nil {
		w.t.Fatal(err)
	}
	w.call = func(method, bearer, input string) (*dynamicpb.Message, error) {
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
	owner := w.identity.browserLogin(w.t, w.audience(id), "console-alice")
	other := w.identity.browserLogin(w.t, w.audience(id), "console-bob")
	for _, value := range []string{"", "forged"} {
		if _, err := w.call("GetCurrentUser", value, `{}`); status.Code(err) != codes.Unauthenticated {
			w.t.Fatal("Gateway accepted invalid identity", status.Code(err))
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
	if err != nil || !proto.Equal(before, after) {
		w.t.Fatal("Gateway lost provider data after replacement", status.Code(err))
	}
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
