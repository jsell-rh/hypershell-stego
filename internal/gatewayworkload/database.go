package gatewayworkload

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"github.com/jsell-rh/hypershell-stego/internal/databasecontroller"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"net/http"
	"net/url"
)

func (k *Kubernetes) databaseCredentials(ctx context.Context, gw *pb.Gateway, db *pb.ManagedDatabase) (object, error) {
	if db.GetProvider() == gateways.ProviderCNPG {
		return k.cnpgCredentials(ctx, gw, db)
	}
	dbSecret, code, err := k.client.Request(ctx, http.MethodGet, "/api/v1/namespaces/"+db.Namespace+"/secrets/"+databasecontroller.CredentialsName, nil)
	if err != nil {
		return nil, err
	}
	if code == 404 {
		return nil, ErrPending
	}
	if !databaseOwner(db.Metadata.Id).Matches(dbSecret) {
		return nil, errors.New("Gateway database Secret has a different owner")
	}
	dbData := object{}
	for key, want := range map[string]string{"host": databasecontroller.WorkloadName + "." + db.Namespace + ".svc.cluster.local", "user": "openshell", "dbname": "openshell", "port": "5432", "sslmode": "verify-full"} {
		value, err := data(dbSecret, key)
		if err != nil || string(value) != want {
			return nil, errors.New("Gateway database connection does not match its placement")
		}
	}
	password, err := data(dbSecret, "password")
	if err != nil {
		return nil, err
	}
	if _, err = hex.DecodeString(string(password)); err != nil || len(password) != 64 {
		return nil, errors.New("Gateway database password is invalid")
	}
	ca, err := data(dbSecret, "ca.crt")
	if err != nil || !x509.NewCertPool().AppendCertsFromPEM(ca) {
		return nil, errors.New("Gateway database CA is invalid")
	}
	address := url.URL{Scheme: "postgresql", User: url.UserPassword("openshell", string(password)), Host: databasecontroller.WorkloadName + "." + db.Namespace + ".svc.cluster.local:5432", Path: "/openshell"}
	address.RawQuery = url.Values{"sslmode": {"verify-full"}, "sslrootcert": {"/etc/openshell-db/ca.crt"}}.Encode()
	dbData["uri"] = base64.StdEncoding.EncodeToString([]byte(address.String()))
	dbData["ca.crt"] = base64.StdEncoding.EncodeToString(ca)
	return dbData, nil
}
