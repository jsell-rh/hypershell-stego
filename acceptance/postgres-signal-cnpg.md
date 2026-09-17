# Gateway workflow with credential preparation telemetry

[Run 35224348179](https://github.com/jsell-rh/hypershell-stego/actions/runs/35224348179)
passed at source `d9cd7e3d6a78b24577f1a0f8f0c435352aa8800d` with published
compiler `b8fdfd6946a740430a2a4f41cf9aaae03a6d6793`. All 11 required tests
passed. The main Gateway workflow took 728.81 seconds.

Independent checks matched all 1,432 source files, 416 generated file hashes
before and after tests, the published compiler package, and the console image.
The workflow checked real login, Gateway creation and owner grants, REST and
gRPC access, filtered lists, denied requests, event delivery, account lifecycle,
and durable deletion. It also checked recovery after database Pod replacement,
workload namespace replacement, and provisioner restart. SQL identities,
credentials, provider data, and access rules remained valid after recovery.

Worker logs, metrics, and traces passed their correlation checks. The generated
browser runtime supplied complete startup signals for three management-console
instances and three Gateway-console instances. PostgreSQL credential preparation
was included in the required signals. The previous failed telemetry check
remains recorded; this result does not change that failed run.

All three retained dashboard images were reviewed. They show the loaded
workspace, invalid JSON rejection, and text selection in the policy editor.
No policy was submitted. Independent cluster reads confirmed that test runtime,
allocations, private fixtures, and both test volumes were absent. The live-test
Lease was empty.

The application Pod waited 246 seconds to schedule. Its placement and limits
were not changed. The database fixture reached its original deadline near the
end of the run. The separate fixture-budget correction remains necessary to
cover the full application time allowance in later runs.

See the [evidence record](postgres-signal-cnpg-evidence.json). This result does
not prove live Kata isolation, production capacity, or all remaining enterprise
requirements.
