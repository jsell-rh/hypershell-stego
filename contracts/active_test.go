package contracts

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestActiveContractHasNoDatabaseCatalog(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	document, err := LoadActiveOpenAPI(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for path := range document.Paths.Map() {
		if strings.Contains(path, "managed_databases") {
			t.Fatal("active database endpoint remains", path)
		}
	}
	for name := range document.Components.Schemas {
		if strings.Contains(name, "ManagedDatabase") {
			t.Fatal("active database schema remains", name)
		}
	}
	collection := document.Paths.Value("/api/hypershell/v1/gateways")
	item := document.Paths.Value("/api/hypershell/v1/gateways/{id}")
	for name, schema := range map[string]*openapi3.Schema{
		"create":   collection.Post.RequestBody.Value.Content["application/json"].Schema.Value,
		"patch":    item.Patch.RequestBody.Value.Content["application/json"].Schema.Value,
		"response": item.Get.Responses.Value("200").Value.Content["application/json"].Schema.Value,
	} {
		if containsProperty(schema, "database_id", make(map[*openapi3.Schema]bool)) {
			t.Fatal("active Gateway contract has a database field", name)
		}
	}
}
