STEGO state now contains a versioned project input manifest. It records hashes
and permission modes for the service declaration, project configuration, module
files, declared protobuf and OpenAPI inputs, and worker declaration files.
It also records the module name,
Go version argument, relative output directory, and registry reference.
Registry content and compiler build data remain separate state fields.

`TestGeneratedProjectInputManifest` compares the saved records with the current
project files and declaration. It checks module options through the Go tool and
checks the configured registry reference. The test failed against the previous
state because that state had no input manifest.

The manifest records actual captured inputs. If apply creates or changes a
module file, the next apply can update state without changing generated code.
The existing `scripts/generate.sh` sequence runs apply, dependency resolution,
and apply again. Once those inputs are stable, `--check` requires no changes.
The compiler uses captured module bytes and rejects modified input snapshots
before starting its write transaction.

These records support input comparison. They do not prove compiler artifact
integrity or include every application build input. Domain files outside the declared generator inputs, external tool
binaries, dependency source trees, and tool environment need separate build
records. The compiler also cannot detect an undeclared external read by a custom
generator. Complete artifact verification remains open work.

The contract test passed in 1.533 seconds after regeneration. The CLI version
test passed in 1.546 seconds. The application build, vet, and module verification
also passed. An independent Python implementation verified all 14 source hashes,
permission modes, and the manifest digest:
`75639ce4d1c478b0ab413a70b02fbadc0a07622be60bb1353ebb35ba145f6852`.

Generation changed only saved state and the CLI's embedded compiler build
record. Application runtime code, protobuf output, `go.mod`, and `go.sum` did
not change. The full database, identity-provider, and Kubernetes workflow suites
were not repeated locally for this compiler-state change.

CI run 34625446509 found a stale test expectation after the generated worker
was added. The saved manifest correctly included
`internal/gatewayidentityapp/worker.go`; the test expected 23 inputs instead of
24. The test now reads the worker declarations and checks each captured file's
hash and mode. Several worker functions can share one declaration file. The
focused check passed in 0.014 seconds on 2026-09-11 with one Go worker and a
512-MiB memory target. No generated state or runtime change was required.
That CI run's application acceptance package passed in 1246.755 seconds, and
its other six jobs passed. The full run remains a failure because of the
manifest test.
