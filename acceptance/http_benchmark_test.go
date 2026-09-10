package acceptance

import (
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	"github.com/jsell-rh/hypershell-stego/out/auth"
)

func BenchmarkRESTFilteredPage(b *testing.B) {
	benchmarkRESTPage(b, "")
}

func BenchmarkRESTSelectedPage(b *testing.B) {
	benchmarkRESTPage(b, "&fields=id,name")
}

func benchmarkRESTPage(b *testing.B, selection string) {
	f := gatewayPageBenchmarkFixture(b, true)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		b.Fatal(err)
	}
	verifier, err := auth.NewVerifier(auth.Config{Issuer: "https://issuer.example", Audience: "hypershell", PublicKey: &key.PublicKey, RolesClaim: "realm_access.roles"})
	if err != nil {
		b.Fatal(err)
	}
	handler, err := httpapi.New(f.storage, verifier, f.db)
	if err != nil {
		b.Fatal(err)
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": "https://issuer.example", "aud": "hypershell", "sub": "alice", "preferred_username": "alice", "email": "alice@example.test", "given_name": "alice", "iat": time.Now().Add(-time.Minute).Unix(), "exp": time.Now().Add(time.Hour).Unix()}).SignedString(key)
	if err != nil {
		b.Fatal(err)
	}
	var responseBytes int64
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		request := httptest.NewRequest(http.MethodGet, "/api/hypershell/v1/gateways?size=20"+selection, nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		responseBytes += int64(response.Body.Len())
		if response.Code != 200 {
			b.Fatalf("REST list: %d %s", response.Code, response.Body.String())
		}
	}
	b.ReportMetric(float64(responseBytes)/float64(b.N), "response-B/op")
}
