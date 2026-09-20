# Common PostgreSQL recovery compiler

Consumer `d24e9da` selects signed STEGO compiler `3be6bce`. The common provider
can resume removal when PostgreSQL has marked an owned database invalid.
It verifies identity and ownership, removes the database and roles, and then
records completion. Component version is 1.5.1. See the
[common recovery contract](https://github.com/jsell-rh/stego/blob/3be6bce4167c54258bb4c154ad2cfb5e7e0d00d0/specs/postgres-invalid-drop-recovery.md).

The compiler passed all six main jobs, 34 race-tested packages, both examples,
and 17 focused recovery cases. The signed release passed source, module,
build-policy, signature, and installation checks. Its bytes match the qualified
branch build. The recovery fixture constructs the invalid catalog state;
physical crash and checkpoint cancellation require separate evidence.

Hosted generation at pin commit `48e707b` passed independent review. All 420
source and module files matched the expected file set. The review checked
compiler identity, common and local registry digests, input manifests, full
state records, and the complete provider template. The imported patch contains
five files: the provider, CLI compiler metadata, and three state records.
The new provider file matches SHA-256
`77fd3a0af7fef290d30bddcc3b11a3f352b93a9a3561de76a8346ef6ad3498fe`.

All 126 Gateway console source and module files match the prior qualified
module. The current module reference remains valid for those bytes. The new
application check verified the actual selected module, signed compiler, image
executable, and pulled image identity. See the
[compiler adoption record](postgres-recovery-compiler-evidence.json).

The consumer includes the qualified
[fixture cleanup correction](database-fixture-cleanup.md) and the existing
pending-result controller change. All six hosted checks passed independent
verification. The full run passed 1,122 core cases across 354 top-level tests,
with all 1,103 baseline cases retained. Adapter, allocation, provider, journal,
and Gateway console checks passed. The trace evidence checks passed 99 cases.
The five conditional core exclusions and the deferred Kata job remain explicit
in the record. The [complete live workflow](postgres-recovery-live.md) now
passes independent verification for this exact source.

The live workflow verified REST, gRPC, access control, events, restart,
regeneration, all three telemetry signals, and complete Gateway cleanup.
Complete cleanup was observed at 31.373211219 seconds for 100 service accounts.
The separate API check passed all 52 required tests and repeated generation.
Joint cleanup passed. The [API evidence](postgres-recovery-api-evidence.json)
records the exact source, compiler, runtime limits, and cleanup proof.
The 30-second target and production capacity remain open. The live Kata test
remains deferred.
