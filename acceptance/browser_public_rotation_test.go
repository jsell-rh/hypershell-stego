package acceptance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	protocol "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func (w *browserGatewayWorkload) readPublicRotationObject(path, id, namespace string) kube.Object {
	w.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	object, code, err := w.kubernetes.Request(ctx, http.MethodGet, path, nil)
	if err != nil || code != http.StatusOK || !publicGatewayOwner(id).Matches(object) || kube.String(object, "metadata", "namespace") != namespace || kube.String(object, "metadata", "uid") == "" || kube.String(object, "metadata", "resourceVersion") == "" || kube.String(object, "metadata", "deletionTimestamp") != "" {
		w.t.Fatal("rotation resource is missing, changed, or not owned")
	}
	return object
}

func (w *browserGatewayWorkload) publicCertificate(secret kube.Object, gateway httpapi.Gateway) *x509.Certificate {
	w.t.Helper()
	certificate, err := kube.VerifyServerTLSSecret(secret, publicGatewayOwner(gateway.ID), kube.ServerTLSSecretTarget{Namespace: gateway.Namespace, Name: publicCertificateName, DNSName: "gw-" + gateway.Namespace + "." + w.public.Domain, Roots: w.public.roots})
	if err != nil {
		w.t.Fatal("public certificate failed verification")
	}
	block, _ := pem.Decode(certificate)
	if block == nil {
		w.t.Fatal("public certificate is absent")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		w.t.Fatal("public certificate is invalid")
	}
	return leaf
}

func (w *browserGatewayWorkload) probePublicCertificate(ctx context.Context, gateway httpapi.Gateway, leaf *x509.Certificate) error {
	probe, err := rpc.NewTLSProbe(rpc.TLSProbeOptions{Address: w.publicRPCAddress(gateway), Roots: w.public.roots, PeerCertificateSHA256: sha256.Sum256(leaf.Raw)})
	if err != nil {
		return err
	}
	defer probe.Close()
	var health protocol.HealthResponse
	if err := probe.Invoke(ctx, "/openshell.v1.OpenShell/Health", &protocol.HealthRequest{}, &health); err != nil {
		return err
	}
	if health.GetStatus() != protocol.ServiceStatus_SERVICE_STATUS_HEALTHY || health.GetVersion() == "" || len(health.GetVersion()) > 128 {
		return status.Error(codes.FailedPrecondition, "public Gateway health differs")
	}
	var identity protocol.GetCurrentUserResponse
	if err := probe.Invoke(ctx, "/openshell.v1.OpenShell/GetCurrentUser", &protocol.GetCurrentUserRequest{}, &identity); status.Code(err) != codes.Unauthenticated {
		return status.Error(codes.FailedPrecondition, "public Gateway identity check differs")
	}
	return nil
}

