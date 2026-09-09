package acceptance

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gatewayworkload"
	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

func gatewaySandboxWorkflow(t *testing.T, k *kubeFixture, gateway httpapi.Gateway, service protoreflect.ServiceDescriptor, connection *grpc.ClientConn, owner, denied string, call gatewayCall, checkCount func(int32)) func(*grpc.ClientConn, string) {
	t.Helper()
	ns, err := gatewayworkload.SandboxNamespace(gateway.ID)
	if err != nil {
		t.Fatal(err)
	}
	input := `{"name":"execution-proof","spec":{"logLevel":"info","command":["sleep","1800"],"template":{"resources":{"requests":{"cpu":"1","memory":"1Gi"},"limits":{"cpu":"2","memory":"2Gi"}}},"policy":{"version":1,"filesystem":{"includeWorkdir":true,"readOnly":["/usr","/lib","/proc","/dev/urandom","/etc","/var/log"],"readWrite":["/sandbox","/tmp","/dev/null"]},"landlock":{"compatibility":"hard_requirement"}}}}`
	if _, err := call("CreateSandbox", denied, input); status.Code(err) != codes.PermissionDenied {
		t.Fatal("ungranted user created a sandbox", err)
	}
	start := time.Now()
	created, err := call("CreateSandbox", owner, input)
	if err != nil {
		t.Fatal("create sandbox", err)
	}
	encoded, _ := protojson.Marshal(created)
	t.Logf("Sandbox creation response: %s", encoded)
	t.Cleanup(func() { _, _ = call("DeleteSandbox", owner, `{"name":"execution-proof"}`) })
	var state struct {
		Sandbox struct {
			Metadata struct{ Id string }
			Status   struct {
				Phase    string
				AgentPod string
			}
		}
	}
	deadline := time.Now().Add(240 * time.Second)
	for {
		current, err := call("GetSandbox", owner, `{"name":"execution-proof"}`)
		if err != nil {
			t.Fatal("get sandbox", err)
		}
		body, _ := protojson.Marshal(current)
		if err := json.Unmarshal(body, &state); err != nil {
			t.Fatal(err)
		}
		if state.Sandbox.Status.Phase == "SANDBOX_PHASE_READY" {
			break
		}
		if time.Now().After(deadline) || state.Sandbox.Status.Phase == "SANDBOX_PHASE_ERROR" {
			pods, _ := k.command(context.Background(), "", "-n", ns, "get", "pods", "-o", "wide")
			events, _ := k.command(context.Background(), "", "-n", ns, "get", "events", "--sort-by=.lastTimestamp")
			logs, _ := k.command(context.Background(), "", "-n", ns, "logs", "pod/default--execution-proof", "--all-containers=true", "--tail=40")
			t.Fatalf("sandbox did not become ready: %s\n%s\n%s\n%s", body, pods, events, logs)
		}
		time.Sleep(time.Second)
	}
	t.Logf("Sandbox became ready in %s", time.Since(start))
	checkCount(1)
	sandboxAdmissionChecks(t, k, ns)
	if state.Sandbox.Metadata.Id == "" {
		t.Fatal("sandbox has no ID")
	}
	descriptor := service.Methods().ByName("ExecSandbox")
	execute := func(bearer string, command []string) (string, error) {
		request := dynamicpb.NewMessage(descriptor.Input())
		body, _ := json.Marshal(map[string]any{"sandboxId": state.Sandbox.Metadata.Id, "command": command, "timeoutSeconds": 15})
		if err := protojson.Unmarshal(body, request); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+bearer)), 25*time.Second)
		defer cancel()
		stream, err := connection.NewStream(ctx, &grpc.StreamDesc{ServerStreams: true}, "/openshell.v1.OpenShell/ExecSandbox")
		if err != nil {
			return "", err
		}
		if err = stream.SendMsg(request); err != nil {
			return "", err
		}
		if err = stream.CloseSend(); err != nil {
			return "", err
		}
		var output, stderr strings.Builder
		exited := false
		for {
			event := dynamicpb.NewMessage(descriptor.Output())
			err := stream.RecvMsg(event)
			if err == io.EOF {
				if !exited {
					t.Fatal("exec stream has no exit status")
				}
				return output.String(), nil
			}
			if err != nil {
				return "", err
			}
			body, _ := protojson.Marshal(event)
			var value map[string]map[string]any
			if err := json.Unmarshal(body, &value); err != nil {
				t.Fatal(err)
			}
			if chunk, ok := value["stdout"]; ok {
				data, err := base64.StdEncoding.DecodeString(chunk["data"].(string))
				if err != nil {
					t.Fatal(err)
				}
				output.Write(data)
			}
			if chunk, ok := value["stderr"]; ok {
				data, err := base64.StdEncoding.DecodeString(chunk["data"].(string))
				if err != nil {
					t.Fatal(err)
				}
				stderr.Write(data)
			}
			if exit, ok := value["exit"]; ok {
				exited = true
				if code, ok := exit["exitCode"]; ok && code != float64(0) {
					t.Fatalf("sandbox exec failed: %s; stdout: %s; stderr: %s", body, output.String(), stderr.String())
				}
			}
		}
	}
	if _, err := execute(denied, []string{"id"}); status.Code(err) != codes.PermissionDenied {
		t.Fatal("ungranted user executed in sandbox", err)
	}
	limitOutput, err := execute(owner, []string{"python3", "-c", sandboxProcessLimitProbe})
	if err != nil || !strings.HasPrefix(limitOutput, "process-limit-ok ") {
		t.Fatal("sandbox process limit is not enforced", limitOutput, err)
	}
	t.Log(strings.TrimSpace(limitOutput))
	output, err := execute(owner, []string{"sh", "-c", "set -eu; printf persistent > /sandbox/acceptance.txt; printf 'stego-sandbox-ok\\n'; uname -r; id -u; cat /proc/sys/kernel/random/boot_id; for key in /etc/openshell-tls/client/tls.key /etc/openshell-tls/proxy/client/tls.key; do if cat \"$key\" >/dev/null 2>/dev/null; then echo tls-key-readable >&2; exit 1; fi; done"})
	if err != nil || !strings.Contains(output, "stego-sandbox-ok") {
		t.Fatal("sandbox execution", output, err)
	}
	hostBootID, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(output)
	if len(lines) != 4 || lines[3] == strings.TrimSpace(string(hostBootID)) || lines[2] == "0" {
		t.Fatal("sandbox did not use a separate kernel and non-root process", output)
	}
	t.Logf("Sandbox execution output: %s", output)
	return func(current *grpc.ClientConn, bearer string) {
		t.Helper()
		checkCount(1)
		connection = current
		deadline := time.Now().Add(45 * time.Second)
		for {
			output, err := execute(bearer, []string{"cat", "/sandbox/acceptance.txt"})
			if err == nil {
				if output != "persistent" {
					t.Fatal("sandbox data changed after Gateway restart", output)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("sandbox did not reconnect after Gateway restart", err)
			}
			time.Sleep(time.Second)
		}
		if _, err := call("DeleteSandbox", bearer, `{"name":"execution-proof"}`); err != nil {
			t.Fatal("delete sandbox", err)
		}
		deadline = time.Now().Add(90 * time.Second)
		for {
			output := k.must(t, "", "-n", ns, "get", "pods", "-o", "name")
			if len(strings.TrimSpace(string(output))) == 0 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("sandbox Pod remained after deletion", string(output))
			}
			time.Sleep(time.Second)
		}
		checkCount(0)
		t.Log("Sandbox execution and stored data survived Gateway and database restart")
	}

}

