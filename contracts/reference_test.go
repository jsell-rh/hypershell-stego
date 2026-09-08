package contracts

import (
	"context"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func loadReference(t *testing.T) *Reference {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	reference, err := Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return reference
}

func TestReferenceInventory(t *testing.T) {
	inventory := loadReference(t).Inventory()
	if len(inventory.REST) != 37 || len(inventory.GRPC) != 41 {
		t.Fatalf("contract coverage changed: REST=%d, gRPC=%d", len(inventory.REST), len(inventory.GRPC))
	}
	streams := 0
	seen := make(map[string]bool)
	for _, rpc := range inventory.GRPC {
		if seen[rpc.Name] {
			t.Errorf("duplicate RPC %s", rpc.Name)
		}
		seen[rpc.Name] = true
		if rpc.ServerStreaming {
			streams++
		}
		if rpc.ClientStreaming {
			t.Errorf("unexpected client stream %s", rpc.Name)
		}
	}
	if streams != 6 {
		t.Fatalf("watch stream coverage changed: %d", streams)
	}
	for _, method := range []string{
		"hypershell.v1.GatewayService/AdjustActiveSandboxCount",
		"hypershell.v1.GatewayService/SetActiveSandboxCount",
		"hypershell.v1.RoleBindingService/ListRoleBindings",
		"hypershell.provisioner.v1.OpenShellGatewayServiceAccountProvisionerService/Reconcile",
	} {
		if !seen[method] {
			t.Errorf("missing application operation %s", method)
		}
	}
}

func TestGatewayFieldOwnershipAndWireNumbers(t *testing.T) {
	reference := loadReference(t)
	file := reference.Proto.FindFileByPath("hypershell/v1/gateways.proto")
	if file == nil {
		t.Fatal("gateway protobuf contract is missing")
	}
	message := file.Messages().ByName("Gateway")
	if message == nil {
		t.Fatal("Gateway message is missing")
	}
	for name, number := range map[protoreflect.Name]protoreflect.FieldNumber{
		"namespace": 7, "supervisor_image": 19, "credential_driver": 20, "active_sandbox_count": 21, "console_address": 22,
	} {
		field := message.Fields().ByName(name)
		if field == nil || field.Number() != number {
			t.Errorf("wire number changed for %s", name)
		}
	}
	if !message.ReservedRanges().Has(3) || !message.ReservedNames().Has("fleet_id") {
		t.Fatal("retired gateway field is not reserved")
	}
	update := file.Messages().ByName("UpdateGatewayRequest")
	if update.Fields().ByName("active_sandbox_count") != nil {
		t.Fatal("general updates can overwrite the control-plane count")
	}
	collection := reference.OpenAPI.Paths.Value("/api/hypershell/v1/gateways")
	item := reference.OpenAPI.Paths.Value("/api/hypershell/v1/gateways/{id}")
	for name, operation := range map[string]*openapi3.Operation{"create": collection.Post, "patch": item.Patch} {
		schema := operation.RequestBody.Value.Content["application/json"].Schema
		for _, field := range []string{"namespace", "active_sandbox_count"} {
			if findProperty(schema.Value, field) != nil {
				t.Errorf("%s exposes server-owned field %s", name, field)
			}
		}
	}
	response := item.Get.Responses.Value("200").Value.Content["application/json"].Schema.Value
	for _, field := range []string{"namespace", "active_sandbox_count"} {
		property := findProperty(response, field)
		if property == nil || !property.ReadOnly {
			t.Errorf("response field %s is not read-only", field)
		}
	}
}

func TestServiceAccountCredentialIsCreateOnly(t *testing.T) {
	document := loadReference(t).OpenAPI
	list := document.Paths.Value("/api/hypershell/v1/gateways/{gateway_id}/service_accounts")
	get := document.Paths.Value("/api/hypershell/v1/gateways/{gateway_id}/service_accounts/{service_account_id}")
	createResponse := list.Post.Responses.Value("201").Value
	for _, header := range []string{"Cache-Control", "Pragma"} {
		if createResponse.Headers[header] == nil {
			t.Errorf("one-time credential response lacks %s", header)
		}
	}
	create := createResponse.Content["application/json"].Schema.Value
	if !containsProperty(create, "client_secret", make(map[*openapi3.Schema]bool)) {
		t.Fatal("create response lacks the one-time credential")
	}
	for name, response := range map[string]*openapi3.Response{
		"list": list.Get.Responses.Value("200").Value,
		"get":  get.Get.Responses.Value("200").Value,
	} {
		schema := response.Content["application/json"].Schema.Value
		if containsProperty(schema, "client_secret", make(map[*openapi3.Schema]bool)) {
			t.Errorf("%s response can expose a client secret", name)
		}
	}
	if get.Delete.Responses.Value("202") == nil || get.Delete.Responses.Value("204") == nil {
		t.Fatal("delete contract lacks pending and complete outcomes")
	}
}

func findProperty(schema *openapi3.Schema, name string) *openapi3.Schema {
	if property := schema.Properties[name]; property != nil {
		return property.Value
	}
	for _, item := range schema.AllOf {
		if property := findProperty(item.Value, name); property != nil {
			return property
		}
	}
	return nil
}

func containsProperty(schema *openapi3.Schema, name string, seen map[*openapi3.Schema]bool) bool {
	if schema == nil || seen[schema] {
		return false
	}
	seen[schema] = true
	if schema.Properties[name] != nil {
		return true
	}
	for _, property := range schema.Properties {
		if containsProperty(property.Value, name, seen) {
			return true
		}
	}
	for _, group := range []openapi3.SchemaRefs{schema.AllOf, schema.OneOf, schema.AnyOf} {
		for _, item := range group {
			if containsProperty(item.Value, name, seen) {
				return true
			}
		}
	}
	return schema.Items != nil && containsProperty(schema.Items.Value, name, seen)
}
