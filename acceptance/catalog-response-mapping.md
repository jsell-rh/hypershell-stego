# Catalog response mapping

This revision accepts runtime source `b145ee5b`. It uses STEGO compiler `6d68417d` to generate the protobuf response
mappings for ManagedCluster, GatewayRelease, and GatewayNetwork. Hypershell
declares their fields, kinds, and href prefixes. STEGO supplies checked
conversion and complete output field coverage.

The application retains catalog authorization, request validation, pagination,
watch delivery, and public error policy. The new tests compare every field with
the prior response shapes for absent, empty, and set optional values. They also
check invalid stored text without a partial response or a private error value.
The existing invalid timestamp, denied request, list, and watch tests remain.

Hosted generation checked all 430 generated and module files. It added one
mapping file and changed only the CLI compiler identity and three generation
records elsewhere. No compiler or application binary ran on the workstation.
See the [generation evidence](catalog-mapping-generation-evidence.json).

The generation input intentionally had the prior committed output. Three seed
checks stopped at the generated-file comparison before runtime tests. The
candidate includes the reviewed output and requires new source checks. The
archive review also corrected a header assertion to account for the compiler's
standard header before the generator header. The original records are retained.

The candidate passed 1,314 core cases across 376 test roots, including all
1,294 prior cases. All 120 focused response cases, five restart cases, and
11 provider test roots passed. Regeneration matched 430 generated and module
files, 421 output hashes, and 41 input hashes. The patch was empty. All seven
application images passed the independent content and signature checks. See the
[application evidence](catalog-mapping-application-evidence.json).

The complete live Gateway workflow passed all 11 required tests. Its browser
test took 561.52 seconds. Review checked the exact source and compiler, generated
files, REST and gRPC access, event delivery, restart, correlated logs, metrics,
and traces, and four browser images. All seven evidence readers completed
without error. Cleanup removed both test fixtures, released the shared Lease,
and preserved all 32 standing resources. See the
[browser evidence](catalog-mapping-browser-evidence.json).

Normal cleanup of one Gateway with 100 live accounts took an observed
32.70 seconds. The 30-second target remains unmet. These results do not prove
production capacity, live Kata isolation, native Sandbox packet isolation, or
OpenShell Sandbox execution. They do not explain the historical event timeout.

This acceptance covers catalog response mapping. Gateway and grant mapping,
stored JSON conversion, REST mapping, and the remaining enterprise requirements
are still open. Exact main checks are required after promotion.
