//go:build ignore

// This command reads public image metadata from the test registry.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
)

func main() {
	if len(os.Args) != 3 || (os.Args[2] != "service" && os.Args[2] != "worker") {
		panic("require image metadata path and service or worker kind")
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic("cannot read image metadata")
	}
	var metadata struct {
		Digest string `json:"digest"`
		Config struct {
			Architecture string
			OS           string
			Config       struct {
				User       string
				Entrypoint []string
			}
		}
	}
	if json.Unmarshal(data, &metadata) != nil || !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(metadata.Digest) {
		panic("invalid image digest")
	}
	if metadata.Config.Architecture != "amd64" || metadata.Config.OS != "linux" || metadata.Config.Config.User != "65532:65532" || len(metadata.Config.Config.Entrypoint) != 1 || metadata.Config.Config.Entrypoint[0] != "/"+os.Args[2] {
		panic("invalid service image configuration")
	}
	fmt.Println(metadata.Digest)
}
