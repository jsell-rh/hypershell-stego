// Package gateway supplies the protocol for the pinned OpenShell Gateway image.
package gateway

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/bufbuild/protocompile"
	"google.golang.org/protobuf/reflect/protoreflect"
)

//go:embed *.proto source.json
var source embed.FS

func Load(ctx context.Context) (protoreflect.ServiceDescriptor, error) {
	manifest, err := source.ReadFile("source.json")
	if err != nil {
		return nil, err
	}
	var record struct {
		Files map[string]string `json:"files"`
	}
	if json.Unmarshal(manifest, &record) != nil || len(record.Files) != 4 {
		return nil, errors.New("invalid Gateway protocol manifest")
	}
	files := map[string]string{}
	for name, want := range record.Files {
		body, err := source.ReadFile(name)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(body)
		if hex.EncodeToString(sum[:]) != want {
			return nil, errors.New("Gateway protocol differs from its source manifest")
		}
		files[name] = string(body)
	}
	compiler := protocompile.Compiler{Resolver: protocompile.WithStandardImports(&protocompile.SourceResolver{Accessor: protocompile.SourceAccessorFromMap(files)})}
	descriptors, err := compiler.Compile(ctx, "openshell.proto")
	if err != nil {
		return nil, err
	}
	service := descriptors[0].Services().ByName("OpenShell")
	if service == nil {
		return nil, errors.New("Gateway protocol service is absent")
	}
	return service, nil
}
