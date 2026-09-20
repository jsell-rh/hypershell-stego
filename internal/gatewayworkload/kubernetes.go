package gatewayworkload

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/jsell-rh/hypershell-stego/out/deploy/allocation"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

type object = kube.Object
type Options struct {
	Console                                                *ConsoleOptions
	InternalCAFile                                         string
	PublicDomain, PublicIssuer, PublicCAFile, PublicRouter string
	SQLBindings                                            SQLBindings
	ConsoleSQLBindings                                     ConsoleSQLBindings
	SandboxRuntimeClass                                    string
	ControlNamespace                                       string
	DatabaseConfigFile                                     string
	ClusterID                                              string
	ServerURL, CAFile, TokenFile, ClusterIssuer            string
	Issuer, TrustBundleFile, SandboxImage, SupervisorImage string
}
type Kubernetes struct {
	client        *kube.Client
	options       Options
	trust         string
	publicRoots   *x509.CertPool
	internalRoots *x509.CertPool
	internalTrust []byte
	allocation    *allocation.Allocator
}

func NewKubernetes(o Options) (*Kubernetes, error) {
	if err := checkConsoleOptions(o); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(o.DatabaseConfigFile) || o.ControlNamespace == "" || o.SQLBindings == nil || o.ConsoleSQLBindings == nil {
		return nil, errors.New("Gateway controller requires a database file, control namespace, and SQL state client")
	}
	if _, err := Namespace(o.ClusterID); err != nil {
		return nil, errors.New("Gateway controller requires a managed cluster ID")
	}
	if (o.SandboxRuntimeClass != "" && !dnsLabel.MatchString(o.SandboxRuntimeClass)) || !dnsLabel.MatchString(o.ClusterIssuer) || !validIssuer(o.Issuer) || !digestImage.MatchString(o.SandboxImage) || !digestImage.MatchString(o.SupervisorImage) {
		return nil, errors.New("Gateway controller configuration is invalid")
	}
	f, err := os.Open(o.TrustBundleFile)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	trust, err := io.ReadAll(io.LimitReader(f, (512<<10)+1))
	if err != nil || len(trust) > 512<<10 {
		return nil, errors.New("Gateway trust bundle is invalid")
	}
	trust, err = certificateBundle(trust)
	if err != nil {
		return nil, err
	}
	publicRoots, err := publicTrust(o)
	if err != nil {
		return nil, err
	}
	internalTrust, internalRoots, err := gatewayTrustMaterial(o.InternalCAFile)
	if err != nil {
		return nil, errors.New("Gateway internal TLS requires an explicit CA file")
	}
	c, err := kube.New(kube.Options{ServerURL: o.ServerURL, CAFile: o.CAFile, TokenFile: o.TokenFile})
	if err != nil {
		return nil, err
	}
	var allocator *allocation.Allocator
	if o.ControlNamespace != "" {
		allocator, err = allocation.New(c, o.ControlNamespace)
		if err != nil {
			c.Close()
			return nil, err
		}
	}
	return &Kubernetes{client: c, options: o, trust: string(trust), allocation: allocator, publicRoots: publicRoots, internalRoots: internalRoots, internalTrust: internalTrust}, nil
}

// Only certificates can enter the public ConfigMap. Never copy a private key
// or other file contents that happen to be beside a certificate in the input.
func certificateBundle(input []byte) ([]byte, error) {
	var output []byte
	for len(bytes.TrimSpace(input)) != 0 {
		block, rest := pem.Decode(input)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, errors.New("Gateway trust bundle must contain only certificates")
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			return nil, errors.New("Gateway trust bundle contains an invalid certificate")
		}
		output = append(output, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: block.Bytes})...)
		input = rest
	}
	if len(output) == 0 {
		return nil, errors.New("Gateway trust bundle is empty")
	}
	return output, nil
}
func (k *Kubernetes) Close()                { k.client.Close() }
func (k *Kubernetes) CleanupTarget() string { return k.options.ClusterID }

