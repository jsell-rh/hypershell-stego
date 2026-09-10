package gatewayworkload

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"strings"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

func databaseOwner(id string) kube.Owner {
	return kube.Owner{"hypershell.redhat.io/database-id": id, managerLabel: "hypershell-database-controller"}
}

// The database namespace retains the key material when the Gateway namespace
// is replaced. The marker prevents silent rekeying after a lost Secret.
func (k *Kubernetes) keys(ctx context.Context, gw *pb.Gateway, db *pb.ManagedDatabase) (object, error) {
	if db.GetProvider() == gateways.ProviderCNPG {
		return k.sharedKeys(ctx, gw, db)
	}
	namespace, code, err := k.client.Request(ctx, http.MethodGet, "/api/v1/namespaces/"+db.Namespace, nil)
	if err != nil {
		return nil, err
	}
	if code == 404 {
		return nil, ErrPending
	}
	if !databaseOwner(db.Metadata.Id).Matches(namespace) {
		return nil, errors.New("Gateway database namespace has a different owner")
	}
	id := gw.Metadata.Id
	marker := kube.String(namespace, "metadata", "annotations", keysMarker)
	linkedGateway := kube.String(namespace, "metadata", "labels", ownerLabel)
	if (marker == "") != (linkedGateway == "") {
		return nil, errors.New("Gateway key identity is incomplete; restore its original metadata")
	}
	if linkedGateway != "" && linkedGateway != id {
		return nil, errors.New("Gateway database namespace is linked to a different Gateway")
	}
	if marker != "" && !strings.HasPrefix(marker, id+":") {
		return nil, errors.New("Gateway database keys belong to a different Gateway")
	}
	collection := "/api/v1/namespaces/" + db.Namespace + "/secrets"
	secret, code, err := k.client.Request(ctx, http.MethodGet, collection+"/"+keysName, nil)
	if err != nil {
		return nil, err
	}
	if code == 404 {
		if marker != "" {
			return nil, errors.New("Gateway keys are missing; restore the original Secret")
		}
		values, err := newKeys()
		if err != nil {
			return nil, err
		}
		secret = definition("v1", "Secret", keysName, id)
		secret["type"] = "Opaque"
		secret["immutable"] = true
		secret["data"] = values
		secret, _, err = k.client.Request(ctx, http.MethodPost, collection, secret)
		if err != nil {
			return nil, err
		}
	}
	if !owner(id).Matches(secret) {
		return nil, errors.New("Gateway key Secret has a different owner")
	}
	if err := validateKeys(secret); err != nil {
		return nil, err
	}
	if secret["immutable"] != true {
		return nil, errors.New("Gateway source keys must be immutable")
	}
	values := object{}
	for _, key := range []string{"signing.pem", "public.pem", "kid", "key-encryption-key"} {
		values[key] = kube.String(secret, "data", key)
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	expected := id + ":" + hex.EncodeToString(sha256sum(encoded))
	if marker != "" && marker != expected {
		return nil, errors.New("Gateway keys differ from their durable identity; restore the original Secret")
	}
	if marker == "" || linkedGateway == "" {
		// Use the first namespace observation. A conflict requires another pass.
		patch := object{"metadata": object{"uid": kube.String(namespace, "metadata", "uid"), "resourceVersion": kube.String(namespace, "metadata", "resourceVersion"), "annotations": object{keysMarker: expected}, "labels": object{ownerLabel: id}}}
		if kube.String(patch, "metadata", "uid") == "" || kube.String(patch, "metadata", "resourceVersion") == "" {
			return nil, errors.New("Gateway database namespace has no identity")
		}
		if _, _, err = k.client.Request(ctx, http.MethodPatch, "/api/v1/namespaces/"+db.Namespace, patch); err != nil {
			return nil, err
		}
	}
	return values, nil
}
func newKeys() (object, error) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		return nil, err
	}
	publicDER, err := x509.MarshalPKIXPublicKey(public)
	if err != nil {
		return nil, err
	}
	kek := make([]byte, 32)
	if _, err = rand.Read(kek); err != nil {
		return nil, err
	}
	values := object{}
	for key, value := range map[string][]byte{"signing.pem": pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}), "public.pem": pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}), "kid": []byte(hex.EncodeToString(sha256sum(publicDER))), "key-encryption-key": []byte(base64.StdEncoding.EncodeToString(kek))} {
		values[key] = base64.StdEncoding.EncodeToString(value)
	}
	return values, nil
}
func validateKeys(secret object) error {
	invalid := errors.New("Gateway signing or encryption keys are invalid")
	privatePEM, err := data(secret, "signing.pem")
	if err != nil {
		return invalid
	}
	privateBlock, rest := pem.Decode(privatePEM)
	if privateBlock == nil || len(bytes.TrimSpace(rest)) != 0 {
		return invalid
	}
	key, err := x509.ParsePKCS8PrivateKey(privateBlock.Bytes)
	if err != nil {
		return invalid
	}
	private, ok := key.(ed25519.PrivateKey)
	if !ok {
		return invalid
	}
	publicPEM, err := data(secret, "public.pem")
	if err != nil {
		return invalid
	}
	publicBlock, rest := pem.Decode(publicPEM)
	if publicBlock == nil || len(bytes.TrimSpace(rest)) != 0 {
		return invalid
	}
	key, err = x509.ParsePKIXPublicKey(publicBlock.Bytes)
	if err != nil {
		return invalid
	}
	public, ok := key.(ed25519.PublicKey)
	if !ok || !private.Public().(ed25519.PublicKey).Equal(public) {
		return invalid
	}
	kid, err := data(secret, "kid")
	if err != nil || string(kid) != hex.EncodeToString(sha256sum(publicBlock.Bytes)) {
		return invalid
	}
	kek, err := data(secret, "key-encryption-key")
	if err != nil {
		return invalid
	}
	decoded, err := base64.StdEncoding.DecodeString(string(kek))
	if err != nil || len(decoded) != 32 {
		return invalid
	}
	return nil
}
