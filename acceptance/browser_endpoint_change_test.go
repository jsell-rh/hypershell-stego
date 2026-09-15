package acceptance

import (
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

type browserEndpointChange struct {
	Endpoint     string `json:"endpoint"`
	Initial      string `json:"initial"`
	Replacement  string `json:"replacement"`
	Nonce        string `json:"nonce"`
	Namespace    string `json:"namespace"`
	NamespaceUID string `json:"namespace_uid"`
}

func parseEndpointChange(raw, control string) (*browserEndpointChange, error) {
	if raw == "" {
		return nil, nil
	}
	if len(raw) > 2048 || !regexp.MustCompile(`^stego-service-[0-9]{8}-[0-9a-f]{6}$`).MatchString(control) {
		return nil, fmt.Errorf("endpoint change requires a bounded direct fixture")
	}
	var value browserEndpointChange
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("endpoint fixture input is invalid")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("endpoint fixture has trailing data")
	}
	if value.Endpoint != "network-probe" || value.Namespace != control+"-peer" || value.NamespaceUID == "" ||
		!regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(value.Nonce) || value.Initial == value.Replacement {
		return nil, fmt.Errorf("endpoint fixture identity differs")
	}
	for _, raw := range []string{value.Initial, value.Replacement} {
		endpoint, err := netip.ParseAddrPort(raw)
		if err != nil || endpoint.Port() != 8080 || endpoint.String() != raw || !endpoint.Addr().IsGlobalUnicast() ||
			endpoint.Addr().IsLoopback() || endpoint.Addr().IsLinkLocalUnicast() || endpoint.Addr().Is4In6() || endpoint.Addr().Zone() != "" {
			return nil, fmt.Errorf("endpoint fixture address is invalid")
		}
	}
	return &value, nil
}

func readEndpointChange(t *testing.T, control string) *browserEndpointChange {
	t.Helper()
	value, err := parseEndpointChange(os.Getenv("STEGO_TEST_GATEWAY_ENDPOINT_CHANGE"), control)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func (w *browserGatewayWorkload) checkEndpointReplacement() {
	w.t.Helper()
	change := w.endpointChange
	if change == nil {
		return
	}
	if len(w.endpointRestarts) != 2 || w.endpointReplaced {
		w.t.Fatal("endpoint replacement requires both generated workers and one transition")
	}
	request, _ := json.Marshal(map[string]string{"nonce": change.Nonce, "action": "replace"})
	const path = "/work/network-endpoint-change.request"
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		w.t.Fatal("endpoint transition was already requested")
	}
	if err := os.WriteFile(path+".tmp", request, 0600); err != nil {
		w.t.Fatal(err)
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		w.t.Fatal(err)
	}
	deadline := time.Now().Add(120 * time.Second)
	for {
		file, err := os.Open("/work/network-endpoint-change.ack")
		if err == nil {
			data, readErr := io.ReadAll(io.LimitReader(file, 2049))
			closeErr := file.Close()
			var ack struct {
				Nonce, Action string
				PolicyUID     string `json:"policy_uid"`
				Generation    int64
			}
			if readErr != nil || closeErr != nil || len(data) > 2048 || json.Unmarshal(data, &ack) != nil ||
				ack.Nonce != change.Nonce || ack.Action != "replace" || ack.PolicyUID == "" || ack.Generation < 2 {
				w.t.Fatal("operator endpoint acknowledgement is invalid")
			}
			break
		}
		if !os.IsNotExist(err) || time.Now().After(deadline) {
			w.t.Fatal("operator did not confirm the fixed endpoint policy change")
		}
		time.Sleep(500 * time.Millisecond)
	}
	var bindings map[string][]string
	if json.Unmarshal([]byte(os.Getenv("STEGO_ALLOCATION_NETWORK_ENDPOINTS")), &bindings) != nil ||
		len(bindings["network-probe"]) != 1 || bindings["network-probe"][0] != change.Initial {
		w.t.Fatal("the initial endpoint runtime binding differs")
	}
	bindings["network-probe"] = []string{change.Replacement}
	encoded, _ := json.Marshal(bindings)
	w.t.Setenv("STEGO_ALLOCATION_NETWORK_ENDPOINTS", string(encoded))
	for _, restart := range w.endpointRestarts {
		restart(change.Replacement)
	}
	w.endpointReplaced = true
	for _, id := range w.gatewayIDs {
		w.check(id)
	}
	w.checkGatewayNetworkIsolation("after-endpoint-replacement")
	w.t.Log("The operator changed one approved address; generated workers allowed the replacement and denied the old address")
}

func TestEndpointChangeInputBoundary(t *testing.T) {
	control := "stego-service-20260915-abcdef"
	value := browserEndpointChange{"network-probe", "192.0.2.10:8080", "192.0.2.11:8080", strings.Repeat("a", 32), control + "-peer", "peer-uid"}
	encode := func(value browserEndpointChange) string { data, _ := json.Marshal(value); return string(data) }
	if _, err := parseEndpointChange(encode(value), control); err != nil {
		t.Fatal(err)
	}
	for _, alter := range []func(*browserEndpointChange){
		func(v *browserEndpointChange) { v.Endpoint = "kubernetes" },
		func(v *browserEndpointChange) { v.Namespace = "default" },
		func(v *browserEndpointChange) { v.Nonce = "" },
		func(v *browserEndpointChange) { v.Replacement = v.Initial },
		func(v *browserEndpointChange) { v.Initial = "127.0.0.1:8080" },
		func(v *browserEndpointChange) { v.Initial = "192.0.2.10:443" },
		func(v *browserEndpointChange) { v.Initial = "[::ffff:192.0.2.10]:8080" },
	} {
		changed := value
		alter(&changed)
		if _, err := parseEndpointChange(encode(changed), control); err == nil {
			t.Fatal("invalid endpoint fixture was accepted")
		}
	}
	if _, err := parseEndpointChange(encode(value), "stego-service-ci"); err == nil {
		t.Fatal("fixed CI cannot use the direct fixture")
	}
	for _, raw := range []string{encode(value) + "{}", strings.TrimSuffix(encode(value), "}") + `,"extra":true}`} {
		if _, err := parseEndpointChange(raw, control); err == nil {
			t.Fatal("extra endpoint input was accepted")
		}
	}
}
