package serviceaccountkeycloak

import (
	"context"
	"encoding/hex"
	"fmt"
	"github.com/segmentio/ksuid"
	"strings"
	"testing"
)

func TestConsoleDomainPolicy(t *testing.T) {
	id := ksuid.New()
	want := "https://console-" + hex.EncodeToString(id.Bytes()) + ".apps.example.com"
	if got, err := GatewayConsoleOrigin(id.String(), "apps.example.com"); err != nil || got != want {
		t.Fatal("console origin differs", got, err)
	}
	for _, domain := range []string{"", "localhost", "127.0.0.1", "Apps.example.com", "a..com", "a.com.", "-a.com", "a_.com", "a.com/path", strings.Repeat("a", 64) + ".com", strings.Repeat("a.", 100) + "com"} {
		if _, err := GatewayConsoleOrigin(id.String(), domain); err == nil {
			t.Fatalf("invalid domain accepted: %q", domain)
		}
	}
	for _, bad := range []string{"", "invalid", ksuid.Nil.String()} {
		if _, err := GatewayConsoleOrigin(bad, "apps.example.com"); err == nil {
			t.Fatal("invalid ID accepted")
		}
	}
	raw := fmt.Sprintf(`{"%s":"apps.example.com"}`, id)
	parsed, err := ParseConsoleDomains(raw)
	if err != nil || parsed[id.String()] != "apps.example.com" {
		t.Fatal("domain policy failed", err)
	}
	copied, err := checkedConsoleDomains(parsed)
	if err != nil {
		t.Fatal(err)
	}
	parsed[id.String()] = "changed.example.com"
	if copied[id.String()] != "apps.example.com" {
		t.Fatal("operator policy retained mutable input")
	}
	for _, bad := range []string{"null", "[]", raw + " {}", raw[:len(raw)-1] + fmt.Sprintf(`,"%s":"other.example.com"}`, id), fmt.Sprintf(`{"%s":null}`, id), fmt.Sprintf(`{"%s":7}`, id), strings.Repeat(" ", 32769)} {
		if _, err := ParseConsoleDomains(bad); err == nil {
			t.Fatalf("invalid policy accepted: %.80q", bad)
		}
	}
	if _, err := NewClient(Options{ConsoleDomains: copied}); err == nil {
		t.Fatal("console configured without protected journals")
	}
	c := &Client{consoleDomains: copied}
	if _, err := c.EnsureGatewayWithConsole(context.Background(), ksuid.New().String(), "gateway", ksuid.New().String(), 1); err == nil {
		t.Fatal("missing cluster placement accepted")
	}
}
