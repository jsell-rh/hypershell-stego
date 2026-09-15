package gateways

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	transport "github.com/jsell-rh/hypershell-stego/out/application/transport"
	auth "github.com/jsell-rh/hypershell-stego/out/auth"
)

func TestEndpointOwnerWritesStopBeforeStorage(t *testing.T) {
	for _, value := range []string{`null`, `""`, `"https://gateway.example"`} {
		request := httptest.NewRequest("PATCH", "/", strings.NewReader(`{"route_address":`+value+`}`))
		request.Header.Set("Content-Type", "application/json")
		if _, err := transport.JSONBody[PatchRequest](request); !errors.Is(err, transport.ErrRequest) {
			t.Fatal("REST decoder accepted the controller field", err)
		}
	}
	request := httptest.NewRequest("PATCH", "/", strings.NewReader(`{"name":"allowed"}`))
	request.Header.Set("Content-Type", "application/json")
	if _, err := transport.JSONBody[PatchRequest](request); err != nil {
		t.Fatal("REST decoder rejected an owner field", err)
	}
}

func TestEndpointControllerRequiresExactGrantAndOneFieldGroup(t *testing.T) {
	caller := Principal{Issuer: "https://issuer.example", Subject: "worker", Username: "worker"}
	policy, err := auth.NewGrantPolicy([]auth.Grant{{Issuer: caller.Issuer, Subject: caller.Subject, Resource: "Gateway", Operation: "observe.endpoint", Target: "cluster-a"}})
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{controlPlaneSubjects: map[string]bool{caller.Subject: true}, controllerWritePolicy: policy}
	endpoint, phase, workload := "https://gateway.example", "Running", "Healthy"
	for _, value := range []string{"", endpoint} {
		patch := PatchRequest{}
		if err := service.authorizeControllerWrite(caller, "cluster-a", patch, nil, &value); err != nil {
			t.Fatal("exact endpoint grant was denied", err)
		}
		if err := service.authorizeControllerWrite(caller, "cluster-b", patch, nil, &value); !errors.Is(err, ErrForbidden) {
			t.Fatal("endpoint grant crossed clusters", err)
		}
	}
	for _, patch := range []PatchRequest{
		{Name: &endpoint},
		{Phase: &phase, Status: &workload},
		{OIDC: &endpoint},
	} {
		if err := service.authorizeControllerWrite(caller, "cluster-a", patch, nil, &endpoint); !errors.Is(err, ErrInvalid) {
			t.Fatal("mixed endpoint patch was accepted", err)
		}
	}
	if err := service.authorizeControllerWrite(caller, "cluster-a", PatchRequest{}, &endpoint, &endpoint); !errors.Is(err, ErrInvalid) {
		t.Fatal("mixed console and endpoint patch was accepted", err)
	}
	if err := service.authorizeControllerWrite(caller, "cluster-a", PatchRequest{Phase: &phase, Status: &workload}, nil, nil); !errors.Is(err, ErrForbidden) {
		t.Fatal("endpoint grant permitted workload observations", err)
	}
}
