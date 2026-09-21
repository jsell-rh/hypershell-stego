package httpapi

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/out/application/responses"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
)

func TestRESTCatalogMappingsPreserveEveryField(t *testing.T) {
	created := time.Date(2026, 9, 21, 1, 2, 3, 456, time.FixedZone("offset", 3600))
	meta := model.Meta{ID: "catalog-reference", CreatedTime: created, UpdatedTime: created.Add(5 * time.Second)}
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
			var percent *int32
			if presence != "absent" {
				value := int32(25)
				if presence == "empty" {
					value = 0
				}
				percent = &value
			}
			cluster := model.ManagedCluster{Meta: meta, Name: "cluster", Provider: "openshift", Region: text("region"), KubeconfigSecret: "cluster-secret", Status: text("cluster-ready"), ApiServerUrl: text("https://api.example.test")}
			release := model.GatewayRelease{Meta: meta, Name: "release", Image: "registry.example.test/gateway:release", RolloutStrategy: text("canary"), CanaryPercent: percent, CanaryDuration: text("5m"), Status: text("release-ready")}
			network := model.GatewayNetwork{Meta: meta, Name: "network", Topology: text("mesh"), TunnelMode: text("wireguard"), HubGatewayID: text("hub"), Status: text("network-ready")}
			for _, item := range []struct {
				kind, collection   string
				present            func() (any, error)
				required, optional map[string]any
			}{
				{"ManagedCluster", "managed_clusters", func() (any, error) { return presentManagedCluster(cluster) }, map[string]any{"name": "cluster", "provider": "openshift", "kubeconfig_secret": "cluster-secret"}, map[string]any{"region": "region", "status": "cluster-ready", "api_server_url": "https://api.example.test"}},
				{"GatewayRelease", "gateway_releases", func() (any, error) { return presentGatewayRelease(release) }, map[string]any{"name": "release", "image": "registry.example.test/gateway:release"}, map[string]any{"rollout_strategy": "canary", "canary_percent": float64(25), "canary_duration": "5m", "status": "release-ready"}},
				{"GatewayNetwork", "gateway_networks", func() (any, error) { return presentGatewayNetwork(network) }, map[string]any{"name": "network"}, map[string]any{"topology": "mesh", "tunnel_mode": "wireguard", "hub_gateway_id": "hub", "status": "network-ready"}},
			} {
				t.Run(item.kind, func(t *testing.T) {
					value, err := item.present()
					if err != nil {
						t.Fatal(err)
					}
					data, err := json.Marshal(value)
					if err != nil {
						t.Fatal(err)
					}
					var got map[string]any
					if err := json.Unmarshal(data, &got); err != nil {
						t.Fatal(err)
					}
					want := map[string]any{"id": "catalog-reference", "kind": item.kind, "href": "/api/hypershell/v1/" + item.collection + "/catalog-reference", "created_at": "2026-09-21T01:02:03.000000456+01:00", "updated_at": "2026-09-21T01:02:08.000000456+01:00"}
					for name, value := range item.required {
						want[name] = value
					}
					if presence != "absent" {
						for name, value := range item.optional {
							if presence == "empty" {
								if _, number := value.(float64); number {
									value = float64(0)
								} else {
									value = ""
								}
							}
							want[name] = value
						}
					}
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("REST properties changed: got %#v; want %#v", got, want)
					}
				})
			}
		})
	}
}

