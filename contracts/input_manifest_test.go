package contracts

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"context"
	"encoding/json"
	"gopkg.in/yaml.v3"
	"os/exec"
	"time"
)

func TestGeneratedProjectInputManifest(t *testing.T) {
	root := ".."
	read := func(name string) []byte {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	var state struct {
		LastApplied struct {
			Inputs struct {
				Version int    `yaml:"version"`
				SHA256  string `yaml:"sha256"`
				Options struct {
					ModuleName  string `yaml:"module_name"`
					GoVersion   string `yaml:"go_version"`
					OutputDir   string `yaml:"output_dir"`
					RegistryRef string `yaml:"registry_ref"`
				} `yaml:"options"`
				Files map[string]struct {
					Exists bool   `yaml:"exists"`
					SHA256 string `yaml:"sha256"`
					Mode   uint32 `yaml:"mode"`
				} `yaml:"files"`
			} `yaml:"inputs"`
		} `yaml:"last_applied"`
	}
	if err := yaml.Unmarshal(read(".stego/state.yaml"), &state); err != nil {
		t.Fatal(err)
	}
	manifest := state.LastApplied.Inputs
	if manifest.Version != 1 || len(manifest.SHA256) != 64 {
		t.Fatal("missing input manifest")
	}
	var service struct {
		Overrides map[string]struct {
			Document   string   `yaml:"document"`
			References []string `yaml:"references"`
			ProtoFiles []struct {
				Path string `yaml:"path"`
			} `yaml:"proto_files"`
			Workers []struct {
				Package string `yaml:"package"`
			} `yaml:"workers"`
		} `yaml:"overrides"`
	}
	if err := yaml.Unmarshal(read("service.yaml"), &service); err != nil {
		t.Fatal(err)
	}
	expected := map[string]bool{"service.yaml": true, "go.mod": true, "go.sum": true, ".stego/config.yaml": true}
	for _, input := range service.Overrides["grpc-application"].ProtoFiles {
		if expected[input.Path] {
			t.Fatal("duplicate input declaration", input.Path)
		}
		expected[input.Path] = true
	}
	client := service.Overrides["go-sdk"]
	for _, name := range append([]string{client.Document}, client.References...) {
		if name == "" || expected[name] {
			t.Fatal("invalid SDK input declaration", name)
		}
		expected[name] = true
	}
	for _, worker := range service.Overrides["kubernetes-service"].Workers {
		if worker.Package == "" {
			t.Fatal("worker has no source package")
		}
		// Several worker functions can share one declaration file.
		expected[worker.Package+"/worker.go"] = true
	}
	if len(expected) != len(manifest.Files) {
		t.Fatal("input inventory differs from declarations", len(expected), len(manifest.Files))
	}
	for name := range expected {
		recorded, ok := manifest.Files[name]
		sum := sha256.Sum256(read(name))
		info, err := os.Stat(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if !ok || !recorded.Exists || recorded.SHA256 != hex.EncodeToString(sum[:]) || recorded.Mode != uint32(info.Mode().Perm()) {
			t.Fatal("input record differs from source", name)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "list", "-m", "-json", "-mod=readonly")
	command.Dir = root
	command.Env = append(os.Environ(), "GOWORK=off")
	data, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	var module struct{ Path, GoVersion string }
	if err := json.Unmarshal(data, &module); err != nil {
		t.Fatal(err)
	}
	var config struct {
		Registry []struct {
			Ref string `yaml:"ref"`
		} `yaml:"registry"`
	}
	if err := yaml.Unmarshal(read(".stego/config.yaml"), &config); err != nil {
		t.Fatal(err)
	}
	if len(config.Registry) != 1 || manifest.Options.RegistryRef != config.Registry[0].Ref || manifest.Options.OutputDir != "out" || manifest.Options.ModuleName != module.Path || manifest.Options.GoVersion != module.GoVersion {
		t.Fatal("generation options differ from the project", manifest.Options)
	}
}
