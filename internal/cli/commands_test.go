package cli

import (
	"github.com/jsell-rh/hypershell-stego/internal/catalog"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/internal/serviceaccounts"
	command "github.com/jsell-rh/hypershell-stego/out/cli/command"
	"reflect"
	"strings"
	"testing"
)

func TestGatewayCreationFieldsFollowDomainContract(t *testing.T) {
	checkFields(t, Commands().Commands[0].Fields, reflect.TypeFor[gateways.CreateRequest]())
}

func TestAccountCreationFieldsFollowDomainContract(t *testing.T) {
	checkFields(t, accountCommands()[0].Fields, reflect.TypeFor[serviceaccounts.CreateRequest]())
}

func checkFields(t *testing.T, fields []command.Field, request reflect.Type) {
	t.Helper()
	if len(fields) != request.NumField() {
		t.Fatal("CLI creation fields differ from domain request")
	}
	for i := 0; i < request.NumField(); i++ {
		field := request.Field(i)
		key, options, _ := strings.Cut(field.Tag.Get("json"), ",")
		found := false
		for _, definition := range fields {
			if definition.Key != key {
				continue
			}
			found = true
			required := !strings.Contains(options, "omitempty")
			if definition.Required != required {
				t.Fatalf("CLI required state differs for %s", key)
			}
			kind := "string"
			valueType := field.Type
			if valueType.Kind() == reflect.Pointer {
				valueType = valueType.Elem()
			}
			switch valueType.Kind() {
			case reflect.Slice:
				kind = "string-list"
			case reflect.Int, reflect.Int32, reflect.Int64:
				kind = "integer"
			case reflect.Bool:
				kind = "boolean"
			}
			if definition.Type != kind {
				t.Fatalf("CLI type differs for %s", key)
			}
		}
		if !found {
			t.Fatalf("CLI does not expose %s", key)
		}
	}
}

func TestGrantCreationFieldsFollowDomainContract(t *testing.T) {
	for _, definition := range grantCommands() {
		if definition.Method == "POST" {
			checkFields(t, definition.Fields, reflect.TypeFor[gateways.GrantRequest]())
		}
	}
}

func TestCatalogCreationFieldsFollowDomainContract(t *testing.T) {
	types := map[string]reflect.Type{
		"/api/hypershell/v1/gateway_networks":  reflect.TypeFor[catalog.NetworkCreate](),
		"/api/hypershell/v1/managed_clusters":  reflect.TypeFor[catalog.ClusterCreate](),
		"/api/hypershell/v1/gateway_releases":  reflect.TypeFor[catalog.ReleaseCreate](),
		"/api/hypershell/v1/managed_databases": reflect.TypeFor[catalog.DatabaseCreate](),
	}
	for _, definition := range catalogCommands() {
		if definition.Method == "POST" {
			checkFields(t, definition.Fields, types[definition.Path])
		}
	}
}
