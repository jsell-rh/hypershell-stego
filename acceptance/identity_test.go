package acceptance

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/out/auth"
)

func TestGatewayRulesUseVerifiedIssuerRoles(t *testing.T) {
	f := database(t)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	public, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "issuer.pem")
	if err := os.WriteFile(file, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: public}), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STEGO_AUTH_PUBLIC_KEY_FILE", file)
	t.Setenv("STEGO_AUTH_ISSUER", "https://issuer.example")
	t.Setenv("STEGO_AUTH_AUDIENCE", "hypershell")
	t.Setenv("STEGO_AUTH_ROLES_CLAIM", "")
	verifier, err := auth.NewVerifierFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	claims := jwt.MapClaims{
		"iss": "https://issuer.example", "aud": "hypershell", "sub": "alice-id", "preferred_username": "alice",
		"email": "alice@example.test", "given_name": "Alice", "family_name": "Example",
		"iat": time.Now().Add(-time.Minute).Unix(), "exp": time.Now().Add(time.Hour).Unix(),
		"realm_access": map[string]any{"roles": []string{"gateway:creator"}},
	}
	verify := func() context.Context {
		t.Helper()
		raw, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		ctx, err := verifier.Authenticate(context.Background(), raw)
		if err != nil {
			t.Fatal(err)
		}
		return ctx
	}
	ctx := verify()
	gateway, err := f.service.Create(ctx, gateways.PrincipalFromContext(ctx), f.request("verified-creator"))
	if err != nil {
		t.Fatal(err)
	}
	var email, name string
	if err := f.db.QueryRow("SELECT email,name FROM users WHERE username='alice'").Scan(&email, &name); err != nil {
		t.Fatal(err)
	}
	if email != "alice@example.test" || name != "Alice Example" {
		t.Fatal("verified user profile was not stored")
	}
	claims["realm_access"] = map[string]any{"roles": []string{"platform:admin"}}
	claims["role"] = "gateway:creator" // This unselected claim must not grant access.
	ctx = verify()
	if _, err := f.service.Create(ctx, gateways.PrincipalFromContext(ctx), f.request("wrong-role-source")); !errors.Is(err, gateways.ErrForbidden) {
		t.Fatalf("wrong role source granted creation: %v", err)
	}
	claims["realm_access"] = map[string]any{"roles": []string{}}
	delete(claims, "role")
	ctx = verify()
	if _, err := f.service.Get(ctx, gateways.PrincipalFromContext(ctx), gateway.ID); err != nil {
		t.Fatalf("stored ownership did not survive creator removal: %v", err)
	}
	claims["iss"] = "https://other-issuer.example"
	raw, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Authenticate(context.Background(), raw); err == nil {
		t.Fatal("wrong issuer was accepted")
	}
	if _, err := f.service.Get(context.Background(), gateways.PrincipalFromContext(context.Background()), gateway.ID); !errors.Is(err, gateways.ErrIdentity) {
		t.Fatalf("unverified request reached storage: %v", err)
	}
}
