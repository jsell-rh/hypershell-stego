# Gateway and grant REST mappings

This change moves field conversion for Gateway and RoleBinding responses
into STEGO. The application selects current observations, resolves the creator,
and checks access. The common converter checks and copies the resulting values.

All 24 Gateway fields and all nine grant fields have explicit mappings. The DNS
list has the same byte and item bounds as the gRPC response. Grant scope must
match the OpenAPI enum. An invalid row returns a private error with no partial
response. List selection and access checks run before conversion.

The existing acceptance checks use separate response decoders. Their assertions
remain unchanged. New checks cover field presence, owned storage, observation
selection, invalid stored values, list bounds, access, restart, and recovery
after repair. The required live API set also includes the complete grant
REST/gRPC/event/restart workflow.

CI run 35572421767 generated this output with the verified compiler release
f972410b9709b7b39a09bc8f9a03dbcc0747c89d. Source review checked all changed
files. The OpenAPI models and 428 other generated or module files are unchanged.
The generated file hashes and all 41 recorded source inputs match.

The complete application workflow passed for source `f1366d1`. All 14 hosted
check groups passed. The full suite passed 1,404 cases across 393 top-level tests
and retained all 1,370 prior cases. The response suite passed 199 cases. Exact
regeneration matched all 434 generated and module files, 425 output hashes, and
41 input hashes.

[API run 35574985157](https://github.com/jsell-rh/hypershell-stego/actions/runs/35574985157)
passed all 55 required roots. It checked source identity, committed and repeated
generation, the authenticated compiler bytes, test limits, and cleanup.
[Browser run 35575801502](https://github.com/jsell-rh/hypershell-stego/actions/runs/35575801502)
passed all 11 required roots. Its main scenario took 550.42 seconds. Review
checked 1,728 source files, 435 live generation hashes, seven signed images,
correlated logs, metrics and traces, and all four screenshots.

The rendered workflow covered login, Gateway creation, owner grants, REST/gRPC
access, filtered and denied requests, event delivery, database and worker
restart, account lifecycle, browser sessions, and namespace finalization.
Cleanup removed both test fixtures and all owned allocations, released the
shared test lock, and left all 32 standing resources unchanged.

The measured Gateway had 100 service accounts. Complete cleanup had an observed
upper bound of 32.203 seconds, above the 30-second target. This result does not
prove the 100-Gateway capacity target, RDS failover, live Kata, or upstream
OpenShell Sandbox execution. The remaining enterprise requirements and gRPC
grant mapper adoption remain open. See the
[workflow evidence](rest-gateway-grant-workflow-evidence.json).
