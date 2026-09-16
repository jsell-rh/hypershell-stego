# Common account lifecycle: cluster result

[Cluster run 35045870314](https://github.com/jsell-rh/hypershell-stego/actions/runs/35045870314)
passed on application commit `048ff55bf2a26dbfac3e238fec3352376fba6495` with
compiler `3e0bc22401729eb95bc1bd304455799a6d378b65` and Keycloak provider 0.13.0.
The complete browser test took 524.89 seconds. The saved source record includes
the exact account journal read fix and production provisioner integration.

The generated runtime completed login, Gateway creation and owner grants,
filtered access, denied requests, REST and gRPC, events, worker and API restart,
session key rotation, renewal, logout, and deletion. It provisioned actual
OpenShell Gateways and isolated logical PostgreSQL databases. Namespace loss,
SQL privilege faults, encrypted state recovery, and admission denials passed.
The expected worker instances exported metrics and correlated logs and traces.

The browser created an account through the common Keycloak lifecycle and the
authenticated SQL journal. Its one-time credential used the actual Gateway.
The test replaced the provisioner Pod, then completed reload, revoke, and delete.
Three account identities used the Gateway. Gateway deletion removed all three
provider clients and committed their cleanup audits before the API response.
Token issuance was then denied. A failed SQL cleanup retained its recovery state;
later cleanup removed the Gateway namespace, SQL state, role, and keys while
another Gateway and the supplied PostgreSQL server stayed available.

The Job UID was `423aaa99-9388-4daf-937b-5728fea570a3`. The saved cleanup record
confirms that test resources and Gateway allocations are absent. The restricted
CI namespace remains. Artifact `gateway-browser-35045870314-1` contains the source
record, Job result, logs, and cleanup evidence. Copies are retained under the
operator's persistent STEGO results directory.

| Artifact file | SHA-256 |
| --- | --- |
| `deployment.log` | `8ad86cbca547ca2e04344f81cd894169d963510b50e19a855d7e08cb55d80d35` |
| `evidence.tar` | `ec132a3896e3131ef491db395064a7700fcb6d9270b0192206c52b6a5e13b93f` |
| `job-status.json` | `2e664563a1b2415a9cc44b16bff2dd9b9768632002b666a63bceb25ef31b753b` |
| `cleanup.json` | `179eec722abf20ace66a7c69315ad9a7758b5305c67c476dddc3956f074b7232` |

The [rendered browser CI job](https://github.com/jsell-rh/hypershell-stego/actions/runs/35045870530/job/104635435888)
also passed in 104.29 seconds on the same source. Web-console and image checks
passed. The core suite completed with one failure in the legacy orphan fixture.
Its real-Keycloak account test passed in 53.36 seconds. The private state API test
passed in 6.04 seconds, including exact grants, an independent journal commit
under the Gateway lock, ciphertext storage, API restart, and retained cleanup.
The [cluster API gate](https://github.com/jsell-rh/hypershell-stego/actions/runs/35045870319)
also passed on the same production source. All 34 required tests passed. The
private state API test took 5.30 seconds; the API acceptance package took
184.467 seconds. Cleanup confirmed that the Job, Pods, and fixture resources
are absent. Artifact `gateway-api-35045870319-1` contains the exact source hashes,
test results, regeneration hashes, and cleanup record. Its `verification.json`
has SHA-256 `fd9013564f9b41b3072a84b05a85c229882a959ea85c08f00dee110043a1f060`;
its `cleanup.json` has SHA-256
`e6a192f8416a44fcebf085be2caffe87d62694df068e68636d4837ba85f8bf38`.

The legacy orphan fixture now explicitly removes the current ownership
attributes. Keycloak retains attributes omitted from an update. The test first
checks that mixed ownership is denied, then verifies the legacy fixture by
readback. Production ownership checks are unchanged. The corrected core suite
and the current CNPG gate are running on `a570dd0`. This evidence does not
establish production capacity,
external DNS enforcement, cross-process writer fencing, or database rollback
detection. The Kata Sandbox test remains deferred.