func (k *Kubernetes) Handles(gw *pb.Gateway) bool { return gw.GetClusterId() == k.options.ClusterID }
func owner(id string) kube.Owner                  { return kube.Owner{ownerLabel: id, managerLabel: manager} }
func definition(api, kind, name, id string) object {
	return object{"apiVersion": api, "kind": kind, "metadata": object{"name": name, "labels": object{ownerLabel: id, managerLabel: manager}}}
}
func data(secret object, key string) ([]byte, error) {
	raw := kube.String(secret, "data", key)
	value, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(value) == 0 {
		return nil, errors.New("Gateway dependency Secret is invalid")
	}
	return value, nil
}
func (k *Kubernetes) ensure(ctx context.Context, path string, desired object, id string) (object, error) {
	return k.client.Ensure(ctx, path, desired, owner(id))
}

func (k *Kubernetes) Ensure(ctx context.Context, gw *pb.Gateway, release *pb.GatewayRelease, version int64) error {
	if version < 1 {
		return errors.New("Gateway workload requires its observed resource version")
	}
	if !k.Handles(gw) {
		return errors.New("Gateway belongs to a different managed cluster")
	}
	if k.allocation == nil {
		return errors.New("Gateway workload requires namespace allocation")
	}
	oidc, err := validate(gw, release, k.options.Issuer)
	if err != nil {
		return err
	}
	if gw.GetSupervisorImage() != "" && gw.GetSupervisorImage() != k.options.SupervisorImage {
		return errors.New("Gateway supervisor image differs from controller configuration")
	}
	id, ns := gw.Metadata.Id, gw.Namespace
	serviceAccount, err := k.allocation.RequireServiceAccount(ctx, "gateway", ns, id, "gateway")
	if err != nil {
		if errors.Is(err, allocation.ErrPending) {
			return ErrPending
		}
		return err
	}
	if err := k.allocation.RequireNamespace(ctx, "gateway", ns, id); err != nil {
		if errors.Is(err, allocation.ErrPending) {
			return ErrPending
		}
		return err
	}
	state, databaseConfig, err := k.localState(ctx, gw)
	if err != nil {
		return err
	}
	keys := object{}
	for _, key := range []string{"signing.pem", "public.pem", "kid", "key-encryption-key"} {
		keys[key] = kube.String(state, "data", key)
	}
	dbData, err := k.databaseCredentials(ctx, gw, state, databaseConfig)
	if err != nil {
		return err
	}
	core := "/api/v1/namespaces/" + ns
	for name, values := range map[string]object{keysName: keys, "openshell-gateway-db-credentials": dbData} {
		secret := definition("v1", "Secret", name, id)
		secret["type"] = "Opaque"
		secret["data"] = values
		if name == keysName {
			secret["immutable"] = true
		}
		if _, err = k.ensure(ctx, core+"/secrets", secret, id); err != nil {
			return err
		}
	}
	host := Name + "." + ns + ".svc.cluster.local"
	for _, name := range []string{"openshell-server-tls", "openshell-client-tls"} {
		cert := definition("cert-manager.io/v1", "Certificate", name, id)
		usage := "server auth"
		if name == "openshell-client-tls" {
			usage = "client auth"
		}
		cert["spec"] = object{"secretName": name, "issuerRef": object{"name": k.options.ClusterIssuer, "kind": "ClusterIssuer"}, "dnsNames": []string{host}, "privateKey": object{"algorithm": "ECDSA", "size": 256, "rotationPolicy": "Always"}, "usages": []string{usage}, "duration": "2160h", "renewBefore": "360h", "secretTemplate": object{"labels": object{ownerLabel: id, managerLabel: manager}}}
		if _, err = k.ensure(ctx, "/apis/cert-manager.io/v1/namespaces/"+ns+"/certificates", cert, id); err != nil {
			return err
		}
	}
	server, code, err := k.client.Request(ctx, http.MethodGet, core+"/secrets/openshell-server-tls", nil)
	if err != nil {
		return err
	}
	if code == 404 {
		return ErrPending
	}
	_, err = k.verifyInternalTLS(server, id, ns, host)
	if err != nil {
		return err
	}
	publicCertificate, publicServer, err := k.ensurePublicTLS(ctx, gw)
	if err != nil {
		return err
	}
	sandboxNS := ns
	sandboxAccount := Name + "-sandbox"
	if k.options.SandboxRuntimeClass != "" {
		sandboxNS, _ = SandboxNamespace(id)
		if sandboxAccount, err = k.ensureSandbox(ctx, id, sandboxNS, core, k.internalRoots); err != nil {
			return err
		}
	}
	config := definition("v1", "ConfigMap", Name+"-config", id)
	config["data"] = object{"gateway.toml": configuration(ns, sandboxNS, sandboxAccount, k.options), "trust.pem": k.trust}
	if _, err = k.ensure(ctx, core+"/configmaps", config, id); err != nil {
		return err
	}
	rendered, err := resources(gw, serviceAccount, release, oidc, config, dbData, keys, server, publicServer)
	if err != nil {
		return err
	}
	for _, entry := range rendered {
		if _, err = k.ensure(ctx, entry.path, entry.object, id); err != nil {
			return err
		}
	}
	if err := k.deploymentAvailable(ctx, id, ns); err != nil {
		return err
	}
	if err := k.ensurePublicRoute(ctx, gw, publicCertificate, k.probePublicGateway); err != nil {
		return err
	}
	if k.options.Console != nil {
		return k.EnsureConsole(ctx, gw, version, 1000)
	}
	return nil
}

