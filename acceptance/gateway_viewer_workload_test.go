package acceptance

import (
	"encoding/json"
	"slices"
	"strings"
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

// Keep the viewer grant and workspace membership through namespace replacement.
// Return the post-recovery checks and remove both grants through their APIs.
func (w *browserGatewayWorkload) startViewerWorkflow(id string, browser *consoleBrowser, ownerGateway string, expected *dynamicpb.Message) func(string) {
	t := w.t
	t.Helper()
	k, call, gatewayClient := w.identity, w.call, w.audience(id)
	ownerAPI := func(method, path string, body []byte) (int, []byte) {
		response := w.owner.api(t, method, path, body)
		return response.StatusCode, response.Body
	}
	viewerAPI := func(method, path string, body []byte) (int, []byte) {
		response := browser.api(t, method, path, body)
		return response.StatusCode, response.Body
	}
	code, body := ownerAPI("GET", "/gateways/"+id, nil)
	var gateway gatewayResponse
	if code != 200 || json.Unmarshal(body, &gateway) != nil || gateway.ID != id {
		t.Fatal("viewer workflow Gateway read failed", code)
	}
	code, body = viewerAPI("GET", "/users/me", nil)
	var recipient httpapi.CurrentUser
	if code != 200 || json.Unmarshal(body, &recipient) != nil || recipient.ID == "" || recipient.Subject == "" {
		t.Fatal("viewer workflow identity read failed", code)
	}
	bobSubject := recipient.Subject
	code, body = ownerAPI("GET", "/roles?search=name%20%3D%20%27gateway%3Aviewer%27", nil)
	var roles httpapi.RoleList
	if code != 200 || json.Unmarshal(body, &roles) != nil || roles.Total != 1 || len(roles.Items) != 1 || roles.Items[0].Name != "gateway:viewer" || roles.Items[0].ID == "" {
		t.Fatal("viewer workflow role read failed", code)
	}
	visibleGateways := func(want []string) {
		t.Helper()
		code, body := viewerAPI("GET", "/gateways", nil)
		var result gatewayListResponse
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
	grant := gateways.GrantRequest{GatewayID: gateway.ID, Scope: "gateway", UserID: recipient.ID, RoleID: roles.Items[0].ID}
	encoded, _ := json.Marshal(grant)
	code, body = ownerAPI("POST", "/role_bindings", encoded)
	var binding struct{ ID string }
	if code != 201 || json.Unmarshal(body, &binding) != nil || binding.ID == "" {
		t.Fatal("grant actual Gateway viewer access", code)
	}
	waitRoles := func(want []string) string {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for {
			raw := k.browserLogin(t, gatewayClient, "console-bob")
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
	if _, err := call("GetProvider", viewer, `{"name":"browser-provider"}`); status.Code(err) != codes.PermissionDenied || !strings.Contains(status.Convert(err).Message(), "not a member") {
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
	ownerWorkspaces := []string{"default", "owner-private"}
	if w.public != nil {
		ownerWorkspaces = append(ownerWorkspaces, "rendered-dashboard")
	}
	if names := workspaceNames(ownerGateway); !slices.Equal(names, ownerWorkspaces) {
		t.Fatal("owner workspace setup", names)
	}
	dashboard := w.newDashboardViewer(id)
	w.checkDashboardViewer(dashboard, false)
	addMember(ownerGateway)
	checkViewer := func(bearer string) {
		t.Helper()
		response, err := call("GetProvider", bearer, `{"name":"browser-provider"}`)
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
			{"GetProvider", `{"name":"browser-provider","workspace":"owner-private"}`},
			{"CreateProvider", `{"provider":{"metadata":{"name":"viewer-denied"},"type":"openai","credentials":{"OPENAI_API_KEY":"test-only"}}}`},
			{"DeleteProvider", `{"name":"browser-provider"}`},
			{"CreateWorkspace", `{"name":"viewer-denied"}`},
			{"GetGatewayInfo", `{}`},
			{"AddWorkspaceMember", strings.Replace(string(member), "WORKSPACE_ROLE_USER", "WORKSPACE_ROLE_ADMIN", 1)},
		} {
			if _, err := call(denied.method, bearer, denied.input); status.Code(err) != codes.PermissionDenied {
				t.Fatal("viewer reached a denied Gateway operation", denied.method, err)
			}
		}
		visibleGateways([]string{gateway.ID})
		if code, _ := viewerAPI("GET", "/gateways/"+gateway.ID, nil); code != 200 {
			t.Fatal("viewer cannot read the Hypershell Gateway", code)
		}
		if code, _ := viewerAPI("PATCH", "/gateways/"+gateway.ID, []byte(`{"name":"viewer-denied"}`)); code != 404 {
			t.Fatal("viewer changed the Hypershell Gateway", code)
		}
		create, _ := json.Marshal(gateways.CreateRequest{Name: "viewer-denied", ClusterID: gateway.ClusterID, ReleaseID: gateway.ReleaseID})
		if code, _ := viewerAPI("POST", "/gateways", create); code != 403 {
			t.Fatal("viewer created a Hypershell Gateway", code)
		}
		w.checkDashboardViewer(dashboard, true)
	}
	checkViewer(viewer)
	return func(owner string) {
		t.Helper()
		viewer = waitRoles([]string{keycloak.RoleUser})
		checkViewer(viewer)
		removeMember(owner)
		if _, err := call("GetProvider", viewer, `{"name":"browser-provider"}`); status.Code(err) != codes.PermissionDenied {
			t.Fatal("removed membership retained access with a current token", err)
		}
		if names := workspaceNames(viewer); len(names) != 0 {
			t.Fatal("removed membership remained in workspace list", names)
		}
		w.checkDashboardViewer(dashboard, false)
		if code, _ := viewerAPI("GET", "/gateways/"+gateway.ID, nil); code != 200 {
			t.Fatal("workspace removal changed the separate Hypershell grant", code)
		}
		addMember(owner)
		if code, _ := ownerAPI("DELETE", "/role_bindings/"+binding.ID, nil); code != 204 {
			t.Fatal("remove Hypershell viewer grant", code)
		}
		if code, _ := viewerAPI("GET", "/gateways/"+gateway.ID, nil); code != 404 {
			t.Fatal("removed viewer grant retained API access", code)
		}
		visibleGateways(nil)
		fresh := waitRoles(nil)
		if _, err := call("GetProvider", fresh, `{"name":"browser-provider"}`); status.Code(err) != codes.PermissionDenied {
			t.Fatal("new token retained removed Gateway role", err)
		}
		if freshDashboard := w.newDashboardViewer(id); freshDashboard != nil {
			if response := freshDashboard.api(t, "GET", "/workspaces/default", nil); response.StatusCode != 403 {
				t.Fatal("new dashboard session retained the removed Gateway role", response.StatusCode)
			}
		}
		// The reference accepts an issued token until expiry. Removing workspace
		// membership must deny that same token without waiting for its expiry.
		if _, err := call("GetProvider", viewer, `{"name":"browser-provider"}`); err != nil {
			t.Fatal("issued token changed before expiry", err)
		}
		removeMember(owner)
		if _, err := call("GetProvider", viewer, `{"name":"browser-provider"}`); status.Code(err) != codes.PermissionDenied {
			t.Fatal("old token bypassed workspace revocation", err)
		}
		if dashboard != nil {
			if response := dashboard.api(t, "GET", "/workspaces/default", nil); response.StatusCode != 403 {
				t.Fatal("existing dashboard session bypassed workspace revocation", response.StatusCode)
			}
		}
		t.Log("Viewer grant and workspace membership survived namespace replacement; filtered lists, denied writes, and both access-removal paths passed")
	}
}