func (w *browserGatewayWorkload) readPublicProvider(gateway httpapi.Gateway) *protocol.ProviderResponse {
	w.t.Helper()
	directory := w.t.TempDir()
	ca := filepath.Join(directory, "ca.pem")
	token := filepath.Join(directory, "token")
	if err := os.WriteFile(ca, []byte(w.public.CA), 0600); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(token, []byte(w.identity.browserLogin(w.t, w.audience(gateway.ID), "console-alice")), 0600); err != nil {
		w.t.Fatal(err)
	}
	client, err := rpc.New(rpc.Options{Address: w.publicRPCAddress(gateway), CAFile: ca, TokenFile: token})
	if err != nil {
		w.t.Fatal("public owner client setup failed")
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var response protocol.ProviderResponse
	if err := client.Invoke(ctx, "/openshell.v1.OpenShell/GetProvider", &protocol.GetProviderRequest{Name: "browser-provider"}, &response); err != nil {
		w.t.Fatal("fresh public owner read failed", status.Code(err))
	}
	return &response
}

func (w *browserGatewayWorkload) checkPublicCertificateRotation(id string) {
	w.t.Helper()
	response := w.owner.api(w.t, "GET", "/gateways/"+id, nil)
	var gateway httpapi.Gateway
	if response.StatusCode != 200 || json.Unmarshal(response.Body, &gateway) != nil {
		w.t.Fatal("public rotation Gateway read failed")
	}
	w.requirePublicEndpoint(gateway)
	namespace := gateway.Namespace
	host := "gw-" + namespace + "." + w.public.Domain
	certificatePath := "/apis/cert-manager.io/v1/namespaces/" + namespace + "/certificates/" + publicCertificateName
	secretPath := "/api/v1/namespaces/" + namespace + "/secrets/"
	deploymentPath := "/apis/apps/v1/namespaces/" + namespace + "/deployments/openshell-gateway"
	read := func(path string) kube.Object { return w.readPublicRotationObject(path, id, namespace) }
	initialCertificate := read(certificatePath)
	initialSecret := read(secretPath + publicCertificateName)
	oldLeaf := w.publicCertificate(initialSecret, gateway)
	initialInternal := read(secretPath + "openshell-server-tls")
	initialDeployment := read(deploymentPath)
	beforeSQL := w.checkSQLIsolation()
	beforeSQLObjects := w.gatewaySQLObjectIDs()
	beforeProvider := w.readPublicProvider(gateway)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err := w.probePublicCertificate(ctx, gateway, oldLeaf)
	cancel()
	if err != nil {
		w.t.Fatal("initial public certificate was not served", status.Code(err))
	}
	// Re-read after the initial RPC checks, then make one conditional status write.
	initialCertificate = read(certificatePath)
	request, err := publicRenewalRequest(initialCertificate, id, namespace, host, w.public.Issuer, time.Now())
	if err != nil {
		w.t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	updated, code, err := w.kubernetes.Request(ctx, http.MethodPut, certificatePath+"/status", request)
	cancel()
	if err != nil || code != 200 || kube.String(updated, "metadata", "uid") != kube.String(initialCertificate, "metadata", "uid") {
		w.t.Fatal("conditional public renewal failed", code)
	}
	started := time.Now()
	deadline := started.Add(180 * time.Second)
	var renewedCertificate, renewedSecret, renewedDeployment kube.Object
	var newLeaf *x509.Certificate
	for {
		renewedCertificate = read(certificatePath)
		renewedSecret = read(secretPath + publicCertificateName)
		renewedDeployment = read(deploymentPath)
		if kube.String(renewedCertificate, "metadata", "uid") != kube.String(initialCertificate, "metadata", "uid") || kube.String(renewedSecret, "metadata", "uid") != kube.String(initialSecret, "metadata", "uid") || kube.String(renewedDeployment, "metadata", "uid") != kube.String(initialDeployment, "metadata", "uid") {
			w.t.Fatal("renewal replaced a stable resource identity")
		}
		newLeaf = w.publicCertificate(renewedSecret, gateway)
		available, availabilityErr := kube.DeploymentAvailable(renewedDeployment, publicGatewayOwner(id), 1)
		if availabilityErr != nil {
			w.t.Fatal("rotation Deployment observation is invalid")
		}
		if certificateNumber(kube.Nested(renewedCertificate, "status", "revision")) > certificateNumber(kube.Nested(initialCertificate, "status", "revision")) &&
			!bytes.Equal(oldLeaf.Raw, newLeaf.Raw) && !bytes.Equal(oldLeaf.RawSubjectPublicKeyInfo, newLeaf.RawSubjectPublicKeyInfo) &&
			certificateNumber(kube.Nested(renewedDeployment, "metadata", "generation")) > certificateNumber(kube.Nested(initialDeployment, "metadata", "generation")) && available {
			ctx, cancel = context.WithTimeout(context.Background(), 3*time.Second)
			err = w.probePublicCertificate(ctx, gateway, newLeaf)
			cancel()
			if err == nil {
				break
			}
		}
		if time.Now().After(deadline) {
			w.t.Fatal("new public certificate was not served after renewal")
		}
		time.Sleep(time.Second)
	}
	w.check(id)
	// A new connection with the old pin must fail after the new pin succeeds.
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	err = w.probePublicCertificate(ctx, gateway, oldLeaf)
	cancel()
	if status.Code(err) != codes.Unavailable {
		w.t.Fatal("old public certificate pin did not fail its connection", status.Code(err))
	}
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	err = w.probePublicCertificate(ctx, gateway, newLeaf)
	cancel()
	if err != nil {
		w.t.Fatal("new public certificate did not remain available", status.Code(err))
	}
	if !reflect.DeepEqual(beforeSQL, w.checkSQLIsolation()) || !reflect.DeepEqual(beforeSQLObjects, w.gatewaySQLObjectIDs()) || !proto.Equal(beforeProvider, w.readPublicProvider(gateway)) {
		w.t.Fatal("public renewal changed database identities or provider data")
	}
	afterInternal := read(secretPath + "openshell-server-tls")
	if kube.String(initialInternal, "metadata", "uid") != kube.String(afterInternal, "metadata", "uid") || !reflect.DeepEqual(initialInternal["data"], afterInternal["data"]) {
		w.t.Fatal("public renewal changed internal TLS material")
	}
	hash := func(leaf *x509.Certificate) string {
		value := sha256.Sum256(leaf.Raw)
		return hex.EncodeToString(value[:])
	}
	record := map[string]any{"gateway_id": id, "address": *gateway.RouteAddress, "certificate_uid": kube.String(initialCertificate, "metadata", "uid"), "old_certificate_sha256": hash(oldLeaf), "new_certificate_sha256": hash(newLeaf), "private_key_rotated": true, "renewal_uses_observed_uid_and_revision": true, "new_tls_connection_verified": true, "old_pin_rejected": true, "unauthenticated_denied": true, "new_owner_connection_verified": true, "sql_credentials_preserved": true, "sql_object_ids_preserved": true, "provider_data_preserved": true, "internal_tls_unchanged": true, "seconds": time.Since(started).Seconds()}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		w.t.Fatal(err)
	}
	directory := os.Getenv("STEGO_BROWSER_ARTIFACT_DIR")
	if directory == "" {
		w.t.Fatal("public rotation evidence directory is missing")
	}
	if err = os.WriteFile(filepath.Join(directory, "gateway-public-certificate-rotation.json"), append(data, '\n'), 0600); err != nil {
		w.t.Fatal(err)
	}
}
