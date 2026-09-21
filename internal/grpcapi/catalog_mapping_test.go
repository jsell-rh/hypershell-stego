package grpcapi

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestCatalogMappingsPreserveEveryField(t *testing.T) {
	created := time.Date(2026, 9, 21, 1, 2, 3, 456, time.FixedZone("offset", 3600))
	updated := created.Add(5 * time.Second)
	meta := model.Meta{ID: "catalog-reference", CreatedTime: created, UpdatedTime: updated}
	reference := func(kind, collection string) *pb.ObjectReference {
		return &pb.ObjectReference{Id: meta.ID, CreatedAt: timestamppb.New(created), UpdatedAt: timestamppb.New(updated), Kind: kind, Href: "/api/hypershell/v1/" + collection + "/" + meta.ID}
	}
	for _, presence := range []string{"absent", "empty", "set"} {
		t.Run(presence, func(t *testing.T) {
			text := func(value string) *string {
				if presence == "absent" {
					return nil
				}
				if presence == "empty" {
					value = ""
				}
				return &value
			}
			number := func(value int32) *int32 {
				if presence == "absent" {
					return nil
				}
				if presence == "empty" {
					value = 0
				}
				return &value
			}
			cluster := model.ManagedCluster{Meta: meta, Name: "cluster", Provider: "openshift", Region: text("region"), KubeconfigSecret: "cluster-secret", Status: text("cluster-ready"), ApiServerUrl: text("https://api.example.test")}
			release := model.GatewayRelease{Meta: meta, Name: "release", Image: "registry.example.test/gateway:release", RolloutStrategy: text("canary"), CanaryPercent: number(25), CanaryDuration: text("5m"), Status: text("release-ready")}
			network := model.GatewayNetwork{Meta: meta, Name: "network", Topology: text("mesh"), TunnelMode: text("wireguard"), HubGatewayID: text("hub"), Status: text("network-ready")}
			cases := []struct {
				name    string
				present func() (proto.Message, error)
				want    proto.Message
			}{
				{"ManagedCluster", func() (proto.Message, error) { return presentManagedCluster(cluster) }, &pb.ManagedCluster{Metadata: reference("ManagedCluster", "managed_clusters"), Name: cluster.Name, Provider: cluster.Provider, Region: cluster.Region, KubeconfigSecret: cluster.KubeconfigSecret, Status: cluster.Status, ApiServerUrl: cluster.ApiServerUrl}},
				{"GatewayRelease", func() (proto.Message, error) { return presentGatewayRelease(release) }, &pb.GatewayRelease{Metadata: reference("GatewayRelease", "gateway_releases"), Name: release.Name, Image: release.Image, RolloutStrategy: release.RolloutStrategy, CanaryPercent: release.CanaryPercent, CanaryDuration: release.CanaryDuration, Status: release.Status}},
				{"GatewayNetwork", func() (proto.Message, error) { return presentGatewayNetwork(network) }, &pb.GatewayNetwork{Metadata: reference("GatewayNetwork", "gateway_networks"), Name: network.Name, Topology: network.Topology, TunnelMode: network.TunnelMode, HubGatewayId: network.HubGatewayID, Status: network.Status}},
			}
			for _, item := range cases {
				t.Run(item.name, func(t *testing.T) {
					actual, err := item.present()
					if err != nil {
						t.Fatal(err)
					}
					if !proto.Equal(actual, item.want) {
						t.Fatal("generated mapping changed the response", actual, item.want)
					}
					gotJSON, err := protojson.Marshal(actual)
					if err != nil {
						t.Fatal(err)
					}
					wantJSON, err := protojson.Marshal(item.want)
					if err != nil {
						t.Fatal(err)
					}
					var gotValue, wantValue any
					if err := json.Unmarshal(gotJSON, &gotValue); err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal(wantJSON, &wantValue); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(gotValue, wantValue) {
						t.Fatal("protobuf JSON changed")
					}
					wire, err := proto.Marshal(actual)
					if err != nil {
						t.Fatal(err)
					}
					decoded := actual.ProtoReflect().New().Interface()
					if err := proto.Unmarshal(wire, decoded); err != nil || !proto.Equal(decoded, item.want) {
						t.Fatal("protobuf wire response changed", err)
					}
				})
			}
		})
	}
}

func TestCatalogMappingsRejectInvalidTextPrivately(t *testing.T) {
	invalid := string([]byte{0xff})
	cases := map[string]func() (proto.Message, error){
		"cluster name":     func() (proto.Message, error) { return presentManagedCluster(model.ManagedCluster{Name: invalid}) },
		"cluster optional": func() (proto.Message, error) { return presentManagedCluster(model.ManagedCluster{Region: &invalid}) },
		"release name":     func() (proto.Message, error) { return presentGatewayRelease(model.GatewayRelease{Name: invalid}) },
		"release optional": func() (proto.Message, error) { return presentGatewayRelease(model.GatewayRelease{Status: &invalid}) },
		"network name":     func() (proto.Message, error) { return presentGatewayNetwork(model.GatewayNetwork{Name: invalid}) },
		"network optional": func() (proto.Message, error) { return presentGatewayNetwork(model.GatewayNetwork{Topology: &invalid}) },
	}
	for name, present := range cases {
		t.Run(name, func(t *testing.T) {
			response, err := present()
			if err == nil {
				t.Fatal("invalid stored text was accepted")
			}
			requirePrivateTimestampError(t, response, mapError(err))
		})
	}
}
