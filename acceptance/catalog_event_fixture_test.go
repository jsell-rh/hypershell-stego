package acceptance

import (
	"context"
	"encoding/json"
	"github.com/twmb/franz-go/pkg/kgo"
	"testing"
	"time"
)

func readCatalogEvent(t *testing.T, consumer *kgo.Client, id, source, operation, kind string, expectedID ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		for _, record := range consumer.PollRecords(ctx, 1).Records() {
			if string(record.Key) != id {
				continue
			}
			var payload map[string]string
			if json.Unmarshal(record.Value, &payload) != nil {
				t.Fatal("invalid event JSON")
			}
			if payload["event_type"] != operation {
				continue
			}
			if len(payload) != 3 || payload["source"] != source || payload["source_id"] != id {
				t.Fatal("catalog event payload", string(record.Value))
			}
			headers := map[string]string{}
			for _, h := range record.Headers {
				headers[h.Key] = string(h.Value)
			}
			if len(expectedID) > 0 && headers["stego-message-id"] != expectedID[0] {
				continue
			}
			if headers["stego-message-kind"] != kind || headers["stego-message-id"] == "" {
				t.Fatal("catalog event headers", headers)
			}
			return headers["stego-message-id"]
		}
	}
	t.Fatal("catalog event was not delivered", id, operation)
	return ""
}
