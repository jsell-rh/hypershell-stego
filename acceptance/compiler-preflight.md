The compiler pin is `021b2d1986adddeaace234feeae9feda5f72d8d1`.
STEGO now checks resolved component inputs before it renders any component.
The application defines its registry composition, factory paths, and protobuf
contracts. The common check and input snapshots belong to STEGO.

The original probe used an invalid factory under `out/`. Validation passed,
but planning rejected it. The new compiler rejects that input through validate,
plan, and apply for the CLI, HTTP, and gRPC factories. The probes used separate
temporary copies of application `d0ad397d1d12fbfc99fffabb2bc2fdc280cbc8d5`.
Output, state, and dependency hashes remained unchanged after the rejected
commands. Valid validation and planning also passed.

The compiler tests also cover malformed protobufs, missing inputs, required
component peers, and a declared file that changes during generation. A later
component still receives the captured bytes that passed its check, and the
changed file prevents a usable plan. The full compiler race suite passed with
PostgreSQL required. The common checks include authentication, storage, REST,
controllers, search, event delivery, and provider clients.

This variant updates the compiler pin and component patch versions. Regeneration
changed only `.stego/state.yaml` and the compiler build record in
`out/cli/command/version.go` among 90 generated, state, and dependency files.
All other generated source and dependency hashes stayed unchanged. There is no
runtime policy or schema change in this compiler update.

For the initial preflight change, ten local calls after warm-up averaged
22.755 ms for validation. Planning
averaged 139.224 ms. The previous compiler averaged 7.669 ms and 131.468 ms.
Other test processes were active. These measurements compare development costs;
they do not establish a production capacity target.

C1 remains open. A separate controller namespace probe used `bad-name`, which
is a canonical path but not a valid Go package name. Validation passed, and
planning rejected the package declaration. The current compiler rejects this
case in all three commands before rendering. The complete audit remains open. See
[component preflight](https://github.com/jsell-rh/stego/blob/main/specs/compiler-preflight-gap.md).

For the initial preflight update, contract tests passed under race detection
in 1.517 seconds. The offline CLI
version test passed in 1.625 seconds and verified the new clean compiler pin
against the generated build record and saved state. Vet passed. Repeated pinned
generation preserved all 90 output, state, and dependency hashes. The full
application and Kubernetes suites were not repeated locally for this metadata
update; remote CI runs those gates after the commit.

The current pin adds shared Go package-name and import-path checks. Twelve
library generators check derived package names; the CLI keeps valid package
container paths such as `cli-tools`. Protobuf checks its actual resolved Go
mapping. Controller and protobuf build tests preserve valid nested and
hyphenated paths. The compiler's full race suite passed with PostgreSQL required.
See the [Go package-name contract](https://github.com/jsell-rh/stego/blob/main/specs/go-package-names.md).

A new probe used application `25c09dd53cb36be90ddaf17945e8b3527f50a5e6`.
All three commands rejected controller names `bad-name`, `workers/type`,
`workers/main`, and `workers/café`. Validation and planning accepted
`go-services/controller` and the original declaration. Rejected commands left
output, state, and dependency hashes unchanged. The positive nested-path probe
checks planning; it does not claim that existing domain imports were changed.

Regeneration with this pin again changed only saved compiler state and the CLI
compiler build record among the 90 generated, state, and dependency files.
The application runtime source, schema, and dependency hashes stayed unchanged.

For the package-name update, contract tests passed under race detection in
1.519 seconds. The offline CLI version test passed in 1.644 seconds and checked
the current clean compiler pin against the executable and saved state. Vet
passed. Repeated pinned generation preserved all 90 hashes. The full application
and Kubernetes suites were not repeated locally for this compiler metadata update.
