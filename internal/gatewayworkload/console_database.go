package gatewayworkload

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"net/url"
	"strconv"

	schema "github.com/jsell-rh/hypershell-stego/gateway-console/out/browser/schema"
	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	"github.com/jsell-rh/hypershell-stego/out/deploy/allocation"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
	postgres "github.com/jsell-rh/hypershell-stego/out/postgres"
)

const consoleStateProfile = "gateway-console-state"
const consoleStateSecret = "gateway-console-state"
const consoleStateMarker = "gateway-console-state-identity"
const consoleStateAnnotation = "hypershell.redhat.io/console-state-identity"

func ConsoleStateNamespace(id string) (string, error) {
	name, err := StateNamespace(id)
	if err != nil {
		return "", err
	}
	return "openshell-console-" + name[len("openshell-state-"):], nil
}
func (k *Kubernetes) consoleDatabaseKey(gw *pb.Gateway) postgres.DatabaseKey {
	return postgres.DatabaseKey{Scope: k.options.ClusterID, Resource: "console:" + gw.GetMetadata().GetId()}
}
func (k *Kubernetes) consoleStateOwner(id string) kube.Owner {
	own := owner(id)
	own[allocation.MarkerLabel] = k.allocation.Marker()
	own[allocation.ProfileLabel] = consoleStateProfile
	return own
}
func (k *Kubernetes) validateConsoleState(secret object, config postgres.Options) error {
	invalid := errors.New("console database state differs; restore its original records")
	count := 0
	switch values := secret["data"].(type) {
	case object:
		count = len(values)
	case map[string]any:
		count = len(values)
	default:
		return invalid
	}
	if count != 4 {
		return invalid
	}
	for _, key := range []string{"database-password", "database-server"} {
		raw, err := data(secret, key)
		decoded, e := hex.DecodeString(string(raw))
		if err != nil || e != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != string(raw) {
			return invalid
		}
	}
	key, err := data(secret, "session-key")
	if err != nil || len(key) != 32 {
		return invalid
	}
	destination, err := data(secret, "database-destination")
	if err != nil || string(destination) != k.destination(config) {
		return invalid
	}
	return nil
}
func (k *Kubernetes) loadConsoleState(ctx context.Context, gw *pb.Gateway, create bool) (object, postgres.Options, error) {
	config, err := k.databaseConfig()
	if err != nil {
		return nil, config, err
	}
	if !k.Handles(gw) || k.allocation == nil || k.options.ConsoleSQLBindings == nil {
		return nil, config, errors.New("console state requires its assigned allocator and SQL client")
	}
	id := gw.GetMetadata().GetId()
	ns, err := ConsoleStateNamespace(id)
	if err != nil {
		return nil, config, err
	}
	convert := func(value store.EffectBinding) kube.SecretStateBinding {
		return kube.SecretStateBinding{Present: value.Present, Closed: value.Closed, Digest: value.Digest}
	}
	callbacks := kube.SecretStateCallbacks{
		Load: func(operation context.Context) (kube.SecretStateBinding, error) {
			value, err := k.options.ConsoleSQLBindings.Load(operation, id, k.options.ClusterID)
			if err != nil {
				return kube.SecretStateBinding{}, err
			}
			if err = k.allocation.RequireNamespace(operation, consoleStateProfile, ns, id); err != nil {
				return kube.SecretStateBinding{}, err
			}
			return convert(value), nil
		},
		Bind: func(operation context.Context, digest string) (kube.SecretStateBinding, error) {
			value, err := k.options.ConsoleSQLBindings.Bind(operation, id, k.options.ClusterID, digest)
			return convert(value), err
		},
		Initialize: func(operation context.Context) (map[string]string, error) {
			prepared, err := postgres.PrepareDatabaseCredentials(operation, config, k.consoleDatabaseKey(gw))
			if err != nil {
				return nil, err
			}
			config.ServerIdentity = prepared.ServerIdentity
			key := make([]byte, 32)
			if _, err = rand.Read(key); err != nil {
				return nil, errors.New("console key generation failed")
			}
			values := map[string]string{"session-key": base64.StdEncoding.EncodeToString(key)}
			for name, value := range map[string]string{"database-password": prepared.Password, "database-server": prepared.ServerIdentity, "database-destination": k.destination(config)} {
				values[name] = base64.StdEncoding.EncodeToString([]byte(value))
			}
			return values, nil
		},
		Validate: func(secret object) error { return k.validateConsoleState(secret, config) },
	}
	state, err := k.client.LoadSecretState(ctx, kube.SecretStateOptions{Namespace: ns, Name: consoleStateSecret, Marker: consoleStateMarker, Annotation: consoleStateAnnotation, Owner: k.consoleStateOwner(id)}, callbacks, create)
	if errors.Is(err, kube.ErrSecretStatePending) || errors.Is(err, allocation.ErrPending) {
		err = ErrPending
	}
	if err != nil {
		return nil, config, err
	}
	server, _ := data(state, "database-server")
	config.ServerIdentity = string(server)
	return state, config, nil
}

