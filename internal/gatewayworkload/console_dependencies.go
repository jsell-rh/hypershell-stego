package gatewayworkload

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"

	keycloak "github.com/jsell-rh/hypershell-stego/internal/serviceaccountkeycloak"
	provisioner "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/provisioner/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

type ConsoleOptions struct {
	Domain, Image string
	Credentials   provisioner.GatewayConsoleCredentialServiceClient
}

func checkConsoleOptions(o Options) error {
	if o.Console == nil {
		return nil
	}
	if _, err := keycloak.GatewayConsoleOrigin(o.ClusterID, o.Console.Domain); err != nil {
		return err
	}
	if o.Console.Credentials == nil || !digestImage.MatchString(o.Console.Image) || o.PublicDomain == "" || o.PublicIssuer == "" || o.PublicRouter == "" {
		return errors.New("console requires a pinned image, credential client, and public TLS configuration")
	}
	return nil
}

// These four Secrets follow the generated container mounts. The dashboard gets
// only the Gateway client certificate. The browser gets OAuth and session data.
func (k *Kubernetes) consoleDependencyObjects(ctx context.Context, gw *pb.Gateway, version int64, store, server, client object) ([]object, error) {
	invalid := errors.New("console dependencies do not match the current Gateway")
	if ctx == nil || version < 1 || !k.Handles(gw) || k.options.Console == nil || k.options.Console.Credentials == nil {
		return nil, invalid
	}
	id, ns := gw.GetMetadata().GetId(), gw.GetNamespace()
	expectedNS, err := Namespace(id)
	if err != nil || ns != expectedNS {
		return nil, invalid
	}
	origin, err := keycloak.GatewayConsoleOrigin(id, k.options.Console.Domain)
	if err != nil || gw.GetConsoleAddress() != origin {
		return nil, invalid
	}
	host := strings.TrimPrefix(origin, "https://")
	if _, err = kube.VerifyServerTLSSecret(server, consoleOwner(id), kube.ServerTLSSecretTarget{Namespace: ns, Name: consoleName + "-tls", DNSName: host, Roots: k.publicRoots}); err != nil {
		return nil, err
	}
	gatewayHost := Name + "." + ns + ".svc.cluster.local"
	if _, err = kube.VerifyClientTLSSecret(client, owner(id), kube.ClientTLSSecretTarget{Namespace: ns, Name: "openshell-client-tls", DNSName: gatewayHost, Roots: k.internalRoots}); err != nil {
		return nil, err
	}
	if len(store) != 3 || len(k.internalTrust) == 0 {
		return nil, invalid
	}
	for _, name := range []string{"database-url", "database-ca.pem", "session-key"} {
		if _, ok := store[name].(string); !ok {
			return nil, invalid
		}
	}
	credential, err := k.options.Console.Credentials.GetCredentials(ctx, &provisioner.GatewayConsoleCredentialRequest{GatewayId: id, ClusterId: k.options.ClusterID, ResourceVersion: version})
	if err != nil {
		return nil, errors.New("console credential is not ready")
	}
	if credential == nil || len(credential.ProtoReflect().GetUnknown()) != 0 || credential.GetClientId() != "hs-console-"+id || len(credential.GetClientSecret()) < 16 || len(credential.GetClientSecret()) > 4096 {
		return nil, invalid
	}
	for _, c := range credential.GetClientSecret() {
		if c < 33 || c > 126 {
			return nil, invalid
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	encode := func(value string) string { return base64.StdEncoding.EncodeToString([]byte(value)) }
	runtime := object{}
	for name, value := range map[string]string{
		"DATABASE_URL_FILE":                "/var/run/stego/database-url",
		"STEGO_BROWSER_ORIGIN":             origin,
		"STEGO_BROWSER_ISSUER":             k.options.Issuer,
		"STEGO_BROWSER_ISSUER_CA_FILE":     "/var/run/stego/issuer-ca.pem",
		"STEGO_BROWSER_CLIENT_ID":          credential.ClientId,
		"STEGO_BROWSER_CLIENT_SECRET_FILE": "/var/run/stego/client-secret",
		"STEGO_BROWSER_SESSION_KEY_FILE":   "/var/run/stego/session-key",
	} {
		runtime[name] = encode(value)
	}
	files := object{"tls.crt": kube.String(server, "data", "tls.crt"), "tls.key": kube.String(server, "data", "tls.key"), "issuer-ca.pem": encode(k.trust), "client-secret": encode(credential.ClientSecret)}
	for name, value := range store {
		files[name] = value
	}
	application := object{}
	for name, value := range map[string]string{
		"AUTH_DISABLED": "false", "AUTH_TOKEN_HEADER": "x-forwarded-access-token", "AUTH_USER_HEADER": "x-auth-request-user",
		"ADMIN_ROLE": keycloak.RoleAdmin, "LOGOUT_URL": "/auth/logout",
		"OPENSHELL_GATEWAY_URL": gatewayHost + ":8080",
		"GATEWAY_CA_CERT":       "/var/run/stego-application/gateway-ca.pem",
		"GATEWAY_CLIENT_CERT":   "/var/run/stego-application/client.crt",
		"GATEWAY_CLIENT_KEY":    "/var/run/stego-application/client.key",
	} {
		application[name] = encode(value)
	}
	applicationFiles := object{"gateway-ca.pem": base64.StdEncoding.EncodeToString(k.internalTrust), "client.crt": kube.String(client, "data", "tls.crt"), "client.key": kube.String(client, "data", "tls.key")}
	values := []object{runtime, files, application, applicationFiles}
	result := make([]object, len(values))
	labels := object{}
	for name, value := range consoleOwner(id) {
		labels[name] = value
	}
	for i, value := range values {
		result[i] = object{"apiVersion": "v1", "kind": "Secret", "metadata": object{"name": consoleSecretNames[i], "namespace": ns, "labels": labels}, "type": "Opaque", "data": value}
	}
	return result, nil
}
