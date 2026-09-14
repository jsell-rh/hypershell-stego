package databaseapp

import (
	"context"
	"strings"
	"testing"
)

func TestRemovedDatabaseProviderFailsBeforeClients(t *testing.T) {
	t.Setenv("DATABASE_PROVIDER", "deployment")
	err := Run(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "provider is not supported") {
		t.Fatal("removed provider reached client setup", err)
	}
}
