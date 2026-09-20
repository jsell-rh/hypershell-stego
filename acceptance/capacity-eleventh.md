# Eleventh account capacity result

[Run 35479995472](https://github.com/jsell-rh/hypershell-stego/actions/runs/35479995472)
passed at `0aa8f0d`. Account cleanup took **16.881928137 seconds**. The account
scope sealed at **16.492664238 seconds**. All 100 selected account rows and
authenticated journals closed. All 100 provider clients and users were absent,
and all successful cleanup audits were present. The 9,900 background account
rows and 10,007 other clients retained their saved state.

Independent checks matched the source archive, binary hashes, compiler, 34 RPC
status cases, resource limits, and test cleanup. All six capacity fixture files
match the tenth run. The check used one CPU, 1 GiB memory, no swap, and a fixed
time limit. See `capacity-eleventh-evidence.json`.

The tenth account-only run took 21.2756 seconds. Both results are below the
30-second account-cleanup target in this fixture. They do not prove complete
Gateway workload and SQL cleanup at production capacity, concurrent cleanup,
or larger installations. The separate complete Gateway workflow recorded a
53.5477-second upper bound; its 30-second target remains unproved. Phase
observations are being added to locate waiting and verification time.

The main regeneration and adapter results were also independently checked.
All 420 archived generated files match committed source. The source archive
and compiler installation identity match the selected release. This artifact
contains the installation record, not the raw compiler build and signature
records. The separate live observer checked those actual records in the Pod.
All 63 required adapter tests, six allocation cleanup tests, 28 collection
cases, five status cases, and eight completion cases passed. See
`main-regeneration-adapter-evidence.json`. The full main and live main results
remain separate and are not qualified by these records.
