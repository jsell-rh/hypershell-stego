The compiler pin is `0b959fb4ffe7842942d2946f82d2e83d093c969b`.
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

In ten local calls after warm-up, validation averaged 22.755 ms and planning
averaged 139.224 ms. The previous compiler averaged 7.669 ms and 131.468 ms.
Other test processes were active. These measurements compare development costs;
they do not establish a production capacity target.

C1 remains open. A separate controller namespace probe used `bad-name`, which
is a canonical path but not a valid Go package name. Validation still passed,
and planning rejected the package declaration. The compiler review records this
next defect and the remaining audit. See
[component preflight](https://github.com/jsell-rh/stego/blob/main/specs/compiler-preflight-gap.md).

Contract tests passed under race detection in 1.517 seconds. The offline CLI
version test passed in 1.625 seconds and verified the new clean compiler pin
against the generated build record and saved state. Vet passed. Repeated pinned
generation preserved all 90 output, state, and dependency hashes. The full
application and Kubernetes suites were not repeated locally for this metadata
update; remote CI runs those gates after the commit.