func sandboxAdmissionChecks(t *testing.T, k *kubeFixture, ns string) {
	t.Helper()
	var list struct{ Items []map[string]any }
	if err := json.Unmarshal(k.must(t, "", "-n", ns, "get", "pods", "-o", "json"), &list); err != nil || len(list.Items) != 1 {
		t.Fatal("expected one sandbox Pod", err)
	}
	original := list.Items[0]
	spec := original["spec"].(map[string]any)
	socketInMemory := false
	for _, item := range spec["volumes"].([]any) {
		v := item.(map[string]any)
		if v["name"] == "openshell-sidecar-state" {
			d, _ := v["emptyDir"].(map[string]any)
			socketInMemory = d["medium"] == "Memory" && d["sizeLimit"] == "16Mi"
		}
	}
	if !socketInMemory {
		t.Fatal("sandbox socket is not in guest memory")
	}
	for _, item := range spec["initContainers"].([]any) {
		c := item.(map[string]any)
		if c["name"] == "workspace-init" && c["securityContext"].(map[string]any)["runAsUser"] == float64(0) {
			t.Fatal("workspace setup runs as root")
		}
	}
	if spec["runtimeClassName"] != os.Getenv("STEGO_TEST_SANDBOX_RUNTIME_CLASS") {
		t.Fatal("sandbox selected a different runtime")
	}
	original["metadata"] = map[string]any{"name": "isolation-denial-check", "namespace": ns, "ownerReferences": original["metadata"].(map[string]any)["ownerReferences"]}
	delete(original, "status")
	delete(spec, "nodeName")
	snapshot, _ := json.Marshal(original)
	check := func(label string, change func(map[string]any)) {
		t.Helper()
		var pod map[string]any
		if err := json.Unmarshal(snapshot, &pod); err != nil {
			t.Fatal(err)
		}
		if change != nil {
			change(pod)
		}
		input, _ := json.Marshal(pod)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		output, err := k.command(ctx, string(input), "create", "--dry-run=server", "-f", "-")
		if change == nil {
			if err != nil {
				t.Fatalf("valid sandbox Pod was denied: %s", output)
			}
			return
		}
		if err == nil || !strings.Contains(string(output), "Sandbox isolation rule") {
			t.Fatalf("%s was not denied by sandbox admission: %v %s", label, err, output)
		}
	}
	check("valid", nil)
	for _, field := range []string{"hostNetwork", "hostPID", "hostIPC"} {
		check(field, func(p map[string]any) {
			spec := p["spec"].(map[string]any)
			spec[field] = true
			if field == "hostPID" {
				delete(spec, "shareProcessNamespace")
			}
		})
	}
	check("runtime override", func(p map[string]any) { delete(p["spec"].(map[string]any), "runtimeClassName") })
	check("guest kernel override", func(p map[string]any) {
		p["metadata"].(map[string]any)["annotations"] = map[string]any{"io.katacontainers.config.hypervisor.kernel_params": "init=/bin/sh"}
	})
	check("host storage", func(p map[string]any) {
		s := p["spec"].(map[string]any)
		s["volumes"] = append(s["volumes"].([]any), map[string]any{"name": "host", "hostPath": map[string]any{"path": "/"}})
	})
	check("Gateway credentials", func(p map[string]any) {
		s := p["spec"].(map[string]any)
		s["volumes"] = append(s["volumes"].([]any), map[string]any{"name": "database", "secret": map[string]any{"secretName": "openshell-gateway-db-credentials"}})
	})
	check("other sandbox storage", func(p map[string]any) {
		s := p["spec"].(map[string]any)
		for _, v := range s["volumes"].([]any) {
			volume := v.(map[string]any)
			if claim, ok := volume["persistentVolumeClaim"].(map[string]any); ok {
				claim["claimName"] = "workspace-another-sandbox"
			}
		}
	})
	check("privileged container", func(p map[string]any) {
		c := p["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)
		c["securityContext"].(map[string]any)["privileged"] = true
		c["securityContext"].(map[string]any)["allowPrivilegeEscalation"] = true
	})
	check("root workload", func(p map[string]any) {
		c := p["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)
		sc := c["securityContext"].(map[string]any)
		sc["runAsUser"] = 0
		sc["runAsNonRoot"] = false
	})
	check("bootstrap token in workload", func(p map[string]any) {
		c := p["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)
		c["volumeMounts"] = append(c["volumeMounts"].([]any), map[string]any{"name": "openshell-sa-token", "mountPath": "/stolen-token"})
	})
	check("replacement network image", func(p map[string]any) {
		for _, entry := range p["spec"].(map[string]any)["containers"].([]any) {
			c := entry.(map[string]any)
			if c["name"] == "openshell-supervisor-network" {
				c["image"] = sandboxImage
				return
			}
		}
		t.Fatal("sandbox has no separate network supervisor")
	})
	check("extra capability", func(p map[string]any) {
		c := p["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)
		c["securityContext"].(map[string]any)["capabilities"] = map[string]any{"add": []string{"SYS_MODULE"}}
	})
}

// Fork at most 513 children, and release all children on success or failure.
// Check the hard limit first so this probe cannot exhaust an unlimited VM.
const sandboxProcessLimitProbe = `import errno, os, resource
assert resource.getrlimit(resource.RLIMIT_NPROC) == (512, 512), "wrong process limit"
try:
    resource.setrlimit(resource.RLIMIT_NPROC, (513, 513))
    raise AssertionError("workload raised its hard limit")
except (ValueError, PermissionError):
    pass
children = []
r, w = os.pipe()
limited = False
try:
    for _ in range(513):
        try:
            pid = os.fork()
        except OSError as e:
            if e.errno != errno.EAGAIN:
                raise
            limited = True
            break
        if pid == 0:
            os.close(w)
            os.read(r, 1)
            os._exit(0)
        children.append(pid)
finally:
    os.close(w)
    os.close(r)
    for pid in children:
        os.waitpid(pid, 0)
assert limited and children, "process limit did not deny fork"
print("process-limit-ok", len(children))
`
