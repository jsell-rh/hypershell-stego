package acceptance

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

type databaseRestartReadFixture struct {
	t           *testing.T
	ctx         context.Context
	object      kube.Object
	code, calls int
	err         error
}

func (r *databaseRestartReadFixture) Request(ctx context.Context, method, path string, body kube.Object) (kube.Object, int, error) {
	r.t.Helper()
	r.calls++
	if ctx != r.ctx || method != "GET" || path != "/api/v1/namespaces/private-namespace/pods/private-pod" || body != nil {
		r.t.Fatal("fixture read changed its context or scope")
	}
	return r.object, r.code, r.err
}

// The private error cannot be formatted, even through an error wrapper.
type privateDatabaseReadError struct{}

func (privateDatabaseReadError) Error() string { panic("private provider error was formatted") }

func TestDatabaseRestartFixtureReadPreservesGuardAndPrivacy(t *testing.T) {
	for _, mode := range []string{"ready", "not ready", "restarted", "request failed", "deadline", "canceled", "forbidden", "unauthorized", "not found", "encoding", "decoding", "missing uid", "wrong app", "wrong job", "missing postgres"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			metadata := map[string]any{"uid": "fixture-uid", "labels": map[string]any{"app": "stego-fixture", "job-name": "service-check"}}
			postgres := map[string]any{"name": "postgres", "restartCount": 0, "ready": true}
			status := map[string]any{"initContainerStatuses": []any{postgres}}
			r := &databaseRestartReadFixture{t: t, object: kube.Object{"metadata": metadata, "status": status, "private": "private-response-body"}, code: 200}
			category := ""
			count, ready := 0, true
			switch mode {
			case "not ready":
				postgres["ready"] = false
				ready = false
			case "restarted":
				postgres["restartCount"] = 2
				count = 2
			case "request failed":
				r.code = 0
				r.err = privateDatabaseReadError{}
				category = "request_failed"
			case "deadline":
				cancel()
				ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				defer cancel()
				r.code = 0
				r.err = privateDatabaseReadError{}
				category = "deadline"
			case "canceled":
				cancel()
				r.code = 0
				r.err = privateDatabaseReadError{}
				category = "canceled"
			case "forbidden":
				r.code = 403
				r.err = privateDatabaseReadError{}
				category = "http_status"
			case "unauthorized":
				r.code = 401
				r.err = privateDatabaseReadError{}
				category = "http_status"
			case "not found":
				r.code = 404
				r.object = nil
				category = "http_status"
			case "encoding":
				r.object["private"] = make(chan string)
				category = "object_encoding"
			case "decoding":
				metadata["labels"] = []string{"private-label"}
				category = "object_decoding"
			case "missing uid":
				delete(metadata, "uid")
				category = "pod_identity"
			case "wrong app":
				metadata["labels"].(map[string]any)["app"] = "private-app"
				category = "app_label"
			case "wrong job":
				metadata["labels"].(map[string]any)["job-name"] = "private-job"
				category = "job_label"
			case "missing postgres":
				postgres["name"] = "private-container"
				category = "postgres_status_missing"
			}
			r.ctx = ctx
			uid, gotCount, gotReady, err := readDatabaseRestartPod(ctx, r, "private-namespace", "private-pod")
			if r.calls != 1 {
				t.Fatal("fixture read was repeated")
			}
			if category == "" {
				if err != nil || uid != "fixture-uid" || gotCount != count || gotReady != ready {
					t.Fatal("valid Pod status was changed")
				}
				return
			}
			if err == nil || uid != "" || gotCount != 0 || gotReady {
				t.Fatal("invalid Pod status was accepted")
			}
			text := err.Error()
			if !strings.Contains(text, "category="+category+" ") || !strings.HasSuffix(text, " status="+strconv.Itoa(r.code)) || strings.Contains(text, "private") || (r.err != nil && errors.Is(err, r.err)) {
				t.Fatal("fixture diagnostic lost its fixed category or retained private data")
			}
		})
	}
}
