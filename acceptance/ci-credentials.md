# CI credential lifetime

The jshell jobs use the `jshell-ci` GitHub environment and its
`JSHELL_CI_KUBECONFIG` secret. GitHub reads environment secrets when a job starts.
Repository secrets are read when a workflow run enters the queue, so a waiting
run can retain an old token after rotation. See the
[GitHub secret timing rules](https://docs.github.com/en/actions/reference/security/secrets).

The operator still issues a one-hour token for the restricted `hypershell-ci`
service account. CI cannot renew this token or obtain an operator credential.
The environment changes when GitHub reads the secret; it does not change
cluster permissions or provide unattended credential renewal.

Before acquiring the shared Lease, each runner checks the explicit context,
verified HTTPS, token-only authentication, expected service-account subject,
issue time, and expiry. It requires the following remaining time:

| Gate | Minimum time | Basis |
| --- | --- | --- |
| Gateway API | 25 minutes | 20-minute Job and five-minute collection margin |
| Rendered browser | 35 minutes | 30-minute Job and five-minute collection margin |
| Supplied CNPG browser | 45 minutes | Browser budget and ten-minute installation margin |

These are start requirements. They do not guarantee that cluster cleanup will
finish within the margin. Cleanup failures retain the Lease and require operator
inspection. Cleanup can use any remaining valid token time; it does not require
a new full test budget. Parsed JWT claims are not authentication evidence. The
Kubernetes API authenticates every request and enforces the restricted roles.

The CNPG runner checks its explicit CI kubeconfig before it creates the results
directory, acquires the Lease, or installs the operator and database. It must not
use the operator context for this check. After server installation, the inner
browser runner checks its 35-minute budget again. A long installation can still
stop at that second check; the operator must then complete normal cleanup.

Renew the environment secret before the remaining time falls below the required
budget. Use the existing operator context explicitly:

```sh
python3 scripts/prepare-jshell-ci.py \
  --context=default/api-jshell-8u58-p3-openshiftapps-com:443/johnsell \
  --output=/home/jsell/.config/stego/ci/jshell.kubeconfig \
  --github-repository=jsell-rh/hypershell-stego \
  --github-environment=jshell-ci
```

The environment must already exist. The helper writes the private kubeconfig
atomically and sends the secret through standard input to GitHub CLI. It prints
the identity and expiry, never the token. It preserves an active cluster Lease.

Earlier workflow revisions still read the repository secret. Rotation cannot
change their queued snapshot. A waiting run can be cancelled before it starts
and rerun on the same commit after rotation. Keep that cancellation in the run
history, require the replacement result, and do not treat it as a passing test.
Do not interrupt an active Job to rotate credentials.

Eight small checks cover the time boundaries, malformed credentials, wrong
identity, excessive lifetime, unverified TLS, private inspection failures, and
all three runner entry points. The CNPG regression failed before the early
check was added. Near-expiry credentials stop each runner after only the local
kubeconfig read. The renewed private credential also
passed the browser lifetime check. The
[environment-backed browser workflow](browser-environment-credential-evidence.json)
passed at `f900d5a`, including the complete application workflow and automated
cleanup. Source and generation hashes matched the recorded revision.

Full application CI at `f900d5a`
[completed](https://github.com/jsell-rh/hypershell-stego/actions/runs/34952975998).
Core acceptance passed in 1316.744 seconds. Ordinary browser, console, and
service-image jobs passed. The overall run failed on the unfinished CNPG and
Sandbox jobs. The environment-backed live browser run `34952975989` passed.
Its older queued API run was cancelled in favor of the newer API run
`34955320269` at `eb53ea7`. The newer duplicate browser run was cancelled while
pending; the next CNPG run will also exercise its credential reader. Neither
cancellation is a passing result.

Full application CI at `eb53ea7`
[also completed](https://github.com/jsell-rh/hypershell-stego/actions/runs/34955320193).
Core acceptance passed in 1091.085 seconds. Ordinary browser, console, and
service-image jobs passed. The overall run failed because the CNPG and Sandbox
entry points still require their restricted cluster execution paths. The
separate restricted API run remains active. The prepared CNPG run must wait for
its completion and cleanup.
