package acceptance

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	keycloak "github.com/jsell-rh/hypershell-stego/internal/serviceaccountkeycloak"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/dynamicpb"
)

type gatewayCall func(method, bearer, input string) (*dynamicpb.Message, error)

// A Hypershell grant and a Gateway workspace membership have separate owners.
// Return a check that the caller runs after the actual workload restarts.
func startGatewayViewerWorkflow(t *testing.T, k *keycloakFixture, root string, gateway httpapi.Gateway, gatewayClient, ownerAPI, ownerGateway, bobSubject string, expected *dynamicpb.Message, call gatewayCall) func(string) {
	t.Helper()
	bobAPI := k.browserLogin(t, "hypershell", "bob")
	recipient := currentUser(t, root, bobAPI)
	visibleGateways := func(want []string) {
		t.Helper()
		code, body := requestJSON(t, "GET", root+"/gateways", bobAPI, nil)
		var result httpapi.GatewayList
		if code != 200 || json.Unmarshal(body, &result) != nil {
			t.Fatal("list viewer Gateways", code)
		}
		var ids []string
		for _, item := range result.Items {
			ids = append(ids, item.ID)
		}
		if !slices.Equal(ids, want) || result.Total != int64(len(want)) {
			t.Fatal("Gateway list did not follow the viewer grant", ids, result.Total)
		}
	}
	visibleGateways(nil)
	grant := gateways.GrantRequest{GatewayID: gateway.ID, Scope: "gateway", UserID: recipient.ID, RoleID: discoverRole(t, root, ownerAPI, "gateway:viewer").ID}
	encoded, _ := json.Marshal(grant)
	code, body := requestJSON(t, "POST", root+"/role_bindings", ownerAPI, encoded)
	var binding struct{ ID string }
	if code != 201 || json.Unmarshal(body, &binding) != nil || binding.ID == "" {
		t.Fatal("grant actual Gateway viewer access", code, string(body))
	}
	waitRoles := func(want []string) string {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for {
			raw := k.browserLogin(t, gatewayClient, "bob")
			response, err := call("GetCurrentUser", raw, `{}`)
			if err != nil {
				t.Fatal("Gateway viewer identity", err)
			}
			document, err := protojson.Marshal(response)
			if err != nil {
				t.Fatal(err)
			}
			var identity struct {
				Subject string
				Roles   []string
			}
			if json.Unmarshal(document, &identity) != nil || identity.Subject != bobSubject {
				t.Fatal("Gateway viewer subject changed")
			}
			if equalStringSet(identity.Roles, want) {
				return raw
			}
			if time.Now().After(deadline) {
				t.Fatalf("actual Gateway viewer roles did not converge: %v", identity.Roles)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	visibleGateways([]string{gateway.ID})
	viewer := waitRoles([]string{keycloak.RoleUser})
	if _, err := call("GetProvider", viewer, `{"name":"stored-provider"}`); status.Code(err) != codes.PermissionDenied || !strings.Contains(status.Convert(err).Message(), "not a member") {
		t.Fatal("Gateway role alone granted workspace access", err)
	}
	member, _ := json.Marshal(map[string]any{"workspace": "default", "principal_subject": bobSubject, "role": "WORKSPACE_ROLE_USER"})
	addMember := func(owner string) {
		t.Helper()
		if _, err := call("AddWorkspaceMember", owner, string(member)); err != nil {
			t.Fatal("grant separate workspace membership", err)
		}
	}
	removeMember := func(owner string) {
		t.Helper()
		input, _ := json.Marshal(map[string]string{"workspace": "default", "principal_subject": bobSubject})
		response, err := call("RemoveWorkspaceMember", owner, string(input))
		if err != nil {
			t.Fatal("remove separate workspace membership", err)
		}
		if !response.Get(response.Descriptor().Fields().ByName("removed")).Bool() {
			t.Fatal("workspace membership was not removed")
		}
	}
	workspaceNames := func(bearer string) []string {
		t.Helper()
		response, err := call("ListWorkspaces", bearer, `{}`)
		if err != nil {
			t.Fatal("list Gateway workspaces", err)
		}
		document, err := protojson.Marshal(response)
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Workspaces []struct{ Metadata struct{ Name string } }
		}
		if json.Unmarshal(document, &result) != nil {
			t.Fatal("invalid Gateway workspace list")
		}
		var names []string
		for _, workspace := range result.Workspaces {
			names = append(names, workspace.Metadata.Name)
		}
		slices.Sort(names)
		return names
	}
	if names := workspaceNames(viewer); len(names) != 0 {
		t.Fatal("viewer saw workspaces without membership", names)
	}
	if _, err := call("CreateWorkspace", ownerGateway, `{"name":"owner-private"}`); err != nil {
		t.Fatal("create owner-only workspace", err)
	}
	if names := workspaceNames(ownerGateway); !slices.Equal(names, []string{"default", "owner-private"}) {
		t.Fatal("owner workspace setup", names)
	}
	addMember(ownerGateway)
	checkViewer := func(bearer string) {
		t.Helper()
		response, err := call("GetProvider", bearer, `{"name":"stored-provider"}`)
		if err != nil || !proto.Equal(response, expected) {
			t.Fatal("viewer could not read the stored provider", err)
		}
		document, err := protojson.Marshal(response)
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Provider struct {
				Credentials map[string]string
				Handles     map[string]json.RawMessage `json:"credentialHandles"`
			}
		}
		if json.Unmarshal(document, &result) != nil || len(result.Provider.Credentials) != 1 || result.Provider.Credentials["OPENAI_API_KEY"] != "REDACTED" || len(result.Provider.Handles) != 0 {
			t.Fatal("provider response exposed credential material")
		}

		if names := workspaceNames(bearer); !slices.Equal(names, []string{"default"}) {
			t.Fatal("workspace list did not filter viewer access", names)
		}
		for _, denied := range []struct{ method, input string }{
			{"GetProvider", `{"name":"stored-provider","workspace":"owner-private"}`},
			{"CreateProvider", `{"provider":{"metadata":{"name":"viewer-denied"},"type":"openai","credentials":{"OPENAI_API_KEY":"test-only"}}}`},
			{"DeleteProvider", `{"name":"stored-provider"}`},
			{"CreateWorkspace", `{"name":"viewer-denied"}`},
			{"GetGatewayInfo", `{}`},
			{"AddWorkspaceMember", strings.Replace(string(member), "WORKSPACE_ROLE_USER", "WORKSPACE_ROLE_ADMIN", 1)},
		} {
			if _, err := call(denied.method, bearer, denied.input); status.Code(err) != codes.PermissionDenied {
				t.Fatal("viewer reached a denied Gateway operation", denied.method, err)
			}
		}
	}
	checkViewer(viewer)
	if code, _ := requestJSON(t, "GET", root+"/gateways/"+gateway.ID, bobAPI, nil); code != 200 {
		t.Fatal("viewer cannot read the Hypershell Gateway", code)
	}
	if code, _ := requestJSON(t, "PATCH", root+"/gateways/"+gateway.ID, bobAPI, []byte(`{"name":"viewer-denied"}`)); code != 404 {
		t.Fatal("viewer changed the Hypershell Gateway", code)
	}
	create, _ := json.Marshal(gateways.CreateRequest{Name: "viewer-denied", ClusterID: gateway.ClusterID, ReleaseID: gateway.ReleaseID})
	if code, _ := requestJSON(t, "POST", root+"/gateways", bobAPI, create); code != 403 {
		t.Fatal("viewer created a Hypershell Gateway", code)
	}
	return func(owner string) {
		t.Helper()
		viewer = waitRoles([]string{keycloak.RoleUser})
		checkViewer(viewer)
		removeMember(owner)
		if _, err := call("GetProvider", viewer, `{"name":"stored-provider"}`); status.Code(err) != codes.PermissionDenied {
			t.Fatal("removed membership retained access with a current token", err)
		}
		if names := workspaceNames(viewer); len(names) != 0 {
			t.Fatal("removed membership remained in workspace list", names)
		}
		if code, _ := requestJSON(t, "GET", root+"/gateways/"+gateway.ID, bobAPI, nil); code != 200 {
			t.Fatal("workspace removal changed the separate Hypershell grant", code)
		}
		addMember(owner)
		if code, _ := requestJSON(t, "DELETE", root+"/role_bindings/"+binding.ID, ownerAPI, nil); code != 204 {
			t.Fatal("remove Hypershell viewer grant", code)
		}
		if code, _ := requestJSON(t, "GET", root+"/gateways/"+gateway.ID, bobAPI, nil); code != 404 {
			t.Fatal("removed viewer grant retained API access", code)
		}
		visibleGateways(nil)
		fresh := waitRoles(nil)
		if _, err := call("GetProvider", fresh, `{"name":"stored-provider"}`); status.Code(err) != codes.PermissionDenied {
			t.Fatal("new token retained removed Gateway role", err)
		}
		// The reference accepts an issued token until expiry. Removing workspace
		// membership must deny that same token without waiting for its expiry.
		if _, err := call("GetProvider", viewer, `{"name":"stored-provider"}`); err != nil {
			t.Fatal("issued token changed before expiry", err)
		}
		removeMember(owner)
		if _, err := call("GetProvider", viewer, `{"name":"stored-provider"}`); status.Code(err) != codes.PermissionDenied {
			t.Fatal("old token bypassed workspace revocation", err)
		}
		t.Log("Viewer grant, separate workspace membership, filtered workspaces, denied writes, restart, and both access-removal paths passed")
	}
}
