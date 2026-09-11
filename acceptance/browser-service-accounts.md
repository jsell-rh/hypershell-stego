The rendered service-account workflow passed on 2026-09-11 with compiler
`0f52bab9021379f877aa355f0b9c8c41d7514e85`. The full deployed browser test passed
in 157.92 seconds; its race-enabled package passed in 158.967 seconds. The
contract checks passed in 1.056 seconds. The Secret-error privacy check passed.

The final frozen source is `/tmp/stego-accounts-source-v8b1a_ny/application`.
Results are in `/tmp/stego-service-results.BjXxoWkq`. The Job reached `Complete`
and its exit record is zero. All 219 output, state, and dependency hashes match
both generation passes, the post-test check, and the checkout. The test used
separate generated API and console Deployments. Their repeated builds produced
these image digests:

- API: `sha256:d495bb6718007929dbdcd35c4db1a40f490477ed5bc6b33a6df2dc360e61d30e`
- Console: `sha256:796b85e59db27b8c2c6dfa79207c161d1d23ae607063ceed41e9657cb68edb1b`

The test kept the existing Gateway REST, gRPC, owner-grant, access, event,
restart, key-rotation, telemetry, renewal, and confirmed sign-out checks. The
new account check verified one-time delivery, real signed token claims,
credential omission from reads and durable data, reload, cancelled and confirmed
revocation, deletion, and credential omission from runtime logs. The saved
`evidence/browser-artifacts/accounts-delete.json.png` was reviewed and shows
an empty account list after deletion.

The Job used the saved jshell context and a dedicated namespace. The Go test
container had limits of one CPU and 3 GiB of memory. Chromium had one CPU and
1536 MiB. The namespace had a six-CPU and 8-GiB quota. The Job limit was
30 minutes; the Go acceptance limit was ten minutes. No container was privileged.
No local performance or stress test ran. All four run namespaces and the small
write-probe namespace were removed; their absence was verified. New application
CI remains a separate check.

The rendered service-account check extends
`TestGeneratedKubernetesBrowserGatewayWorkflow` and the process-level
`TestGeneratedBrowserGatewayWorkflow`. It runs only when the rendered browser
is required. It uses the captured React console, generated browser backend,
generated API, generated secure RPC transport, and real Keycloak operations.

The test uses a separate Gateway fixture with a known workload readiness
observation. Its owner has the real console user's issuer and subject. The
fixture administrator binds the Gateway audience in Keycloak. This is a
service-account lifecycle test. It does not claim to provision an actual
Gateway workload. The existing Gateway creation, grant, event, access,
regeneration, key rotation, and restart checks still run before it.

The browser signs in as the owner and creates an admin service account through
the console. The secret must be masked. The user must acknowledge its storage
before completing setup. A private temporary file passes the one-time value to
the test process. That file is outside the saved browser artifacts and is
removed before credential verification. Failure handling must not save the DOM
or a screenshot while the one-time secret can be present.

The real identity provider must issue a signed token from the displayed
credential. The test verifies its issuer, Gateway audience, subject, and role.
The subject must match the stored account. A different Gateway audience must
reject the token. Client credentials must not produce a refresh token. List and
detail responses must omit the secret. Application account records, audit
records, outbox data, and runtime logs must not contain it.

After a page reload, the setup view must say that the secret is no longer
available. Cancelled revocation must leave the account ready. Confirmed
revocation must stop new token issuance. Confirmed deletion must remove the
account from the UI list and return HTTP 404 for its detail. A screenshot is
saved only after deletion. Previously issued tokens can remain valid until
expiry; this check does not claim immediate token revocation.

The provisioner runs in the bounded test Job. Its RPC listener uses verified
TLS and a service-token caller allowlist. In the deployed check, the generated
API reaches it through the fixture Service on port 19094. The fixture network
policy permits that port only from the test Job and generated API Pod. The
initial provisioner process had a handwritten entry point. The
[RPC process check](rpc-process.md) uses this workflow to check its replacement
with a generated STEGO entry point.

Run the complete check from a frozen source copy:

```sh
STEGO_TEST_CONTEXT=default/api-jshell-8u58-p3-openshiftapps-com:443/johnsell \
STEGO_TEST_BROWSER_DEPLOYMENT=1 scripts/check-service-deployment.sh
```

The initial build was stopped before the browser test after review found an
incorrect role-button selector. No test pass is claimed for that source.
Its frozen source is `/tmp/stego-accounts-source-47gd6by2`. The pre-stop Job
record and build log are in `/tmp/stego-accounts-cancelled-first`; wrapper
records are in `/tmp/stego-service-results.Y25ubsVJ`. The Job was removed, its
Pod stopped, and namespace deletion was verified. The next source uses the
actual `OpenShell role` button label. The script and all twelve embedded browser
scripts passed syntax checks before the next frozen run.

The next run failed at 99.94 seconds during a write of the console runtime
Secret, before the account UI steps. Its race-enabled package failed in
100.012 seconds. The failure category was not known. Results are saved in
`/tmp/stego-service-results.WKrn0mIq`; the namespace was removed. Five isolated
writes with a namespace-limited test identity then passed. That probe did not
reproduce the error and does not establish its cause. Its records are in
`/tmp/stego-secret-write-probe-l8vmus26`; its namespace was also removed.

The fixture now captures Secret-command transport metadata in memory and emits
only known error labels or numeric HTTP status. It never prints the raw
Secret-command response. A regression check verifies that private values do
not enter those categories. The browser deployment command includes that check.

The third run passed Pod replacement and key rotation, then created an account
and reloaded its page. The new test failed because it treated the public
`credential_type: client_secret` value as a secret field. The captured API
contract requires that type value. The check now rejects the `client_secret`
property and the actual secret value. It still permits the required type
metadata. The failed test took 144.47 seconds; the package took 144.512 seconds.
The diagnostic privacy check passed. Results are in
`/tmp/stego-service-results.gfAsEue4`. No account workflow pass is claimed for
that run.

The prior application CI run `34653702744` found a separate test fault. Its
service-image Job reached its time limit in `test-wait-service-result.sh`,
before any image build. The completion test used a shell function as its fake
cluster command. GNU `timeout` could not execute that function, so the test
kept polling. Commit `4ca15ce` uses a Bash fixture process with exported test
inputs. All five completion cases passed with the real timeout wrapper. CI now
limits that test step to 15 seconds. Full CI for the fix remains separate from
the bounded cluster result.
