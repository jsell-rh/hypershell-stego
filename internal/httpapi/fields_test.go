package httpapi

import (
	"reflect"
	"strings"
	"testing"
)

// A new public response field must be reviewed in the projection declaration.
func TestListProjectionCoversPublicResponseFields(t *testing.T) {
	if listFieldError != nil {
		t.Fatal(listFieldError)
	}
	for entity, typ := range map[string]reflect.Type{
		"Gateway": reflect.TypeFor[Gateway](), "GatewayNetwork": reflect.TypeFor[GatewayNetwork](),
		"ManagedCluster": reflect.TypeFor[ManagedCluster](), "ManagedDatabase": reflect.TypeFor[ManagedDatabase](),
		"GatewayRelease": reflect.TypeFor[GatewayRelease](), "Role": reflect.TypeFor[Role](), "RoleBinding": reflect.TypeFor[grantItem](),
	} {
		var check func(reflect.Type)
		check = func(typ reflect.Type) {
			for i := 0; i < typ.NumField(); i++ {
				field := typ.Field(i)
				if field.Anonymous {
					check(field.Type)
					continue
				}
				name := strings.Split(field.Tag.Get("json"), ",")[0]
				if name == "" || name == "-" {
					continue
				}
				if _, err := listFieldProjectors[entity].Parse(name); err != nil {
					t.Errorf("%s response field %s is absent from its projection: %v", entity, name, err)
				}
			}
		}
		check(typ)
	}
}
