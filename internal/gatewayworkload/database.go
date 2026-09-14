package gatewayworkload

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"syscall"

	"github.com/jsell-rh/hypershell-stego/out/deploy/allocation"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
	sql "github.com/jsell-rh/hypershell-stego/out/postgres"
)

const stateSecret = "openshell-gateway-state"
const stateIdentity = "openshell-state-identity"
const stateAnnotation = "hypershell.redhat.io/state-identity"

// databaseConfig is one file from the controller's declared Secret. Read the
// file again for each attempt so that Secret rotation does not require restart.
// The administrator's credentials never enter Gateway state or its workload.
type databaseConfig struct {
	Host     string `json:"host"`
	Port     uint16 `json:"port"`
	Database string `json:"database"`
	User     string `json:"user"`
	Password string `json:"password"`
	CA       string `json:"ca"`
}

func (k *Kubernetes) databaseConfig() (sql.Options, error) {
	invalid := errors.New("Gateway database configuration is missing or invalid")
	f, err := os.OpenFile(k.options.DatabaseConfigFile, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return sql.Options{}, invalid
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Size() > 131072 || info.Mode().Perm()&0137 != 0 {
		return sql.Options{}, invalid
	}
	raw, err := io.ReadAll(io.LimitReader(f, 131073))
	if err != nil || len(raw) > 131072 {
		return sql.Options{}, invalid
	}
	var value databaseConfig
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF {
		return sql.Options{}, invalid
	}
	// STEGO validates the complete TLS and SQL identity before connecting.
	return sql.Options{Host: value.Host, Port: value.Port, Database: value.Database, User: value.User, Password: value.Password, CA: []byte(value.CA)}, nil
}
func (k *Kubernetes) databaseKey(gw *pb.Gateway) sql.DatabaseKey {
	return sql.DatabaseKey{Scope: k.options.ClusterID, Resource: gw.GetMetadata().GetId()}
}
func (k *Kubernetes) destination(config sql.Options) string {
	encoded, _ := json.Marshal([]string{k.options.ClusterID, config.Host, strconv.Itoa(int(config.Port)), config.Database})
	return hex.EncodeToString(sha256sum(encoded))
}
func stateFingerprint(secret object) (string, error) {
	values := secret["data"]
	switch values.(type) {
	case object, map[string]any:
	default:
		return "", errors.New("Gateway state has no data")
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return "", errors.New("Gateway state is invalid")
	}
	return hex.EncodeToString(sha256sum(encoded)), nil
}
func (k *Kubernetes) stateOwner(id string) kube.Owner {
	own := owner(id)
	own[allocation.MarkerLabel] = k.allocation.Marker()
	own[allocation.ProfileLabel] = "gateway-state"
	return own
}
func (k *Kubernetes) stateDefinition(kind, name, id string) object {
	value := definition("v1", kind, name, id)
	labels := value["metadata"].(object)["labels"].(object)
	for key, val := range k.stateOwner(id) {
		labels[key] = val
	}
	value["immutable"] = true
	return value
}
func keyResourceOwned(value object, own kube.Owner) bool {
	return own.Matches(value) && kube.String(value, "metadata", "uid") != "" && kube.String(value, "metadata", "resourceVersion") != "" && kube.String(value, "metadata", "deletionTimestamp") == ""
}
func (k *Kubernetes) stateNamespace(ctx context.Context, gw *pb.Gateway) (string, object, error) {
	if !k.Handles(gw) || k.allocation == nil {
		return "", nil, errors.New("Gateway state requires its assigned allocator")
	}
	id := gw.GetMetadata().GetId()
	ns, err := StateNamespace(id)
	if err != nil {
		return "", nil, err
	}
	if err = k.allocation.RequireNamespace(ctx, "gateway-state", ns, id); err != nil {
		if errors.Is(err, allocation.ErrPending) {
			return "", nil, ErrPending
		}
		return "", nil, err
	}
	namespace, code, err := k.client.Request(ctx, http.MethodGet, "/api/v1/namespaces/"+ns, nil)
	if err != nil {
		return "", nil, err
	}
	if code == 404 {
		return "", nil, ErrPending
	}
	if !keyResourceOwned(namespace, k.stateOwner(id)) {
		return "", nil, errors.New("Gateway state namespace has a different owner")
	}
	return ns, namespace, nil
}
func (k *Kubernetes) validateState(gw *pb.Gateway, secret object, config sql.Options) error {
	invalid := errors.New("Gateway durable state differs; restore its original records")
	if !keyResourceOwned(secret, k.stateOwner(gw.GetMetadata().GetId())) || secret["immutable"] != true {
		return invalid
	}
	if err := validateKeys(secret); err != nil {
		return err
	}
	password, err := data(secret, "database-password")
	decoded, e := hex.DecodeString(string(password))
	if err != nil || e != nil || len(decoded) != 32 || len(password) != 64 {
		return invalid
	}
	server, err := data(secret, "database-server")
	decoded, e = hex.DecodeString(string(server))
	if err != nil || e != nil || len(decoded) != 32 || len(server) != 64 {
		return invalid
	}
	destination, err := data(secret, "database-destination")
	if err != nil || string(destination) != k.destination(config) {
		return errors.New("Gateway database destination changed; migration is not supported")
	}
	return nil
}
func (k *Kubernetes) localState(ctx context.Context, gw *pb.Gateway) (object, sql.Options, error) {
	return k.loadLocalState(ctx, gw, true)
}
func (k *Kubernetes) readLocalState(ctx context.Context, gw *pb.Gateway) (object, sql.Options, error) {
	return k.loadLocalState(ctx, gw, false)
}
func (k *Kubernetes) loadLocalState(ctx context.Context, gw *pb.Gateway, create bool) (object, sql.Options, error) {
	config, err := k.databaseConfig()
	if err != nil {
		return nil, config, err
	}
	ns, namespace, err := k.stateNamespace(ctx, gw)
	if err != nil {
		return nil, config, err
	}
	id := gw.GetMetadata().GetId()
	core := "/api/v1/namespaces/" + ns
	pinned := kube.String(namespace, "metadata", "annotations", stateAnnotation)
	marker, markerCode, err := k.client.Request(ctx, http.MethodGet, core+"/configmaps/"+stateIdentity, nil)
	if err != nil {
		return nil, config, err
	}
	if markerCode != 404 && (!keyResourceOwned(marker, k.stateOwner(id)) || marker["immutable"] != true) {
		return nil, config, errors.New("Gateway state identity is invalid")
	}
	secret, code, err := k.client.Request(ctx, http.MethodGet, core+"/secrets/"+stateSecret, nil)
	if err != nil {
		return nil, config, err
	}
	if code == 404 {
		if !create || pinned != "" || markerCode != 404 {
			return nil, config, errors.New("Gateway state is missing; restore the original Secret")
		}
		// SQL identity is pinned before the first SQL database operation. A new
		// Secret cannot adopt old SQL data after both local key records are lost.
		server, err := sql.DatabaseServerIdentity(ctx, config)
		if err != nil {
			return nil, config, err
		}
		config.ServerIdentity = server
		names, err := sql.DatabaseNames(k.databaseKey(gw))
		if err != nil {
			return nil, config, err
		}
		var absent bool
		err = sql.ReadRow(ctx, config, `SELECT NOT EXISTS(SELECT 1 FROM pg_catalog.pg_database WHERE datname=$1) AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_roles WHERE rolname=$2 OR rolname=$3)`, []any{names.Database, names.Owner, names.User}, &absent)
		if err != nil {
			return nil, config, err
		}
		if !absent {
			return nil, config, errors.New("Gateway keys are missing for existing SQL state")
		}
		values, err := newKeys()
		if err != nil {
			return nil, config, err
		}
		password, err := sql.NewDatabasePassword()
		if err != nil {
			return nil, config, err
		}
		for key, value := range map[string]string{"database-password": password, "database-server": server, "database-destination": k.destination(config)} {
			values[key] = base64.StdEncoding.EncodeToString([]byte(value))
		}
		secret = k.stateDefinition("Secret", stateSecret, id)
		secret["type"] = "Opaque"
		secret["data"] = values
		secret, _, err = k.client.Request(ctx, http.MethodPost, core+"/secrets", secret)
		if err != nil {
			return nil, config, err
		}
	}
	if err = k.validateState(gw, secret, config); err != nil {
		return nil, config, err
	}
	fingerprint, err := stateFingerprint(secret)
	if err != nil {
		return nil, config, err
	}
	if pinned != "" && pinned != fingerprint {
		return nil, config, errors.New("Gateway state differs from its namespace identity")
	}
	if markerCode == 404 {
		if !create {
			return nil, config, errors.New("Gateway state identity is missing")
		}
		marker = k.stateDefinition("ConfigMap", stateIdentity, id)
		marker["data"] = object{"sha256": fingerprint}
		marker, _, err = k.client.Request(ctx, http.MethodPost, core+"/configmaps", marker)
		if err != nil {
			return nil, config, err
		}
	}
	if !keyResourceOwned(marker, k.stateOwner(id)) || marker["immutable"] != true || kube.String(marker, "data", "sha256") != fingerprint {
		return nil, config, errors.New("Gateway state differs from its durable identity")
	}
	// The allocator must retain the public fingerprint before SQL can start.
	if pinned != fingerprint {
		return nil, config, ErrPending
	}
	server, _ := data(secret, "database-server")
	config.ServerIdentity = string(server)
	return secret, config, nil
}
func (k *Kubernetes) databaseCredentials(ctx context.Context, gw *pb.Gateway, state object, config sql.Options) (object, error) {
	password, err := data(state, "database-password")
	if err != nil {
		return nil, err
	}
	identity, err := sql.EnsureDatabase(ctx, config, sql.DatabaseSpec{Key: k.databaseKey(gw), Password: string(password), ConnectionLimit: 32})
	if err != nil {
		return nil, err
	}
	address := url.URL{Scheme: "postgresql", User: url.UserPassword(identity.User, string(password)), Host: net.JoinHostPort(config.Host, strconv.Itoa(int(config.Port))), Path: "/" + identity.Database}
	address.RawQuery = url.Values{"sslmode": {"verify-full"}, "sslrootcert": {"/etc/openshell-db/ca.crt"}}.Encode()
	result := object{}
	for key, value := range map[string]string{"host": config.Host, "port": strconv.Itoa(int(config.Port)), "dbname": identity.Database, "user": identity.User, "password": string(password), "sslmode": "verify-full", "uri": address.String(), "ca.crt": string(config.CA)} {
		result[key] = base64.StdEncoding.EncodeToString([]byte(value))
	}
	return result, nil
}
func (k *Kubernetes) deleteDatabase(ctx context.Context, gw *pb.Gateway, state object, config sql.Options) error {
	// The retained source state pins the server even after workload deletion.
	if err := k.validateState(gw, state, config); err != nil {
		return err
	}
	return sql.DeleteDatabase(ctx, config, k.databaseKey(gw))
}
