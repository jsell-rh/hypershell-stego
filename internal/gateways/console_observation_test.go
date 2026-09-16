package gateways

import (
	"errors"
	"testing"

	auth "github.com/jsell-rh/hypershell-stego/out/auth"
)

func TestConsoleObservationRequiresEveryExactGrant(t *testing.T) {
	p := Principal{Issuer: "https://issuer.example", Subject: "worker", Username: "worker"}
	phase, state, route, console := "Running", "Healthy", "https://gateway.example", "https://console.example"
	operations := []string{"observe.workload", "observe.endpoint", "configure.console"}
	for missing := -1; missing < len(operations); missing++ {
		grants := []auth.Grant{}
		for index, operation := range operations {
			if index != missing {
				grants = append(grants, auth.Grant{Issuer: p.Issuer, Subject: p.Subject, Resource: "Gateway", Operation: operation, Target: "cluster"})
			}
		}
		policy, err := auth.NewGrantPolicy(grants)
		if err != nil {
			t.Fatal(err)
		}
		s := &Service{controlPlaneSubjects: map[string]bool{p.Subject: true}, controllerWritePolicy: policy}
		patch := PatchRequest{Phase: &phase, Status: &state}
		err = s.authorizeControllerWrite(p, "cluster", patch, &console, &route)
		if missing < 0 && err != nil || missing >= 0 && !errors.Is(err, ErrForbidden) {
			t.Fatal("combined observation did not require every grant", missing, err)
		}
		if err := s.authorizeControllerWrite(p, "another-cluster", patch, &console, &route); !errors.Is(err, ErrForbidden) {
			t.Fatal("observation crossed its assigned cluster", err)
		}
		if err := s.authorizeControllerWrite(p, "cluster", PatchRequest{}, &console, &route); !errors.Is(err, ErrInvalid) {
			t.Fatal("combined endpoints omitted the workload observation", err)
		}
	}
}
