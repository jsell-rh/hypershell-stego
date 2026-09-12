package databaseplacement

import (
	"github.com/segmentio/ksuid"
	"google.golang.org/grpc/metadata"
	"testing"
)

func TestReadPlacementContract(t *testing.T) {
	id := ksuid.New().String()
	for _, tc := range []struct {
		name   string
		header metadata.MD
		want   string
		bad    bool
	}{
		{"assigned", metadata.Pairs(versionKey, "cluster-v1", clusterKey, id), id, false},
		{"unassigned", metadata.Pairs(versionKey, "cluster-v1"), "", false},
		{"old server", nil, "", true},
		{"unknown version", metadata.Pairs(versionKey, "cluster-v2", clusterKey, id), "", true},
		{"duplicate version", metadata.Pairs(versionKey, "cluster-v1", versionKey, "cluster-v1"), "", true},
		{"duplicate cluster", metadata.Pairs(versionKey, "cluster-v1", clusterKey, id, clusterKey, id), "", true},
		{"empty cluster", metadata.Pairs(versionKey, "cluster-v1", clusterKey, ""), "", true},
		{"invalid cluster", metadata.Pairs(versionKey, "cluster-v1", clusterKey, "invalid"), "", true},
		{"nil cluster ID", metadata.Pairs(versionKey, "cluster-v1", clusterKey, ksuid.Nil.String()), "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Read(tc.header)
			if (err != nil) != tc.bad || got != tc.want {
				t.Fatal("placement contract result differs", err)
			}
		})
	}
}
