package acceptance

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gatewayworkload"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
	postgres "github.com/jsell-rh/hypershell-stego/out/postgres"
)

// Inspect bytes written by the actual Gateway after namespace recovery. Use
// Go's AEAD implementation to verify the pinned Gateway's storage contract.
// Do not change stored ciphertext, keys, or provider data during these probes.
func (w *browserGatewayWorkload) checkCredentialEncryption(id string) {
	w.t.Helper()
	const known = "acceptance-only-upstream-secret"
	options, _ := w.sqlOptions(id)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	var objectID string
	var payload []byte
	var count int
	err := postgres.ReadRow(ctx, options, `SELECT id,payload,count(*) OVER () FROM public.objects WHERE object_type='credential.gateway-encrypted' LIMIT 1`, nil, &objectID, &payload, &count)
	cancel()
	if err != nil || count != 1 || len(payload) == 0 || len(payload) > 8192 {
		w.t.Fatal("credential encryption requires one bounded stored envelope")
	}
	defer clear(payload)
	type encrypted struct {
		Nonce      string `json:"nonce"`
		Ciphertext string `json:"ciphertext"`
	}
	var envelope struct {
		Version    int       `json:"version"`
		ID         string    `json:"id"`
		Provider   string    `json:"provider_name"`
		Credential string    `json:"credential_key"`
		Algorithm  string    `json:"algorithm"`
		KeyID      string    `json:"key_encryption_key_id"`
		WrappedKey encrypted `json:"wrapped_dek"`
		Value      encrypted `json:"value"`
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&envelope) != nil || decoder.Decode(new(any)) != io.EOF || envelope.Version != 1 || envelope.Algorithm != "AES-256-GCM" || envelope.ID != objectID || envelope.Provider != "browser-provider" || envelope.Credential != "OPENAI_API_KEY" {
		w.t.Fatal("stored credential envelope differs from the pinned Gateway contract")
	}
	decodedID, err := hex.DecodeString(objectID)
	if err != nil || len(decodedID) != 32 || hex.EncodeToString(decodedID) != objectID {
		w.t.Fatal("stored credential identity is invalid")
	}
	decode := func(text string, limit int) []byte {
		w.t.Helper()
		if len(text) > base64.StdEncoding.EncodedLen(limit) {
			w.t.Fatal("credential encryption field exceeds its bound")
		}
		value, err := base64.StdEncoding.Strict().DecodeString(text)
		if err != nil || len(value) > limit || base64.StdEncoding.EncodeToString(value) != text {
			w.t.Fatal("credential encryption field is invalid")
		}
		return value
	}
	key := func(gatewayID string) []byte {
		w.t.Helper()
		state, err := gatewayworkload.StateNamespace(gatewayID)
		if err != nil {
			w.t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		secret, code, err := w.kubernetes.Request(ctx, "GET", "/api/v1/namespaces/"+state+"/secrets/openshell-gateway-state", nil)
		if err != nil || code != 200 || kube.String(secret, "metadata", "uid") == "" {
			w.t.Fatal("credential encryption source key is unavailable")
		}
		text := decode(kube.String(secret, "data", "key-encryption-key"), 44)
		defer clear(text)
		value := decode(string(text), 32)
		if len(value) != 32 {
			w.t.Fatal("credential encryption key length is invalid")
		}
		return value
	}
	kek := key(id)
	defer clear(kek)
	digest := sha256.Sum256(kek)
	if envelope.KeyID != "sha256:"+hex.EncodeToString(digest[:]) {
		w.t.Fatal("stored credential uses a different retained key")
	}
	aead := func(key []byte) cipher.AEAD {
		w.t.Helper()
		block, err := aes.NewCipher(key)
		if err != nil {
			w.t.Fatal("credential encryption key is invalid")
		}
		value, err := cipher.NewGCM(block)
		if err != nil {
			w.t.Fatal("credential encryption algorithm is unavailable")
		}
		return value
	}
	wrappedNonce, valueNonce := decode(envelope.WrappedKey.Nonce, 12), decode(envelope.Value.Nonce, 12)
	wrapped, value := decode(envelope.WrappedKey.Ciphertext, 48), decode(envelope.Value.Ciphertext, 4096)
	if len(wrappedNonce) != 12 || len(valueNonce) != 12 || len(wrapped) != 48 || len(value) != len(known)+16 {
		w.t.Fatal("credential encryption nonce or ciphertext length is invalid")
	}
	contextSuffix := objectID + ":" + envelope.Provider + ":" + envelope.Credential
	wrappedContext := []byte("openshell:gateway-credential-storage:v1:dek:" + contextSuffix)
	valueContext := []byte("openshell:gateway-credential-storage:v1:value:" + contextSuffix)
	dek, err := aead(kek).Open(nil, wrappedNonce, wrapped, wrappedContext)
	if err != nil || len(dek) != 32 || bytes.Equal(dek, kek) {
		w.t.Fatal("stored data key does not unwrap with the retained Gateway key")
	}
	defer clear(dek)
	plaintext, err := aead(dek).Open(nil, valueNonce, value, valueContext)
	defer clear(plaintext)
	if err != nil || !bytes.Equal(plaintext, []byte(known)) {
		w.t.Fatal("stored credential does not authenticate and decrypt to the supplied value")
	}
	reject := func(key, nonce, ciphertext, context []byte) {
		w.t.Helper()
		result, err := aead(key).Open(nil, nonce, ciphertext, context)
		clear(result)
		if err == nil {
			w.t.Fatal("stored credential authentication accepted an invalid key, ciphertext, or context")
		}
	}
	otherKeys := 0
	for _, other := range w.gatewayIDs {
		if other != id {
			otherKey := key(other)
			if bytes.Equal(kek, otherKey) {
				clear(otherKey)
				w.t.Fatal("Gateways share a credential encryption key")
			}
			reject(otherKey, wrappedNonce, wrapped, wrappedContext)
			clear(otherKey)
			otherKeys++
		}
	}
	if otherKeys != 1 {
		w.t.Fatal("credential encryption isolation requires the other Gateway")
	}
	altered := bytes.Clone(wrapped)
	altered[len(altered)-1] ^= 1
	reject(kek, wrappedNonce, altered, wrappedContext)
	altered = bytes.Clone(value)
	altered[len(altered)-1] ^= 1
	reject(dek, valueNonce, altered, valueContext)
	reject(kek, wrappedNonce, wrapped, append(bytes.Clone(wrappedContext), 0))
	reject(dek, valueNonce, value, append(bytes.Clone(valueContext), 0))
	// Check all object payloads, including the provider object, for a duplicate
	// plaintext value. Keep inventory and aggregate payload bytes bounded.
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	var rows, payloadBytes, exposed int64
	err = postgres.ReadRow(ctx, options, `SELECT count(*),COALESCE(sum(octet_length(payload)),0),count(*) FILTER (WHERE position(convert_to($1,'UTF8') IN payload)>0 OR position(convert_to($2,'UTF8') IN payload)>0) FROM public.objects`, []any{known, base64.StdEncoding.EncodeToString([]byte(known))}, &rows, &payloadBytes, &exposed)
	cancel()
	if err != nil || rows < 2 || rows > 64 || payloadBytes > 65536 || exposed != 0 {
		w.t.Fatal("stored object payloads expose a credential or exceed the fixture bounds")
	}
	if directory := os.Getenv("STEGO_BROWSER_ARTIFACT_DIR"); directory != "" {
		record := map[string]any{"gateway_id": id, "envelope_version": envelope.Version, "algorithm": envelope.Algorithm, "stored_envelopes": count, "object_rows": rows, "after_namespace_recovery": true, "retained_key_unwrapped_data_key": true, "authenticated_plaintext_matches": true, "other_gateway_key_rejected": true, "altered_wrapped_key_rejected": true, "altered_value_rejected": true, "altered_wrapping_context_rejected": true, "altered_value_context_rejected": true, "no_plaintext_or_base64_in_object_payloads": true}
		data, err := json.MarshalIndent(record, "", "  ")
		if err != nil || os.WriteFile(filepath.Join(directory, "credential-encryption.json"), append(data, '\n'), 0600) != nil {
			w.t.Fatal("cannot write credential encryption evidence")
		}
	}
	w.t.Log("Stored Gateway credentials use authenticated envelope encryption after namespace recovery; another Gateway key, altered ciphertext, and altered context are rejected. Object payloads contain no plaintext or base64 copy of the credential")
}
