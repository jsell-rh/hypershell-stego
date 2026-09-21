package grpcapi

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func gatewayMappingRecord() model.Gateway {
	text := func(s string) *string { return &s }
	count := int32(7)
	instant := time.Date(2026, 9, 21, 1, 2, 3, 456, time.FixedZone("offset", 3600))
	return model.Gateway{
		Meta:               model.Meta{ID: "gateway-reference", CreatedTime: instant, UpdatedTime: instant.Add(time.Second)},
		ResourceGeneration: 3, ObservedGenerations: []byte(`{"console":3,"endpoint":3,"workload":3}`),
		Name: "gateway", ClusterID: "cluster-reference", ReleaseID: "release-reference", Namespace: "gateway-namespace",
		ExternalDns: text("gateway.example.test"), TlsMode: text("passthrough"), ServiceType: text("ClusterIP"),
		Status: text("Ready"), Phase: text("Ready"), Image: text("registry.example.test/gateway:release"), SupervisorImage: text("registry.example.test/supervisor:release"),
		ServerDnsNames: []byte(`["second.example.test","first.example.test","second.example.test"]`),
		RouteAddress:   text("https://gateway.example.test"), ConsoleAddress: text("https://console.example.test"),
		Oidc: text(`{"issuer":"https://issuer.example.test"}`), Route: text(`{"host":"gateway.example.test"}`), CredentialDriver: text("postgres"), ActiveSandboxCount: &count,
	}
}

func TestGatewayMappingPreservesEveryField(t *testing.T) {
	for _, presence := range []string{"absent", "empty", "set"} {
		t.Run(presence, func(t *testing.T) {
			row := gatewayMappingRecord()
			fields := []**string{&row.ExternalDns, &row.TlsMode, &row.ServiceType, &row.Status, &row.Phase, &row.Image, &row.SupervisorImage, &row.RouteAddress, &row.ConsoleAddress, &row.Oidc, &row.Route, &row.CredentialDriver}
			names := []string{"second.example.test", "first.example.test", "second.example.test"}
			if presence == "absent" {
				for _, f := range fields {
					*f = nil
				}
				row.ActiveSandboxCount = nil
				row.ServerDnsNames = nil
				names = nil
			}
			if presence == "empty" {
				for _, f := range fields {
					v := ""
					*f = &v
				}
				zero := int32(0)
				row.ActiveSandboxCount = &zero
				row.ServerDnsNames = []byte(`[]`)
				names = []string{}
			}
			want := &pb.Gateway{
				Metadata: &pb.ObjectReference{Id: row.ID, Kind: "Gateway", Href: "/api/hypershell/v1/gateways/" + row.ID, CreatedAt: timestamppb.New(row.CreatedTime), UpdatedAt: timestamppb.New(row.UpdatedTime)},
				Name:     row.Name, ClusterId: row.ClusterID, ReleaseId: row.ReleaseID, Namespace: row.Namespace,
				ExternalDns: row.ExternalDns, TlsMode: row.TlsMode, ServiceType: row.ServiceType, Status: row.Status, Phase: row.Phase, Image: row.Image, SupervisorImage: row.SupervisorImage,
				ServerDnsNames: names, RouteAddress: row.RouteAddress, ConsoleAddress: row.ConsoleAddress, Oidc: row.Oidc, Route: row.Route, CredentialDriver: row.CredentialDriver, ActiveSandboxCount: row.ActiveSandboxCount,
			}
			got, err := present(row)
			if err != nil || !proto.Equal(got, want) {
				t.Fatal("Gateway response differs", got, err)
			}
			if !reflect.DeepEqual(got.ServerDnsNames, names) {
				t.Fatal("list presence differs")
			}
			a, err := protojson.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			b, err := protojson.Marshal(want)
			if err != nil {
				t.Fatal(err)
			}
			var av, bv any
			if json.Unmarshal(a, &av) != nil || json.Unmarshal(b, &bv) != nil || !reflect.DeepEqual(av, bv) {
				t.Fatal("Gateway JSON response differs")
			}
			wire, err := proto.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			decoded := new(pb.Gateway)
			if err := proto.Unmarshal(wire, decoded); err != nil || !proto.Equal(decoded, want) {
				t.Fatal("Gateway wire response differs", err)
			}
		})
	}
}

func TestGatewayMappingSelectsObservationsBeforeConversion(t *testing.T) {
	t.Run("stale", func(t *testing.T) {
		row := gatewayMappingRecord()
		bad := string([]byte{0xff})
		row.ResourceGeneration = 4
		row.Status = &bad
		row.Phase = &bad
		row.RouteAddress = &bad
		row.ConsoleAddress = &bad
		result, err := present(row)
		if err != nil || result.GetStatus() != "ObservationPending" || result.GetPhase() != "Provisioning" || result.GetRouteAddress() != "" || result.GetConsoleAddress() != "" {
			t.Fatal("stale observations were not selected before conversion", err)
		}
		if *row.Status != bad || *row.Phase != bad || *row.RouteAddress != bad || *row.ConsoleAddress != bad {
			t.Fatal("observation selection changed stored fields")
		}
	})
	t.Run("deleting", func(t *testing.T) {
		row := gatewayMappingRecord()
		bad := string([]byte{0xff})
		row.Status = &bad
		row.Phase = &bad
		row.DeletedAt.Valid = true
		row.DeletedAt.Time = row.UpdatedTime
		result, err := present(row)
		if err != nil || result.GetPhase() != "Deleting" || result.GetStatus() != "Gateway cleanup is in progress" {
			t.Fatal("deletion policy was lost", err)
		}
	})
	t.Run("current invalid", func(t *testing.T) {
		row := gatewayMappingRecord()
		bad := string([]byte{0xff})
		row.Status = &bad
		result, err := present(row)
		requirePrivateTimestampError(t, result, mapError(err))
	})
}

func TestGatewayMappingRejectsInvalidDNSListsPrivately(t *testing.T) {
	cases := map[string]string{
		"malformed": "[", "object": "{}", "null member": "[null]", "number member": "[17]", "nested": "[[]]", "trailing": "[] null",
		"invalid UTF8": string([]byte{'[', '"', 0xff, '"', ']'}), "surrogate": `["\uD800"]`,
		"input bytes": strings.Repeat(" ", 262143) + "[]", "item bytes": `["` + strings.Repeat("x", 254) + `"]`,
	}
	names := make([]string, 129)
	for i := range names {
		names[i] = "gateway.example.test"
	}
	raw, err := json.Marshal(names)
	if err != nil {
		t.Fatal(err)
	}
	cases["item count"] = string(raw)
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			row := gatewayMappingRecord()
			row.ServerDnsNames = []byte(raw)
			result, err := present(row)
			requirePrivateTimestampError(t, result, mapError(err))
		})
	}
}

func TestGatewayMappingAcceptsDNSListBounds(t *testing.T) {
	// Escaped text checks the encoded bound as well as the decoded byte bound.
	// DNS policy remains in the application; this check covers field conversion.
	names := make([]string, 128)
	for i := range names {
		names[i] = strings.Repeat("<", 253)
	}
	raw, err := json.Marshal(names)
	if err != nil {
		t.Fatal(err)
	}
	row := gatewayMappingRecord()
	row.ServerDnsNames = raw
	result, err := present(row)
	if err != nil || !reflect.DeepEqual(result.ServerDnsNames, names) {
		t.Fatal("valid list bounds were rejected", err)
	}
	row.ServerDnsNames = []byte(`null`)
	result, err = present(row)
	if err != nil || result.ServerDnsNames != nil {
		t.Fatal("null list became present", err)
	}
}
