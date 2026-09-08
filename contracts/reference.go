// Package contracts verifies the fixed Hypershell compatibility inputs.
package contracts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"sort"
	"strings"

	"github.com/bufbuild/protocompile"
	"github.com/bufbuild/protocompile/linker"
	"github.com/getkin/kin-openapi/openapi3"
)

//go:embed reference upstream.json
var snapshot embed.FS

type sourceManifest struct {
	Repository string       `json:"repository"`
	Commit     string       `json:"commit"`
	Files      []sourceFile `json:"files"`
}

type sourceFile struct {
	Source   string `json:"source"`
	Snapshot string `json:"snapshot"`
	SHA256   string `json:"sha256"`
}

// Reference contains validated REST and gRPC contracts. It is not a running
// service and does not establish behavioral compatibility.
type Reference struct {
	OpenAPI *openapi3.T
	Proto   linker.Files
}

type Operation struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	ID     string `json:"id"`
}

type RPC struct {
	Name            string `json:"name"`
	Input           string `json:"input"`
	Output          string `json:"output"`
	ClientStreaming bool   `json:"client_streaming"`
	ServerStreaming bool   `json:"server_streaming"`
}

type Inventory struct {
	REST []Operation `json:"rest"`
	GRPC []RPC       `json:"grpc"`
}

func verifiedFiles() (map[string]string, error) {
	data, err := snapshot.ReadFile("upstream.json")
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest sourceManifest
	if err := decoder.Decode(&manifest); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("extra manifest data")
	}
	if manifest.Repository != "https://github.com/openshift-online/hypershell" || manifest.Commit != "14256be29bcfe4fff38bcaf4a41511cb394ea8e1" {
		return nil, fmt.Errorf("reference revision changed without a compatibility review")
	}
	files := make(map[string]string)
	for _, file := range manifest.Files {
		if !fs.ValidPath(file.Snapshot) || !strings.HasPrefix(file.Snapshot, "reference/") || !fs.ValidPath(file.Source) {
			return nil, fmt.Errorf("invalid manifest path %q", file.Snapshot)
		}
		if _, exists := files[file.Snapshot]; exists {
			return nil, fmt.Errorf("duplicate snapshot %s", file.Snapshot)
		}
		content, err := snapshot.ReadFile(file.Snapshot)
		if err != nil {
			return nil, err
		}
		if fmt.Sprintf("%x", sha256.Sum256(content)) != file.SHA256 {
			return nil, fmt.Errorf("reference hash mismatch: %s", file.Snapshot)
		}
		files[file.Snapshot] = string(content)
	}
	err = fs.WalkDir(snapshot, "reference", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			if _, exists := files[path]; !exists {
				return fmt.Errorf("reference file has no provenance: %s", path)
			}
		}
		return nil
	})
	return files, err
}

// Load checks source hashes and resolves references only from the embedded
// snapshot. It cannot fetch remote schemas or read external files.
func Load(ctx context.Context) (*Reference, error) {
	files, err := verifiedFiles()
	if err != nil {
		return nil, err
	}
	loader := openapi3.NewLoader()
	loader.Context = ctx
	loader.ReadFromURIFunc = func(_ *openapi3.Loader, location *url.URL) ([]byte, error) {
		if location.Scheme != "" || location.Host != "" || location.User != nil || location.RawQuery != "" || !strings.HasPrefix(location.Path, "reference/openapi/") {
			return nil, fmt.Errorf("reference is outside the contract snapshot: %s", location)
		}
		content, exists := files[location.Path]
		if !exists {
			return nil, fmt.Errorf("unknown contract reference %s", location.Path)
		}
		return []byte(content), nil
	}
	document, err := loader.LoadFromFile("reference/openapi/openapi.yaml")
	if err != nil {
		return nil, fmt.Errorf("loading reference OpenAPI: %w", err)
	}
	if err := document.Validate(ctx); err != nil {
		return nil, fmt.Errorf("invalid reference OpenAPI: %w", err)
	}
	protos := make(map[string]string)
	var names []string
	for name, content := range files {
		if strings.HasPrefix(name, "reference/proto/") {
			name = strings.TrimPrefix(name, "reference/proto/")
			protos[name] = content
			names = append(names, name)
		}
	}
	sort.Strings(names)
	compiler := protocompile.Compiler{
		Resolver:       protocompile.WithStandardImports(&protocompile.SourceResolver{Accessor: protocompile.SourceAccessorFromMap(protos)}),
		MaxParallelism: 4,
	}
	descriptors, err := compiler.Compile(ctx, names...)
	if err != nil {
		return nil, fmt.Errorf("invalid reference protobuf: %w", err)
	}
	return &Reference{OpenAPI: document, Proto: descriptors}, nil
}

func (r *Reference) Inventory() Inventory {
	var inventory Inventory
	for path, item := range r.OpenAPI.Paths.Map() {
		for method, operation := range item.Operations() {
			inventory.REST = append(inventory.REST, Operation{Method: method, Path: path, ID: operation.OperationID})
		}
	}
	for _, file := range r.Proto {
		services := file.Services()
		for i := 0; i < services.Len(); i++ {
			service := services.Get(i)
			methods := service.Methods()
			for j := 0; j < methods.Len(); j++ {
				method := methods.Get(j)
				inventory.GRPC = append(inventory.GRPC, RPC{
					Name:  string(service.FullName()) + "/" + string(method.Name()),
					Input: string(method.Input().FullName()), Output: string(method.Output().FullName()),
					ClientStreaming: method.IsStreamingClient(), ServerStreaming: method.IsStreamingServer(),
				})
			}
		}
	}
	sort.Slice(inventory.REST, func(i, j int) bool {
		if inventory.REST[i].Path != inventory.REST[j].Path {
			return inventory.REST[i].Path < inventory.REST[j].Path
		}
		return inventory.REST[i].Method < inventory.REST[j].Method
	})
	sort.Slice(inventory.GRPC, func(i, j int) bool { return inventory.GRPC[i].Name < inventory.GRPC[j].Name })
	return inventory
}
