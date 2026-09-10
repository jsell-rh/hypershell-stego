The generated CLI has an offline `version` command. It returns JSON with
`application` and `compiler` records. Each record contains the Go version,
target OS and architecture, main module version, VCS type, source revision,
and source state (`clean`, `modified`, or `unknown`).

STEGO supplies the metadata reader, report format, and command. Hypershell
sets `Application.VersionCommand` to true. It adds no metadata parser or
build-report process. The compiler record is fixed during generation. The
application record comes from the built executable. The saved STEGO state
contains the same compiler record as the generated CLI.

The command needs no configuration, token, API, or source checkout. It accepts
no arguments. The reference CLI prints a release label; this variant prints
structured build metadata. A missing revision is unknown. It is never inferred
from the current working directory or the compiler pin at runtime.

`TestGeneratedCLIVersion` builds and runs the application CLI from a different
directory with no configuration file. It checks the application record against
the binary's Go metadata. It checks the compiler record against the pin and
saved state. It also checks rejection of extra arguments. STEGO tests build a
separate CLI fixture from clean source, changed source, and source with VCS
stamping disabled. Each binary retains its report outside the source directory.

The record is diagnostic data. It is not an artifact digest or a signature.
Two changed builds from one commit can have identical records. Complete input
manifests, compiler artifact verification, and controlled release builds remain
open work. The module version does not replace release policy.

Local validation passed with PostgreSQL and Keycloak required. Seven selected
acceptance tests took 100.494 seconds: version, catalog, apply, OIDC login,
Gateway, grants, and service accounts. They include access checks, event delivery,
and API or provider restart. CLI unit tests, contract tests, and `go vet ./...`
also passed. The old CLI failed the version test before regeneration.
The full application suite and Kubernetes provider workflows were not repeated
locally for this change.

Regeneration added two files and changed the CLI dispatcher and saved state.
The other 73 generated files did not change. The compiler build uses
`-buildvcs=true` in a fresh checkout, so a missing VCS tool causes a build error.
The regeneration check compares generated source, state, and dependencies.
