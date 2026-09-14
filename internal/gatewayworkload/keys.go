package gatewayworkload

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
)

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
