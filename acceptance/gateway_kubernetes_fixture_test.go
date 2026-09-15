package acceptance

import (
	"bufio"
	"context"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

const gatewayImage = "quay.io/opendatahub/odh-openshell-gateway:v0.0.109-rhaiv.0@sha256:a80b79e514826e8d57ea137749cf18a6e7f3d92e26bfefe005f3a9c4a55b8bdd"
const supervisorImage = "quay.io/opendatahub/odh-openshell-supervisor:v0.0.109-rhaiv.0@sha256:96e21135c18bc9f6f4d1dfd0cccae3c91769ef4d87da2e470eca4b56a24b2152"
const sandboxImage = "ghcr.io/nvidia/openshell-community/sandboxes/base@sha256:aeef1c63f00e2913ea002ccb3aaf925f338b5c5d70e63576f0d95c16a138044e"

func (k *kubeFixture) forwardGateway(t *testing.T, ns string) (string, func()) {
	return k.forwardService(t, ns, "openshell-gateway", "8080")
}
func (k *kubeFixture) forwardService(t *testing.T, ns, service, port string) (string, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	if k.context == "" {
		t.Fatal("port forwarding requires an explicit test context")
	}
	cmd := exec.CommandContext(ctx, "kubectl", "--kubeconfig", k.config, "--context", k.context, "-n", ns, "port-forward", "service/"+service, "0:"+port, "--address=127.0.0.1")
	output, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	logs := &runtimeOutput{}
	cmd.Stderr = logs
	if err = cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	ready := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(output)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "Forwarding from 127.0.0.1:") {
				select {
				case ready <- strings.Fields(line)[2]:
				default:
				}
			}
		}
		done <- cmd.Wait()
	}()
	var once sync.Once
	stop := func() { once.Do(func() { cancel(); <-done }) }
	t.Cleanup(stop)
	select {
	case address := <-ready:
		return address, stop
	case <-time.After(15 * time.Second):
		t.Fatalf("Gateway forwarding did not start: %s", logs.String())
	}
	return "", stop
}
