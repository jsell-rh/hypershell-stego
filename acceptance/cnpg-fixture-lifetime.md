# CNPG fixture lifetime

The database fixture previously expired after 25 minutes. The application Job
allows 30 minutes and starts after database readiness. The database could thus
expire before the application's allowed test window ended.

The database now uses the same 40-minute resource limit as the CNPG operator.
That budget covers the 30-minute application Job, the six-minute database
readiness wait, and a three-minute cleanup reserve. The credential gate still
requires 45 minutes. The application deadline remains 30 minutes. CPU, memory,
storage, instance counts, and immediate lifetime-owner cleanup are unchanged.

One common constant supplies the database and operator templates and their
admission limits. A regression test reads the application Job's actual deadline
and checks the dependency budget against it. It also checks both generated
templates and their admission policies.

Run 35224348179 passed with its original source and limits. See the
[complete workflow record](postgres-signal-cnpg.md). That result does not prove
the new fixture lifetime. The installation update below supplies the required
cluster configuration for a repeat.

Hosted run [35226222533](https://github.com/jsell-rh/hypershell-stego/actions/runs/35226222533)
passed at source `52f1ecb`. Independent checks matched the source archive and
all 40 tests: 18 CI boundary tests, 14 database fixture tests, and eight
credential tests. The dependency-budget regression passed. See the
[evidence record](cnpg-fixture-lifetime-evidence.json). These are offline checks. The later installation check below verifies the
cluster policy. A complete workflow with the new budget remains separate.


The installation update completed at 13:35:20 UTC on 2026-09-17, after the
previous application and admission tests ended and their cleanup was checked.
The database policy and immutable configuration now permit 2,400 seconds.
The policy UID, other installation identities, operator template, and remaining
configuration data were preserved. Server dry-runs accepted 2,400 seconds and
rejected 2,401 seconds through the required policy. The live-test Lease was
released. No Job or Pod was started by this update. See the
[installation record](cnpg-fixture-lifetime-installation.json). A full Gateway
run with the new fixture budget remains required.