func (k *Kubernetes) deploymentAvailable(ctx context.Context, id, namespace string) error {
	current, code, err := k.client.Request(ctx, http.MethodGet, "/apis/apps/v1/namespaces/"+namespace+"/deployments/"+Name, nil)
	if err != nil {
		return err
	}
	if code == http.StatusNotFound {
		return ErrPending
	}
	available, err := kube.DeploymentAvailable(current, owner(id), 1)
	if err != nil {
		return err
	}
	if !available {
		return ErrPending
	}
	return nil
}

func sha256sum(value []byte) []byte { sum := sha256.Sum256(value); return sum[:] }

func (k *Kubernetes) Delete(ctx context.Context, gw *pb.Gateway) error {
	if !k.Handles(gw) {
		return errors.New("Gateway cleanup belongs to a different cluster")
	}
	id := gw.GetMetadata().GetId()
	ns, err := Namespace(id)
	if err != nil || ns != gw.GetNamespace() {
		return errors.New("Gateway cleanup namespace is invalid")
	}
	if k.allocation == nil {
		return errors.New("Gateway cleanup requires namespace allocation")
	}
	gone, err := k.allocation.NamespaceGone(ctx, "gateway", ns, id)
	if err != nil {
		return err
	}
	if !gone {
		return ErrPending
	}
	sandboxName, err := SandboxNamespace(id)
	if err != nil {
		return err
	}
	gone, err = k.allocation.NamespaceGone(ctx, "sandbox", sandboxName, id)
	if err != nil {
		return err
	}
	if !gone {
		return ErrPending
	}
	return nil
}

// DeleteDatabase waits for workload removal and retains the destination until
// STEGO has durably removed the SQL database and login.
func (k *Kubernetes) DeleteDatabase(ctx context.Context, gw *pb.Gateway) error {
	if err := k.Delete(ctx, gw); err != nil {
		return err
	}
	var failures []error
	for _, remove := range []func(context.Context, *pb.Gateway) error{k.deleteConsoleDatabase, k.deleteGatewayDatabase} {
		operation, cancel := context.WithTimeout(ctx, 8*time.Second)
		failures = append(failures, remove(operation, gw))
		cancel()
	}
	return errors.Join(failures...)
}
func (k *Kubernetes) deleteGatewayDatabase(ctx context.Context, gw *pb.Gateway) error {
	if k.options.SQLBindings == nil {
		return errors.New("SQL state registration is required")
	}
	binding, err := k.options.SQLBindings.Close(ctx, gw.GetMetadata().GetId(), k.options.ClusterID)
	if err != nil {
		return err
	}
	if !binding.Present || !binding.Closed {
		return errors.New("SQL state closure was not confirmed")
	}
	if binding.Digest == "" {
		return nil
	}
	state, config, err := k.readLocalState(ctx, gw)
	if err != nil {
		return err
	}
	return k.deleteDatabase(ctx, gw, state, config)
}

// Retained API records supply recovery IDs. The resource worker has no
// permission to list namespaces or credentials in other installations.
func (k *Kubernetes) GatewayIDs(context.Context) ([]string, error) { return nil, nil }
