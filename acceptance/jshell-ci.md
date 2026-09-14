# Restricted jshell CI identity

The operator created `system:serviceaccount:stego-ci-access:hypershell-ci`
on 2026-09-14. Its private kubeconfig is in GitHub secret
`JSHELL_CI_KUBECONFIG` in `jsell-rh/hypershell-stego`. No operator credential
is in that secret. The selected context is `jshell-ci`.

The identity can create bounded Jobs in `stego-ci`, read their logs, and copy
source and results through Pod exec. It cannot create namespaces, change RBAC
or admission policies, request another token, or read another namespace's
Secrets. It can read only four declared test Secrets in `stego-ci`. Admission
also restricts Job Secret references to those names.

The service account is in `stego-ci-access`, which has a zero-Pod quota.
This separates its automatically renewed OpenShift registry credentials from
CI workloads. CI cannot read that namespace. A Job cannot select this account,
mount an API token, or select an undeclared Secret.

The workload namespace has a one-Pod quota, CPU and memory quotas, and no PVCs.
Each Job must have a deadline of at most 20 minutes, no retries, resource
limits, and a cleanup deadline. Containers must run without root or privilege,
with a read-only root filesystem and all capabilities removed. Restricted Pod
Security Admission also applies. The quota does not replace the shared lock:
other live test fixtures use separate namespaces.

## One live test at a time

`scripts/jshell_live_lock.py` uses Lease `stego-ci/jshell-live-test`.
Acquisition uses UID and resource-version checks. A nonempty holder blocks
another run, even after the Lease duration expires. Do not clear a stale
holder until its Job and other test resources are removed. Release requires
the same holder and an absent Job. Each runner must also verify its other
resources before release.

The count, CNPG database, and service deployment runners use this Lease.
The service runner stops its Job and Deployments before fallback cleanup.
It checks the test namespaces, permissions, policies, and CNPG ownership
journal before release. It retains the Lease if cleanup fails. This lock is
cooperative: a caller with direct Lease write access must use the helper.
The CI identity does not have the operator permissions that those live
fixture installers currently require.

## Credential renewal

The API token lasts one hour. The issued token expires at
`2026-09-14T19:38:11Z`. This is not an unattended CI credential service.
An operator can renew it with the following command. The script sends the
credential directly to GitHub and never prints it.

```sh
python3 scripts/prepare-jshell-ci.py \
  --context=default/api-jshell-8u58-p3-openshiftapps-com:443/johnsell \
  --output="$HOME/.config/stego/ci/jshell.kubeconfig" \
  --github-repository=jsell-rh/hypershell-stego
```

Use the separate file for checks. Do not change the active operator context.

```sh
python3 scripts/verify-jshell-ci.py \
  --kubeconfig="$HOME/.config/stego/ci/jshell.kubeconfig" --context=jshell-ci
```

## Evidence and remaining work

The actual CI credential passed 18 permission checks and 20 server dry-run
admission checks. No live Job was created by this check. While the CNPG run
held the Lease, a second holder and release by the wrong holder were both
denied. The safe result is in [the identity evidence](jshell-ci-evidence.json).
The CNPG run then removed its resources and released the Lease.

The service runner's lock and cleanup changes passed shell syntax checks and
Python parsing. They have not had a new full service deployment run.

The old GitHub Kubernetes jobs used kind. Their standard Docker nodes
are privileged, which conflicts with this repository's container rule.
See the [kind Docker provider](https://github.com/kubernetes-sigs/kind/blob/main/pkg/cluster/internal/providers/docker/provision.go).
This is a restriction of the current test setup, not a claim that kind cannot
support other setups.

The [Gateway API workflow](jshell-gateway-ci.md) now uses the restricted identity.
Operator-owned workload fixture provisioning, automatic credential renewal,
and the remaining workload fixture conversions are still open. Do not give the CI identity cluster-admin access to complete that work.
Do not treat the old failed CI jobs as passed. The first setup commits used `[skip ci]` to avoid starting the prohibited old
kind jobs. The old installer is now removed. Unconverted selectors fail
explicitly, so a later push cannot start privileged kind nodes.
