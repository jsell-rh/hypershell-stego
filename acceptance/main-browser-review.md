# Main browser workflow

The [main browser run](https://github.com/jsell-rh/hypershell-stego/actions/runs/35479995475)
passed at source `0aa8f0d`. All 11 required tests passed. The complete browser
workflow took 718.29 seconds. Independent checks matched 1,548 source files,
421 repeated generation hashes, the signed compiler package, and the actual
compiler bytes in the bounded test Pod. See the
[workflow result](main-browser-evidence.json) and
[account identity result](main-browser-account-evidence.json).

The result covers real login, Gateway creation, owner and viewer access,
filtered lists, denied writes, REST and gRPC, generated events, process restart,
session key rotation, renewal, and logout. The rendered account workflow used
the actual Gateway. Namespace replacement and denied SQL cleanup preserved
unrelated Gateway and installation data. Provisioner restart denied new
accounts during the outage and preserved SQL and credential identities after
recovery. Worker and browser telemetry checks passed.

Four saved views were inspected. They show a healthy Gateway, an active
workspace, invalid JSON with submission disabled, and selected valid policy
text. The test did not submit the policy. No layout defect was seen at the
tested viewport. This is not a full accessibility or browser compatibility
review.

The normal final deletion used 100 accounts created through REST, each with
verified token issuance. All 100 rows and journals closed; provider clients and
users were absent; and all 100 cleanup success audits were present. The scope
was sealed. Namespace, database, role, key, binding, and final-state checks
passed. The supplied PostgreSQL server and installation data remained.

The observed upper bound was **53.949664261 seconds**. The prior candidate
observed 53.547650006 seconds. Neither observation proves the 30-second target.
The reads are sequential and do not locate the delay. The separate phase
observation candidate is ready for a live test after the remaining main gates
and joint cleanup check pass. No production capacity claim is made.

An independent read found no browser workloads, fixtures, or allocated
resources. All 32 standing browser installation objects matched the adopted
configuration. The separate API test was active in `stego-ci` at that time.
Its cleanup and release of the shared lease require the joint audit before
another live test. This browser result does not claim that the API gate or
full main suite passed. Live Kata isolation and RDS failover remain outside
this result.
