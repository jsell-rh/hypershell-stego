// Package databaseplacement carries private, retained database placement.
package databaseplacement

import (
	"context"
	"errors"

	"github.com/segmentio/ksuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const versionKey = "hypershell-database-placement"
const clusterKey = "hypershell-database-cluster-id"

var ErrContract = errors.New("database placement metadata is invalid")

func validID(id string) bool {
	value, err := ksuid.Parse(id)
	return err == nil && value != ksuid.Nil && value.String() == id
}

// Set adds placement only after the server authorizes a retained read.
// A nil cluster is unassigned, including records from before placement storage.
func Set(ctx context.Context, cluster *string) error {
	header := metadata.Pairs(versionKey, "cluster-v1")
	if cluster != nil {
		if !validID(*cluster) {
			return ErrContract
		}
		header.Set(clusterKey, *cluster)
	}
	return grpc.SetHeader(ctx, header)
}

// Read requires an explicit contract version. Empty placement is not authority
// to act in any cluster. Duplicate or malformed values are rejected.
func Read(header metadata.MD) (string, error) {
	version, cluster := header.Get(versionKey), header.Get(clusterKey)
	if len(version) != 1 || version[0] != "cluster-v1" || len(cluster) > 1 {
		return "", ErrContract
	}
	if len(cluster) == 0 {
		return "", nil
	}
	if !validID(cluster[0]) {
		return "", ErrContract
	}
	return cluster[0], nil
}
