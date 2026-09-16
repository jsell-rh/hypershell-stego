package gatewayworkload

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	keycloak "github.com/jsell-rh/hypershell-stego/internal/serviceaccountkeycloak"
	provisioner "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/provisioner/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
	telemetry "github.com/jsell-rh/hypershell-stego/out/tracing"
)

type ConsoleOptions struct {
	Domain, Image       string
	ImagePullConfigFile string
	Credentials         provisioner.GatewayConsoleCredentialServiceClient
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
	if o.Console.ImagePullConfigFile != "" && !filepath.IsAbs(o.Console.ImagePullConfigFile) {
		return errors.New("console image pull configuration requires an absolute private file path")
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
	origin, err := k.consoleOrigin(gw, version)
	if err != nil {
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
	telemetryEnvironment, telemetryFiles, err := telemetry.ExportEnvironment(consoleName, "/var/run/stego")
	if err != nil {
		return nil, err
	}
	defer func() {
		for _, value := range telemetryFiles {
			clear(value)
		}
	}()
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
	for name, value := range telemetryEnvironment {
		runtime[name] = encode(value)
	}
	files := object{"tls.crt": kube.String(server, "data", "tls.crt"), "tls.key": kube.String(server, "data", "tls.key"), "issuer-ca.pem": encode(k.trust), "client-secret": encode(credential.ClientSecret)}
	for name, value := range store {
		files[name] = value
	}
	for name, value := range telemetryFiles {
		files[name] = base64.StdEncoding.EncodeToString(value)
	}
	application := object{}
	for name, value := range map[string]string{
		"AUTH_DISABLED": "false", "AUTH_TOKEN_HEADER": "x-forwarded-access-token", "AUTH_USER_HEADER": "x-auth-request-user",
		"ADMIN_ROLE": keycloak.RoleAdmin, "LOGOUT_URL": "/auth/logout",
		"OPENSHELL_GATEWAY_URL": "https://" + gatewayHost + ":8080",
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

// Console placement and origin come from the current Gateway and operator
// configuration. Reject a stale origin before creating its database or TLS.
func (k *Kubernetes) consoleOrigin(gw *pb.Gateway, version int64) (string, error) {
	invalid := errors.New("console does not match the observed Gateway")
	if version < 1 || !k.Handles(gw) || k.options.Console == nil {
		return "", invalid
	}
	if err := checkConsoleOptions(k.options); err != nil {
		return "", err
	}
	ns, err := Namespace(gw.GetMetadata().GetId())
	if err != nil || ns != gw.GetNamespace() {
		return "", invalid
	}
	origin, err := keycloak.GatewayConsoleOrigin(gw.GetMetadata().GetId(), k.options.Console.Domain)
	if err != nil || (gw.GetConsoleAddress() != "" && origin != gw.GetConsoleAddress()) {
		return "", invalid
	}
	return origin, nil
}

// The operator's domain defines the desired address before public observation.
// The controller publishes it only after Ensure verifies the complete endpoint.
func (k *Kubernetes) DesiredConsoleEndpoint(gw *pb.Gateway) *string {
	if k.options.Console == nil || !k.Handles(gw) {
		return nil
	}
	origin, err := keycloak.GatewayConsoleOrigin(gw.GetMetadata().GetId(), k.options.Console.Domain)
	if err != nil {
		return nil
	}
	return &origin
}

func (k *Kubernetes) ensureConsoleDependencies(ctx context.Context, gw *pb.Gateway, version int64, store object) (string, error) {
	origin, err := k.consoleOrigin(gw, version)
	if err != nil {
		return "", err
	}
	if ctx == nil || k.publicRoots == nil {
		return "", errors.New("console TLS trust is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	id, ns := gw.GetMetadata().GetId(), gw.GetNamespace()
	labels := object{}
	for key, value := range consoleOwner(id) {
		labels[key] = value
	}
	cert := object{"apiVersion": "cert-manager.io/v1", "kind": "Certificate", "metadata": object{"name": consoleName + "-tls", "namespace": ns, "labels": labels}, "spec": object{
		"secretName": consoleName + "-tls", "issuerRef": object{"name": k.options.PublicIssuer, "kind": "ClusterIssuer"},
		"dnsNames": []string{strings.TrimPrefix(origin, "https://")}, "privateKey": object{"algorithm": "ECDSA", "size": 256, "rotationPolicy": "Always"},
		"usages": []string{"server auth"}, "duration": "2160h", "renewBefore": "360h", "secretTemplate": object{"labels": labels},
	}}
	if _, err := k.client.Ensure(ctx, "/apis/cert-manager.io/v1/namespaces/"+ns+"/certificates", cert, consoleOwner(id)); err != nil {
		return "", err
	}
	certificates := make([]object, 2)
	for i, name := range []string{consoleName + "-tls", "openshell-client-tls"} {
		value, code, err := k.client.Request(ctx, http.MethodGet, "/api/v1/namespaces/"+ns+"/secrets/"+name, nil)
		if err != nil {
			return "", err
		}
		if code == http.StatusNotFound {
			return "", ErrPending
		}
		if code != http.StatusOK {
			return "", errors.New("console certificate read failed")
		}
		certificates[i] = value
	}
	desired, err := k.consoleDependencyObjects(ctx, gw, version, store, certificates[0], certificates[1])
	if err != nil {
		return "", err
	}
	return k.client.EnsureOpaqueSecretSet(ctx, ns, consoleSecretNames[:], consoleOwner(id), desired)
}
