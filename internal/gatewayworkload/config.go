// Package gatewayworkload provisions the Hypershell Gateway workload.
package gatewayworkload

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	keycloak "github.com/jsell-rh/hypershell-stego/internal/serviceaccountkeycloak"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"github.com/segmentio/ksuid"
)

const Name = "openshell-gateway"
const ownerLabel = "hypershell.redhat.io/gateway-id"
const managerLabel = "app.kubernetes.io/managed-by"
const manager = "hypershell-gateway-controller"
const keysMarker = "hypershell.redhat.io/gateway-keys"
const keysName = "openshell-gateway-keys"

var digestImage = regexp.MustCompile(`^[a-z0-9][a-z0-9._:/-]*@sha256:[a-f0-9]{64}$`)
var dnsLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
var ErrPending = errors.New("Gateway workload is not ready")

func Namespace(id string) (string, error) {
	parsed, err := ksuid.Parse(id)
	if err != nil || parsed == ksuid.Nil || parsed.String() != id {
		return "", errors.New("invalid Gateway ID")
	}
	return "openshell-" + hex.EncodeToString(parsed.Payload()[:8]), nil
}

// SandboxNamespace separates sandbox permissions from Gateway credentials.
func SandboxNamespace(id string) (string, error) {
	ns, err := Namespace(id)
	if err != nil {
		return "", err
	}
	return "openshell-sandbox-" + ns[len("openshell-"):], nil
}

type oidcConfig struct {
	Issuer     string `json:"issuer"`
	ClientID   string `json:"client_id"`
	Audience   string `json:"audience"`
	JWKSTTL    int    `json:"jwks_ttl"`
	RolesClaim string `json:"roles_claim"`
	AdminRole  string `json:"admin_role"`
	UserRole   string `json:"user_role"`
}

func validate(gw *pb.Gateway, db *pb.ManagedDatabase, release *pb.GatewayRelease, issuer string) (oidcConfig, error) {
	var oidc oidcConfig
	ns, err := Namespace(gw.GetMetadata().GetId())
	if err != nil || gw.GetNamespace() != ns || gw.GetDatabaseId() == "" || gw.GetDatabaseId() != db.GetMetadata().GetId() || gw.GetReleaseId() != release.GetMetadata().GetId() {
		return oidc, errors.New("Gateway placement does not match its records")
	}
	dbNS, err := gateways.DatabaseNamespace(gw.GetDatabaseId())
	if err != nil || db.GetNamespace() != dbNS || (db.GetProvider() != gateways.ProviderDeployment && db.GetProvider() != gateways.ProviderCNPG) {
		return oidc, errors.New("Gateway requires a matching supported database")
	}
	if !digestImage.MatchString(release.GetImage()) || (gw.GetImage() != "" && gw.GetImage() != release.GetImage()) {
		return oidc, errors.New("Gateway image must match a release pinned by digest")
	}
	if gw.GetCredentialDriver() != "" || (gw.GetServiceType() != "" && gw.GetServiceType() != "ClusterIP") || gw.GetTlsMode() != "" || gw.GetExternalDns() != "" || len(gw.GetServerDnsNames()) != 0 {
		return oidc, errors.New("Gateway workload overrides are not supported")
	}
	if gw.GetOidc() == "" || db.GetStatus() != "ready" {
		return oidc, ErrPending
	}
	if json.Unmarshal([]byte(gw.GetOidc()), &oidc) != nil {
		return oidc, errors.New("invalid Gateway OIDC configuration")
	}
	clientID, err := keycloak.GatewayClientID(gw.Metadata.Id)
	if err != nil || oidc.Issuer != issuer || oidc.ClientID != clientID || oidc.Audience != clientID || oidc.RolesClaim != "hypershell.roles" || oidc.AdminRole != keycloak.RoleAdmin || oidc.UserRole != keycloak.RoleUser || oidc.JWKSTTL != 3600 {
		return oidc, errors.New("Gateway OIDC configuration does not match its trusted binding")
	}
	return oidc, nil
}

func validIssuer(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && u.Fragment == ""
}
