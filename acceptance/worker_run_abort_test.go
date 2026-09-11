package acceptance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Faults exist only in this temporary build. The committed worker has no fault
// setting. Abort the outer Run callback after the real domain work and cleanup.
func buildIdentityWorkerWithRunAbort(t *testing.T) string {
	t.Helper()
	return buildProgramWithMainOverlay(t, "./out/deploy/workers/gateway-identity", func(source string) string {
		anchor := "runtime.Main(application.Run)"
		if strings.Count(source, anchor) != 1 {
			t.Fatal("generated worker main changed")
		}
		source = strings.Replace(source, "import (", `import (
 "context"
 "os"
 goruntime "runtime"`, 1)
		return strings.Replace(source, anchor, `runtime.Main(func(ctx context.Context,metrics *runtime.Metrics)error{
   defer func(){
    if err:=os.WriteFile(os.Getenv("STEGO_TEST_WORKER_RETURNED"),[]byte("closed"),0600);err!=nil{panic("test marker failed")}
    if os.Getenv("STEGO_TEST_WORKER_ABORT")=="goexit"{goruntime.Goexit()}
    panic("private-controller-run-panic")
   }()
   return application.Run(ctx,metrics)
  })`, 1)
	})
}

func checkIdentityWorkerRunAborts(t *testing.T, k *keycloakFixture, address, ca, bearer, healthy string, reset func(), wait func() string, expected string) {
	t.Helper()
	fault := buildIdentityWorkerWithRunAbort(t)
	for _, mode := range []string{"panic", "goexit"} {
		reset()
		marker := filepath.Join(t.TempDir(), "returned")
		stop, logs := startIdentityControllerWithExit(t, fault, k, address, ca, bearer, 1, "OTEL_EXPORTER_OTLP_ENDPOINT=", "STEGO_TEST_WORKER_ABORT="+mode, "STEGO_TEST_WORKER_RETURNED="+marker)
		if wait() != expected {
			t.Fatal("fault worker changed Gateway identity", mode)
		}
		stop()
		data, err := os.ReadFile(marker)
		if err != nil || string(data) != "closed" {
			t.Fatal("Run callback did not finish domain cleanup", mode, err)
		}
		output := logs()
		for _, private := range []string{"private-controller-run-panic", bearer, "goroutine ", "main.go:"} {
			if strings.Contains(output, private) {
				t.Fatal("worker abort exposed private data", mode)
			}
		}
		count := 0
		for _, line := range strings.Split(output, "\n") {
			var record map[string]any
			if json.Unmarshal([]byte(line), &record) != nil || record["event.name"] != "controller.process.failed" {
				continue
			}
			count++
			if len(record) != 3 || record["severity"] != "ERROR" || record["message"] != "Controller process failed" {
				t.Fatal("invalid worker failure record", mode)
			}
		}
		if count != 1 {
			t.Fatal("worker abort did not report one failure", mode)
		}
	}
	reset()
	stop, _ := startIdentityControllerWithExit(t, healthy, k, address, ca, bearer, 0, "OTEL_EXPORTER_OTLP_ENDPOINT=")
	if wait() != expected {
		t.Fatal("healthy worker did not repair identity after abort")
	}
	stop()
	t.Log("Generated identity worker passed callback panic, Goexit, private failure output, and recovery")
}
