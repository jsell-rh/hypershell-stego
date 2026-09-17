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
	"net/url"
	"os"
	"strconv"
	"syscall"

	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
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
func (k *Kubernetes) stateOwner(id string) kube.Owner {
	own := owner(id)
	own[allocation.MarkerLabel] = k.allocation.Marker()
	own[allocation.ProfileLabel] = "gateway-state"
	return own
}
func keyResourceOwned(value object, own kube.Owner) bool {
	return own.Matches(value) && kube.String(value, "metadata", "uid") != "" && kube.String(value, "metadata", "resourceVersion") != "" && kube.String(value, "metadata", "deletionTimestamp") == ""
}
func (k *Kubernetes) stateNamespace(ctx context.Context, gw *pb.Gateway) (string, error) {
	if !k.Handles(gw) || k.allocation == nil {
		return "", errors.New("Gateway state requires its assigned allocator")
	}
	id := gw.GetMetadata().GetId()
	ns, err := StateNamespace(id)
	if err != nil {
		return "", err
	}
	if err = k.allocation.RequireNamespace(ctx, "gateway-state", ns, id); err != nil {
		if errors.Is(err, allocation.ErrPending) {
			return "", ErrPending
		}
		return "", err
	}
	return ns, nil
}
func (k *Kubernetes) validateState(gw *pb.Gateway, secret object, config sql.Options) error {
	invalid := errors.New("Gateway durable state differs; restore its original records")
	if !keyResourceOwned(secret, k.stateOwner(gw.GetMetadata().GetId())) || secret["immutable"] != true {
		return invalid
	}
	return k.validateStateData(secret, config)
}
func (k *Kubernetes) validateStateData(secret object, config sql.Options) error {
	invalid := errors.New("Gateway durable state differs; restore its original records")
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
	if k.options.SQLBindings == nil {
		return nil, config, errors.New("SQL state registration is required")
	}
	if !k.Handles(gw) || k.allocation == nil {
		return nil, config, errors.New("Gateway state requires its assigned allocator")
	}
	id := gw.GetMetadata().GetId()
	ns, err := StateNamespace(id)
	if err != nil {
		return nil, config, err
	}
	convert := func(value store.EffectBinding) kube.SecretStateBinding {
		return kube.SecretStateBinding{Present: value.Present, Closed: value.Closed, Digest: value.Digest}
	}
	callbacks := kube.SecretStateCallbacks{
		Load: func(operation context.Context) (kube.SecretStateBinding, error) {
			value, err := k.options.SQLBindings.Load(operation, id, k.options.ClusterID)
			if err != nil {
				return kube.SecretStateBinding{}, err
			}
			if _, err = k.stateNamespace(operation, gw); err != nil {
				return kube.SecretStateBinding{}, err
			}
			return convert(value), nil
		},
		Bind: func(operation context.Context, digest string) (kube.SecretStateBinding, error) {
			value, err := k.options.SQLBindings.Bind(operation, id, k.options.ClusterID, digest)
			return convert(value), err
		},
		Initialize: func(operation context.Context) (map[string]string, error) {
			return k.newStateData(operation, gw, &config)
		},
		Validate: func(secret object) error { return k.validateStateData(secret, config) },
	}
	secret, err := k.client.LoadSecretState(ctx, kube.SecretStateOptions{Namespace: ns, Name: stateSecret, Marker: stateIdentity, Annotation: stateAnnotation, Owner: k.stateOwner(id)}, callbacks, create)
	if errors.Is(err, kube.ErrSecretStatePending) {
		err = ErrPending
	}
	if err != nil {
		return nil, config, err
	}
	server, _ := data(secret, "database-server")
	config.ServerIdentity = string(server)
	return secret, config, nil
}

// This callback supplies Gateway key policy. STEGO retains the resulting data
// before the worker can create or use its external database.
func (k *Kubernetes) newStateData(ctx context.Context, gw *pb.Gateway, config *sql.Options) (map[string]string, error) {
	prepared, err := sql.PrepareDatabaseCredentials(ctx, *config, k.databaseKey(gw))
	if err != nil {
		return nil, err
	}
	config.ServerIdentity = prepared.ServerIdentity
	keys, err := newKeys()
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	for key, value := range keys {
		encoded, ok := value.(string)
		if !ok {
			return nil, errors.New("Gateway key encoding is invalid")
		}
		values[key] = encoded
	}
	for key, value := range map[string]string{"database-password": prepared.Password, "database-server": prepared.ServerIdentity, "database-destination": k.destination(*config)} {
		values[key] = base64.StdEncoding.EncodeToString([]byte(value))
	}
	return values, nil
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
