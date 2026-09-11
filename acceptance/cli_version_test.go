package acceptance

import (
	"bytes"
	"debug/buildinfo"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestGeneratedCLIVersion(t *testing.T) {
	binary := buildProgram(t, "./out/cli/cmd")
	command := exec.Command(binary, "version")
	command.Dir = t.TempDir()
	command.Env = append(os.Environ(), "HYPERSHELL_CONFIG="+filepath.Join(t.TempDir(), "absent.json"), "GORACE=atexit_sleep_ms=0")
	var diagnostics bytes.Buffer
	command.Stderr = &diagnostics
	output, err := command.Output()
	if err != nil {
		t.Fatalf("offline version: %v %s", err, output)
	}
	if strings.Count(diagnostics.String(), `"event.name":"cli.command.completed"`) != 1 {
		t.Fatal("offline version has no common completion record")
	}
	var report struct {
		Application map[string]string `json:"application"`
		Compiler    map[string]string `json:"compiler"`
	}
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatal(err)
	}
	for _, record := range []map[string]string{report.Application, report.Compiler} {
		if len(record) != 7 {
			t.Fatal("unexpected record shape", record)
		}
		for _, key := range []string{"go_version", "goos", "goarch", "module_version", "vcs", "revision", "source_state"} {
			if record[key] == "" {
				t.Fatal("missing build field", key)
			}
		}
	}
	info, err := buildinfo.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	settings := map[string]string{}
	for _, s := range info.Settings {
		settings[s.Key] = s.Value
	}
	if report.Application["go_version"] != info.GoVersion || report.Application["revision"] != settings["vcs.revision"] {
		t.Fatal("application record differs from executable", report.Application)
	}
	pin, err := os.ReadFile("../.stego/compiler-revision")
	if err != nil {
		t.Fatal(err)
	}
	if report.Compiler["revision"] != strings.TrimSpace(string(pin)) || report.Compiler["source_state"] != "clean" {
		t.Fatal("compiler record differs from clean pin", report.Compiler)
	}
	stateData, err := os.ReadFile("../.stego/state.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		LastApplied struct {
			Compiler map[string]string `yaml:"compiler_build"`
		} `yaml:"last_applied"`
	}
	if err := yaml.Unmarshal(stateData, &state); err != nil {
		t.Fatal(err)
	}
	saved, _ := json.Marshal(state.LastApplied.Compiler)
	compiled, _ := json.Marshal(report.Compiler)
	if !bytes.Equal(saved, compiled) {
		t.Fatal("state and CLI have different compiler records")
	}
	bad := exec.Command(binary, "version", "extra")
	bad.Env = command.Env
	if out, err := bad.Output(); err == nil || len(out) != 0 {
		t.Fatal("invalid version arguments accepted")
	}
}
