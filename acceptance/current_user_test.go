package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	"github.com/jsell-rh/hypershell-stego/internal/users"
	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	"github.com/segmentio/ksuid"
)

func currentUser(t testing.TB, root, bearer string) httpapi.CurrentUser {
	t.Helper()
	code, body := requestJSON(t, "GET", root+"/users/me", bearer, nil)
	schema, err := openapi3.NewLoader().LoadFromFile("../contracts/extensions/current-user.openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
	var actual any
	if err := json.Unmarshal(body, &actual); err != nil {
		t.Fatal(err)
	}
	if err := schema.Paths.Value("/api/hypershell/v1/users/me").Get.Responses.Status(200).Value.Content.Get("application/json").Schema.Value.VisitJSON(actual); err != nil {
		t.Fatal("current user schema", code, err)
	}
	var user httpapi.CurrentUser
	var fields map[string]json.RawMessage
	if code != 200 || json.Unmarshal(body, &user) != nil || json.Unmarshal(body, &fields) != nil {
		t.Fatal("current user", code, string(body))
	}
	id, err := ksuid.Parse(user.ID)
	if err != nil || id == ksuid.Nil || id.String() != user.ID || user.Kind != "User" || user.Href != "/api/hypershell/v1/users/me" || user.CreatedAt.IsZero() || user.UpdatedAt.IsZero() || len(fields) != 8 {
		t.Fatal("current user shape", user)
	}
	return user
}

func TestCurrentUserThroughGeneratedRuntime(t *testing.T) {
	f := database(t)
	key, settings := issuer(t)
	_, config := broker(t, identity(t, "localhost"))
	binary := buildApplication(t)
	stop, address := startApplication(t, binary, f.dsn, config, settings...)
	defer func() { stop() }()
	root := address + "/api/hypershell/v1"
	claimIssuer := "https://issuer.example"
	sign := func(subject, username, email, audience string, expiry time.Time) string {
		t.Helper()
		claims := jwt.MapClaims{"iss": claimIssuer, "sub": subject, "aud": audience, "preferred_username": username, "email": email, "given_name": username, "family_name": "Example", "iat": time.Now().Add(-time.Minute).Unix(), "exp": expiry.Unix(), "realm_access": map[string]any{"roles": []string{}}}
		value, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	bearer := sign("recipient", "bob", "bob@example.test", "hypershell", time.Now().Add(time.Hour))
	for _, invalid := range []string{"", "forged", sign("recipient", "bob", "bob@example.test", "other-api", time.Now().Add(time.Hour)), sign("recipient", "bob", "bob@example.test", "hypershell", time.Now().Add(-time.Hour))} {
		if code, _ := requestJSON(t, "GET", root+"/users/me", invalid, nil); code != 401 {
			t.Fatal("current user authentication", code)
		}
	}
	for _, query := range []string{"?id=recipient", "?subject=recipient", "?issuer=https://other.example", "?search=username", "?fields=id"} {
		if code, _ := requestJSON(t, "GET", root+"/users/me"+query, bearer, nil); code != 400 {
			t.Fatal("target selection accepted", query, code)
		}
	}
	if code, _ := requestJSON(t, "GET", root+"/users/me", bearer, []byte(`{"id":"recipient"}`)); code != 400 {
		t.Fatal("request body accepted", code)
	}
	var count int
	if err := f.db.QueryRow("SELECT count(*) FROM users").Scan(&count); err != nil || count != 0 {
		t.Fatal("invalid request wrote identity", count, err)
	}

	user := currentUser(t, root, bearer)
	if user.Username != "bob" || user.Email != "bob@example.test" || user.Name != "bob Example" {
		t.Fatal("profile", user)
	}
	again := currentUser(t, root, bearer)
	if again.ID != user.ID || !again.CreatedAt.Equal(user.CreatedAt) || !again.UpdatedAt.Equal(user.UpdatedAt) {
		t.Fatal("unchanged identity was rewritten")
	}
	if err := f.db.QueryRow("SELECT count(*) FROM role_bindings WHERE user_id=$1", user.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("self lookup assigned a role", err)
	}
	renamedBearer := sign("recipient", "renamed-bob", "new@example.test", "hypershell", time.Now().Add(time.Hour))
	renamed := currentUser(t, root, renamedBearer)
	if renamed.ID != user.ID || !renamed.CreatedAt.Equal(user.CreatedAt) || !renamed.UpdatedAt.After(user.UpdatedAt) || renamed.Username != "renamed-bob" || renamed.Name != "renamed-bob Example" || renamed.Email != "new@example.test" {
		t.Fatal("profile change altered identity or lost timestamps", renamed)
	}
	other := currentUser(t, root, sign("other-subject", "renamed-bob", "new@example.test", "hypershell", time.Now().Add(time.Hour)))
	if other.ID == user.ID {
		t.Fatal("profile reuse adopted an identity")
	}

	// A failed profile update must not return the proposed profile as stored data.
	if _, err := f.db.Exec(`CREATE FUNCTION reject_profile() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'profile write rejected'; END $$; CREATE TRIGGER reject_profile BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION reject_profile()`); err != nil {
		t.Fatal(err)
	}
	if code, _ := requestJSON(t, "GET", root+"/users/me", bearer, nil); code != 500 {
		t.Fatal("failed profile update reported success", code)
	}
	var stored string
	if err := f.db.QueryRow("SELECT username FROM users WHERE id=$1", user.ID).Scan(&stored); err != nil || stored != "renamed-bob" {
		t.Fatal("failed profile update changed user", stored, err)
	}
	if _, err := f.db.Exec("DROP TRIGGER reject_profile ON users; DROP FUNCTION reject_profile()"); err != nil {
		t.Fatal(err)
	}
	stop()
	stop, address = startApplication(t, binary, f.dsn, config, settings...)
	root = address + "/api/hypershell/v1"
	after := currentUser(t, root, renamedBearer)
	if after.ID != user.ID || !after.CreatedAt.Equal(user.CreatedAt) || !after.UpdatedAt.Equal(renamed.UpdatedAt) {
		t.Fatal("restart changed identity", after)
	}
	// A deleted identity cannot be restored through a login request.
	if _, err := f.db.Exec("UPDATE users SET deleted_at=now() WHERE id=$1", other.ID); err != nil {
		t.Fatal(err)
	}
	if code, _ := requestJSON(t, "GET", root+"/users/me", sign("other-subject", "renamed-bob", "new@example.test", "hypershell", time.Now().Add(time.Hour)), nil); code != 409 {
		t.Fatal("deleted identity restored", code)
	}
	if err := f.db.QueryRow("SELECT count(*) FROM users WHERE id=$1 AND deleted_at IS NOT NULL", other.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("deleted identity changed", err)
	}
	stop()
	claimIssuer = "https://other-issuer.example"
	stop, address = startApplication(t, binary, f.dsn, config, append(settings, "STEGO_AUTH_ISSUER="+claimIssuer)...)
	root = address + "/api/hypershell/v1"
	if code, _ := requestJSON(t, "GET", root+"/users/me", renamedBearer, nil); code != 401 {
		t.Fatal("old issuer remained trusted", code)
	}
	foreign := currentUser(t, root, sign("recipient", "renamed-bob", "new@example.test", "hypershell", time.Now().Add(time.Hour)))
	if foreign.ID == user.ID {
		t.Fatal("different issuer adopted identity")
	}
}

func TestConcurrentCurrentUserRegistration(t *testing.T) {
	f := database(t)
	service, err := users.New(f.storage)
	if err != nil {
		t.Fatal(err)
	}
	const workers = 8
	start := make(chan struct{})
	type outcome struct {
		id  string
		err error
	}
	results := make(chan outcome, workers)
	var work sync.WaitGroup
	for range workers {
		work.Add(1)
		go func() {
			defer work.Done()
			<-start
			var result outcome
			for range 10 {
				row, err := service.Current(context.Background(), principal("new-recipient"))
				result = outcome{row.ID, err}
				if !errors.Is(err, store.ErrConflict) && !errors.Is(err, store.ErrSerialization) {
					break
				}
			}
			results <- result
		}()
	}
	close(start)
	work.Wait()
	close(results)
	var id string
	for result := range results {
		if result.err != nil || result.id == "" {
			t.Fatal("concurrent registration", result.err)
		}
		if id != "" && id != result.id {
			t.Fatal("concurrent registration created multiple identities")
		}
		id = result.id
	}
	var count int
	if err := f.db.QueryRow("SELECT count(*) FROM users WHERE issuer=$1 AND subject=$2", "https://issuer.example", "new-recipient").Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate identity", count, err)
	}
}

func BenchmarkCurrentUserLookup(b *testing.B) {
	f := database(b)
	service, err := users.New(f.storage)
	if err != nil {
		b.Fatal(err)
	}
	p := principal("current-user")
	user, err := service.Current(context.Background(), p)
	if err != nil {
		b.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO users(id,created_time,updated_time,username,issuer,subject,email,name)
 SELECT lpad(n::text,27,'0'),now(),now(),'shared-name','https://issuer.example','unrelated-'||n,'',''
 FROM generate_series(1,10000) AS n`); err != nil {
		b.Fatal(err)
	}
	if _, err := f.db.Exec("ANALYZE users"); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		got, err := service.Current(context.Background(), p)
		if err != nil || got.ID != user.ID {
			b.Fatal("current user lookup", err)
		}
	}
}