func TestRESTCatalogMappingsOwnPointers(t *testing.T) {
	shared := "original"
	percent := int32(0)
	cluster, err := presentManagedCluster(model.ManagedCluster{Region: &shared, Status: &shared, ApiServerUrl: &shared})
	if err != nil {
		t.Fatal(err)
	}
	release, err := presentGatewayRelease(model.GatewayRelease{RolloutStrategy: &shared, CanaryDuration: &shared, Status: &shared, CanaryPercent: &percent})
	if err != nil {
		t.Fatal(err)
	}
	network, err := presentGatewayNetwork(model.GatewayNetwork{Topology: &shared, TunnelMode: &shared, HubGatewayID: &shared, Status: &shared})
	if err != nil {
		t.Fatal(err)
	}
	fields := []*string{cluster.Region, cluster.Status, cluster.ApiServerUrl, release.RolloutStrategy, release.CanaryDuration, release.Status, network.Topology, network.TunnelMode, network.HubGatewayId, network.Status}
	seen := map[*string]bool{}
	for _, field := range fields {
		if field == nil || field == &shared || seen[field] || *field != "original" {
			t.Fatal("response string pointer is absent or shared")
		}
		seen[field] = true
		*field = "response"
	}
	if shared != "original" || release.CanaryPercent == nil || release.CanaryPercent == &percent {
		t.Fatal("response changed source storage")
	}
	*release.CanaryPercent = 10
	shared, percent = "source", 20
	for _, field := range fields {
		if *field != "response" {
			t.Fatal("source changed response storage")
		}
	}
	if *release.CanaryPercent != 10 {
		t.Fatal("source changed response number")
	}
}

func TestRESTCatalogMappingsRejectInvalidValues(t *testing.T) {
	bad := string([]byte{255})
	badTime := time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	for name, present := range map[string]func() (any, error){
		"cluster required": func() (any, error) { return presentManagedCluster(model.ManagedCluster{Name: bad}) },
		"cluster optional": func() (any, error) { return presentManagedCluster(model.ManagedCluster{Region: &bad}) },
		"release required": func() (any, error) { return presentGatewayRelease(model.GatewayRelease{Image: bad}) },
		"release optional": func() (any, error) { return presentGatewayRelease(model.GatewayRelease{CanaryDuration: &bad}) },
		"network required": func() (any, error) { return presentGatewayNetwork(model.GatewayNetwork{Name: bad}) },
		"network optional": func() (any, error) { return presentGatewayNetwork(model.GatewayNetwork{HubGatewayID: &bad}) },
		"cluster created": func() (any, error) {
			return presentManagedCluster(model.ManagedCluster{Meta: model.Meta{CreatedTime: badTime}})
		},
		"release updated": func() (any, error) {
			return presentGatewayRelease(model.GatewayRelease{Meta: model.Meta{UpdatedTime: badTime}})
		},
		"network offset": func() (any, error) {
			return presentGatewayNetwork(model.GatewayNetwork{Meta: model.Meta{CreatedTime: time.Now().In(time.FixedZone("seconds", 30))}})
		},
	} {
		t.Run(name, func(t *testing.T) {
			value, err := present()
			if err != responses.ErrConversion || (value != nil && !reflect.ValueOf(value).IsNil()) {
				t.Fatal("invalid value returned a response or changed its error")
			}
			if err.Error() != "response conversion failed" {
				t.Fatal("conversion error contains a supplied value")
			}
		})
	}
}

func TestRESTCatalogMappingsPreserveEmptyReference(t *testing.T) {
	for kind, present := range map[string]func() (any, error){
		"ManagedCluster": func() (any, error) { return presentManagedCluster(model.ManagedCluster{}) },
		"GatewayRelease": func() (any, error) { return presentGatewayRelease(model.GatewayRelease{}) },
		"GatewayNetwork": func() (any, error) { return presentGatewayNetwork(model.GatewayNetwork{}) },
	} {
		t.Run(kind, func(t *testing.T) {
			value, err := present()
			if err != nil {
				t.Fatal(err)
			}
			body, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			if err := json.Unmarshal(body, &fields); err != nil {
				t.Fatal(err)
			}
			if _, present := fields["id"]; present {
				t.Fatal("empty ID must retain its prior omission")
			}
			if fields["kind"] != kind || fields["name"] != "" || fields["created_at"] != "0001-01-01T00:00:00Z" || fields["updated_at"] != "0001-01-01T00:00:00Z" {
				t.Fatal("empty reference presence changed")
			}
		})
	}
}
