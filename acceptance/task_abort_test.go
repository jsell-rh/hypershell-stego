package acceptance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc/metadata"
)

func buildApplicationWithTaskFault(t *testing.T) string {
	return buildApplicationWithMainOverlay(t, func(source string) string {
		anchor := "return stegoRunTasks(ctx, []stegoTask{"
		if strings.Count(source, anchor) != 1 || strings.Count(source, "defer stop()") != 1 {
			t.Fatal("generated task setup changed")
		}
		source = strings.Replace(source, "import (", "import (\n goruntime \"runtime\"", 1)
		source = strings.Replace(source, "defer stop()", `defer stop()
 defer func(){
  result:="done"
  if _,err:=os.Stat(os.Getenv("STEGO_TEST_WITNESS"));err!=nil{result="early"}
  os.WriteFile(os.Getenv("STEGO_TEST_CLEANUP"),[]byte(result),0600)
 }()`, 1)
		return strings.Replace(source, anchor, anchor+`
 {name:"fault-probe",run:func(ctx context.Context)error{
  defer os.WriteFile(os.Getenv("STEGO_TEST_TASK_LEFT"),[]byte("done"),0600)
  tick:=time.NewTicker(10*time.Millisecond);defer tick.Stop()
  for{
   select{case <-ctx.Done():return ctx.Err();case <-tick.C:}
   if _,err:=os.Stat(os.Getenv("STEGO_TEST_TRIGGER"));err==nil{
    if os.Getenv("STEGO_TEST_ABORT_MODE")=="goexit"{goruntime.Goexit()}
    panic("private-task-panic-value")
   }
  }
 }},
 {name:"cancel-probe",run:func(ctx context.Context)error{
  <-ctx.Done()
  time.Sleep(50*time.Millisecond)
  os.WriteFile(os.Getenv("STEGO_TEST_WITNESS"),[]byte("done"),0600)
  return ctx.Err()
 }},
 `, 1)
	})
}

func TestGatewayBackgroundTaskAbortAndRestart(t *testing.T) {
	faultBinary := buildApplicationWithTaskFault(t)
	healthyBinary := buildApplication(t)
	for _, mode := range []string{"panic", "goexit"} {
		t.Run(mode, func(t *testing.T) {
			f := database(t)
			cert := identity(t, "localhost")
			_, config := broker(t, cert)
			consumer := kafkaConsumer(t, config)
			key, settings := issuer(t)
			directory := filepath.Dir(cert.config.CAFile)
			settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"), "OTEL_EXPORTER_OTLP_ENDPOINT=", "OTEL_SERVICE_NAME=hypershell-task-abort")
			temp := t.TempDir()
			trigger, witness, left, cleanup := filepath.Join(temp, "trigger"), filepath.Join(temp, "witness"), filepath.Join(temp, "left"), filepath.Join(temp, "cleanup")
			faultSettings := append(append([]string{}, settings...), "STEGO_TEST_TRIGGER="+trigger, "STEGO_TEST_WITNESS="+witness, "STEGO_TEST_TASK_LEFT="+left, "STEGO_TEST_CLEANUP="+cleanup, "STEGO_TEST_ABORT_MODE="+mode)
			_, address, rpcAddress, waitFailure, _ := startBothWithLogs(t, faultBinary, f.dsn, config, faultSettings...)
			creator := token(t, key, "alice", "gateway:creator")
			owner := token(t, key, "alice")
			input, _ := json.Marshal(f.request("private-task-gateway"))
			code, data := requestJSON(t, "POST", address+"/api/hypershell/v1/gateways", creator, input)
			var gateway httpapi.Gateway
			if code != 201 || json.Unmarshal(data, &gateway) != nil {
				t.Fatal("Gateway creation failed", code)
			}
			var grants int
			if err := f.db.QueryRow("SELECT count(*) FROM role_bindings WHERE gateway_id=$1", gateway.ID).Scan(&grants); err != nil || grants != 1 {
				t.Fatal("Gateway owner grant was not committed", err)
			}
			if readEvent(t, consumer, gateway.ID) == "" {
				t.Fatal("Gateway event was not delivered")
			}
			awaitQueueEmpty(t, f)
			if err := os.WriteFile(trigger, []byte("fail"), 0600); err != nil {
				t.Fatal(err)
			}
			output := waitFailure()
			for _, private := range []string{"private-task-panic-value", "private-task-gateway", gateway.ID, creator, "goroutine ", "main.go:"} {
				if strings.Contains(output, private) {
					t.Fatal("task failure exposed private data", private)
				}
			}
			for _, marker := range []string{witness, left, cleanup} {
				value, err := os.ReadFile(marker)
				if err != nil || string(value) != "done" {
					t.Fatal("task failure skipped or preceded cleanup", filepath.Base(marker), err)
				}
			}
			count := 0
			for _, line := range strings.Split(output, "\n") {
				var record struct {
					Event   string   `json:"event.name"`
					Stage   string   `json:"stage"`
					Tasks   []string `json:"tasks"`
					Aborted []string `json:"aborted_tasks"`
				}
				if json.Unmarshal([]byte(line), &record) != nil || record.Event != "service.failed" {
					continue
				}
				count++
				if record.Stage != "service.run" || len(record.Tasks) != 1 || record.Tasks[0] != "fault-probe" || len(record.Aborted) != 1 || record.Aborted[0] != "fault-probe" {
					t.Fatal("task failure record lost its source", record)
				}
			}
			if count != 1 || !strings.Contains(output, `"event.name":"telemetry.runtime.stopped"`) {
				t.Fatal("task failure did not report failure and stop telemetry")
			}
			restart := append(append([]string{}, settings...), "STEGO_GRPC_ADDR="+rpcAddress)
			stop, address, rpcAddress := startBoth(t, healthyBinary, f.dsn, config, restart...)
			defer stop()
			if code, _ := requestJSON(t, "GET", address+"/api/hypershell/v1/gateways/"+gateway.ID, owner, nil); code != 200 {
				t.Fatal("restart lost Gateway", code)
			}
			client, connection := grpcClient(t, rpcAddress, cert)
			defer connection.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			result, err := client.GetGateway(metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+owner)), &pb.GetGatewayRequest{Id: gateway.ID})
			if err != nil || result.GetGateway().GetName() != "private-task-gateway" {
				t.Fatal("gRPC restart lost Gateway", err)
			}
		})
	}
}
