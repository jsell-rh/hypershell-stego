package gatewayworkload

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
	"github.com/segmentio/ksuid"
)

// Use every ID byte. Case folding of a KSUID can merge distinct identities.
func sharedResourceName(id string) (string, error) {
	parsed, err := ksuid.Parse(id)
	if err != nil || parsed == ksuid.Nil || parsed.String() != id {
		return "", errors.New("invalid Gateway ID")
	}
	return "gw-" + hex.EncodeToString(parsed.Bytes()), nil
}

func sharedOwner(gatewayID, databaseID string) kube.Owner {
	return kube.Owner{ownerLabel: gatewayID, managerLabel: manager, "hypershell.redhat.io/database-id": databaseID}
}
func keyResourceOwned(value object, own kube.Owner) bool {
	return own.Matches(value) && kube.String(value, "metadata", "uid") != "" && kube.String(value, "metadata", "resourceVersion") != "" && kube.String(value, "metadata", "deletionTimestamp") == ""
}

func sharedDefinition(kind, name, gatewayID, databaseID string) object {
	value := definition("v1", kind, name, gatewayID)
	value["metadata"].(object)["labels"].(object)["hypershell.redhat.io/database-id"] = databaseID
	return value
}

// Each Gateway has separate source keys and a separate immutable fingerprint.
// Return material only after both records exist. A missing pinned Secret must
// be restored. It must never cause a new encryption key to be generated.
func (k *Kubernetes) sharedKeys(ctx context.Context, gw *pb.Gateway, db *pb.ManagedDatabase) (object, error) {
	name, err := sharedResourceName(gw.GetMetadata().GetId())
	if err != nil {
		return nil, err
	}
	ns, err := gateways.DatabaseNamespace(db.GetMetadata().GetId())
	if err != nil || ns != db.GetNamespace() || gw.GetDatabaseId() != db.GetMetadata().GetId() {
		return nil, errors.New("Gateway database key placement is invalid")
	}
	namespace, code, err := k.client.Request(ctx, http.MethodGet, "/api/v1/namespaces/"+ns, nil)
	if err != nil {
		return nil, err
	}
	if code == 404 {
		return nil, ErrPending
	}
	if !keyResourceOwned(namespace, databaseOwner(db.Metadata.Id)) {
		return nil, errors.New("Gateway database key namespace is not available")
	}
	id, dbID := gw.Metadata.Id, db.Metadata.Id
	own := sharedOwner(id, dbID)
	core := "/api/v1/namespaces/" + ns
	marker, markerCode, err := k.client.Request(ctx, http.MethodGet, core+"/configmaps/"+name+"-key-identity", nil)
	if err != nil {
		return nil, err
	}
	if markerCode != 404 && (!keyResourceOwned(marker, own) || marker["immutable"] != true) {
		return nil, errors.New("Gateway key identity has a different owner or is not immutable")
	}
	secret, code, err := k.client.Request(ctx, http.MethodGet, core+"/secrets/"+name+"-keys", nil)
	if err != nil {
		return nil, err
	}
	if code == 404 {
		if markerCode != 404 {
			return nil, errors.New("Gateway keys are missing; restore the original Secret")
		}
		// A SQL database can contain ciphertext from an earlier key record. A lost
		// Secret and marker must not make that database use newly generated keys.
		if err := k.client.RequireResources(ctx, "postgresql.cnpg.io/v1", kube.APIResource{Name: "databases", Kind: "Database", Namespaced: true, Verbs: []string{"get"}}); err != nil {
			return nil, err
		}
		_, databaseCode, err := k.client.Request(ctx, http.MethodGet, "/apis/postgresql.cnpg.io/v1/namespaces/"+ns+"/databases/"+name, nil)
		if err != nil {
			return nil, err
		}
		if databaseCode != 404 {
			return nil, errors.New("Gateway keys are missing for an existing SQL database; restore the original keys")
		}
		values, err := newKeys()
		if err != nil {
			return nil, err
		}
		secret = sharedDefinition("Secret", name+"-keys", id, dbID)
		secret["type"] = "Opaque"
		secret["immutable"] = true
		secret["data"] = values
		secret, _, err = k.client.Request(ctx, http.MethodPost, core+"/secrets", secret)
		if err != nil {
			return nil, err
		}
	}
	if !keyResourceOwned(secret, own) || secret["immutable"] != true {
		return nil, errors.New("Gateway source keys have a different owner or are not immutable")
	}
	if err := validateKeys(secret); err != nil {
		return nil, err
	}
	values := object{}
	for _, key := range []string{"signing.pem", "public.pem", "kid", "key-encryption-key"} {
		values[key] = kube.String(secret, "data", key)
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	fingerprint := hex.EncodeToString(sha256sum(encoded))
	if markerCode == 404 {
		marker = sharedDefinition("ConfigMap", name+"-key-identity", id, dbID)
		marker["immutable"] = true
		marker["data"] = object{"sha256": fingerprint}
		marker, _, err = k.client.Request(ctx, http.MethodPost, core+"/configmaps", marker)
		if err != nil {
			return nil, err
		}
	}
	if !keyResourceOwned(marker, own) || marker["immutable"] != true || kube.String(marker, "data", "sha256") != fingerprint {
		return nil, errors.New("Gateway keys differ from their durable identity; restore the original Secret")
	}
	return values, nil
}