// prepareConsoleDatabase uses the generated schema package through STEGO's
// bounded owner connection. The runtime login cannot install or change schema.
func (k *Kubernetes) prepareConsoleDatabase(ctx context.Context, gw *pb.Gateway) (object, error) {
	state, config, err := k.loadConsoleState(ctx, gw, true)
	if err != nil {
		return nil, err
	}
	password, err := data(state, "database-password")
	if err != nil {
		return nil, err
	}
	spec := postgres.DatabaseSpec{Key: k.consoleDatabaseKey(gw), Password: string(password), ConnectionLimit: 32, ManagedSchema: true}
	names, err := postgres.EnsureDatabase(ctx, config, spec)
	if err != nil {
		return nil, err
	}
	err = postgres.WithDatabaseOwner(ctx, config, spec, func(operation context.Context, conn *sql.Conn, identity postgres.DatabaseIdentity) error {
		return schema.Bootstrap(operation, conn, identity.User)
	})
	if err != nil {
		return nil, err
	}
	address := url.URL{Scheme: "postgresql", User: url.UserPassword(names.User, string(password)), Host: net.JoinHostPort(config.Host, strconv.Itoa(int(config.Port))), Path: "/" + names.Database}
	address.RawQuery = url.Values{"sslmode": {"verify-full"}, "sslrootcert": {"/var/run/stego/database-ca.pem"}}.Encode()
	key, _ := data(state, "session-key")
	files := object{}
	for name, value := range map[string]string{"database-url": address.String(), "database-ca.pem": string(config.CA), "session-key": base64.StdEncoding.EncodeToString(key)} {
		files[name] = base64.StdEncoding.EncodeToString([]byte(value))
	}
	return files, nil
}

func (k *Kubernetes) deleteConsoleDatabase(ctx context.Context, gw *pb.Gateway) error {
	if k.options.ConsoleSQLBindings == nil {
		return errors.New("console SQL cleanup requires its state client")
	}
	id := gw.GetMetadata().GetId()
	binding, err := k.options.ConsoleSQLBindings.Close(ctx, id, k.options.ClusterID)
	if err != nil {
		return err
	}
	if !binding.Present || !binding.Closed {
		return errors.New("console SQL closure was not confirmed")
	}
	if binding.Digest != "" {
		_, config, err := k.loadConsoleState(ctx, gw, false)
		if err != nil {
			return err
		}
		if err = postgres.DeleteDatabase(ctx, config, k.consoleDatabaseKey(gw)); err != nil {
			return err
		}
	}
	completed, err := k.options.ConsoleSQLBindings.Complete(ctx, id, k.options.ClusterID)
	if err != nil {
		return err
	}
	if !completed.Present || !completed.Closed || completed.Digest != binding.Digest {
		return errors.New("console SQL completion differs from its state")
	}
	return nil
}
