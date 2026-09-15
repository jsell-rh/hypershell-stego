# Public Gateway TLS

The pinned Gateway image uses source revision
[`681c9b2`](https://github.com/opendatahub-io/openshell/tree/681c9b2d8b9887f230cee4871bdbdbc9a362dfc8).
Its image index is
`sha256:a80b79e514826e8d57ea137749cf18a6e7f3d92e26bfefe005f3a9c4a55b8bdd`.
The selected Linux AMD64 image metadata records that source revision. The
[`TlsConfig` source](https://github.com/opendatahub-io/openshell/blob/681c9b2d8b9887f230cee4871bdbdbc9a362dfc8/crates/openshell-core/src/config.rs)
and [TLS resolver](https://github.com/opendatahub-io/openshell/blob/681c9b2d8b9887f230cee4871bdbdbc9a362dfc8/crates/openshell-server/src/tls.rs)
support separate internal and external certificates on the same listener.
The resolver selects the external certificate by SNI.

The workload controller accepts these installation settings:

- `HYPERSHELL_GATEWAY_PUBLIC_DOMAIN`: the DNS suffix for public Gateway hosts.
- `HYPERSHELL_GATEWAY_PUBLIC_ISSUER`: the cert-manager ClusterIssuer for public TLS.
- `HYPERSHELL_GATEWAY_PUBLIC_ROUTER`: the selected Route status router name.
- `HYPERSHELL_GATEWAY_PUBLIC_CA_FILE`: an optional absolute certificate bundle
  path. An absent path selects system trust. A supplied file selects only its
  certificates. Private keys and files larger than 512 KiB are rejected.

A public host is `gw-<assigned-namespace>.<public-domain>`. The controller creates
`openshell-public-tls` with that hostname and the selected issuer. STEGO's
`kubernetes-client` 1.8.1 verifies the Secret type, namespace, name, owner,
certificate chain, hostname, server use, expiry, and private key against the
configured trust. It does not take public trust from the workload Secret's
`ca.crt` field. The certificate mounts separately from the
internal certificate. Certificate changes affect the Deployment configuration
hash. Internal clients keep their Service hostname and private CA.

The domain, issuer, and router are required together. When none is set, the existing
internal TLS path remains in use. CA changes require a controller restart.

Focused checks passed for trusted and untrusted issuers, wrong hostnames,
incorrect keys, expired certificates, client-only certificates, private-key
rejection in the trust bundle, separate mounts, and internal names. The
workload controller now creates an owned passthrough Route and waits for the
selected router to admit it. STEGO verifies the Route target and status. The
controller then uses STEGO's TLS RPC probe to match the assigned certificate,
check Gateway health, and require an unauthenticated identity request to fail.
It reads the Route again and rejects a changed UID or revision. These calls use
generated Go protocol types from the pinned Gateway contracts.

The controller commits status and address together against the original API
revision. Both `observe.workload` and `observe.endpoint` grants are required.
A failed check clears the address with degraded status. When public exposure is
disabled, the controller continues Route deletion after the API address is
cleared. It does not treat a submitted deletion as proof of absence.

Focused checks cover creation, pending admission, ownership, extra backends,
probe failure, Route replacement, stale Route revisions, continued deletion,
TLS peer identity, health responses, denied identity calls, metadata privacy,
and status/address publication. The complete acceptance package compiles, and
both services regenerate without drift. These checks do not prove public
connectivity or certificate rotation in a live Gateway.

Compiler `0953fcc` supplies the shared check. Its generated runtime checks pass
for an independent widget service. Hypershell uses that check directly; it has
no separate public certificate verifier. Focused application checks and repeated
generation pass. [Full compiler CI](https://github.com/jsell-rh/stego/actions/runs/34973613718)
passed, including both independent examples and SQL provisioning. Complete
application CI and public connection results remain required for this revision.

The 1.8.1 runtime also rejects private-key blocks and extra text in `tls.crt`.
A regression check reproduced this defect in 1.8.0 before the correction.
Hypershell tests this rejection through its real Kubernetes client fixture.

STEGO `kubernetes-service` 1.10.0 supplies optional external destinations.
The worker declares `gateway-public`. The operator supplies each router IP and
port at render time, for example `--egress gateway-public=192.0.2.3:443`.
This produces one `/32` rule for that address and TCP port. Without the binding,
the renderer adds no public egress rule. Required Kubernetes and PostgreSQL
bindings remain required. A direct render check verifies that this one rule
is the only difference between the enabled and disabled policies. No network
connection was made by that check.

The complete live gate must configure the issuer, trust, router, worker grants,
and the test actor's access. It must verify network enforcement, public RPC,
certificate rotation, restart, and cleanup. API run `34975653347` and browser
run `34975653307` stopped before test creation because the CI credential had
too little time left. They provide no application result for these changes.

## Public browser test inputs

The existing browser workflow accepts an explicit public profile. Set
`JSHELL_GATEWAY_PUBLIC_CONFIG` in the `jshell-ci` GitHub environment to a JSON
object with these fields:

- `domain`: the DNS suffix routed to the selected ingress controller.
- `issuer`: the existing cert-manager ClusterIssuer name.
- `router`: the selected Route status router name.
- `ca_pem`: the independently supplied certificate authority bundle.
- `endpoints`: 1 to 16 router IP and port pairs, each on TCP port 443.

For example, an endpoint can be `192.0.2.3:443` or `[2001:db8::3]:443`.
These are example addresses. Use the actual addresses visible at the cluster
network policy boundary. Supply certificates only. Do not put private keys or
credentials in this configuration.

Start the `Gateway browser workflow on jshell` workflow manually with
`public_gateway` set to `true`. The test fails if the configuration is absent
or invalid. For a direct run, set `STEGO_TEST_GATEWAY_PUBLIC_CONFIG` to the
configuration file and `STEGO_TEST_REQUIRE_PUBLIC_GATEWAY=1`. Normal push runs
retain the internal Service profile. They cannot establish a public pass.

The fixture mounts the public inputs from a separate key in the existing test
trust ConfigMap. Database trust stays in its own key. The test grants the
assigned worker both workload and endpoint observation rights for one cluster.
It uses the generated network renderer for the public worker destination. The
Job deadline, resource limits, shared Lease, and cleanup checks stay in effect.
The fixed cluster installation must match the current generated policy before
a test can start.

In the public profile, the existing real Gateway RPC sequence uses the
controller-published address and the supplied trust. It checks invalid tokens,
a foreign audience, an ungranted user, owner provider writes and reads, and
provider data after Gateway Pod replacement. Subsequent worker restart,
namespace replacement, viewer recovery, and service account calls use the same
public connection. The rendered browser must show a connection command with
the expected endpoint after API and console restart.

`gateway-public-rpc.json` records the initial RPC and Pod replacement stage.
`verify.json` records the rendered connection check. These files are partial
evidence until the complete Job, regeneration, and cleanup checks pass.
Focused fixture input and manifest tests pass, the acceptance package compiles,
and the WebDriver script passes its syntax check. No live public result exists
for this source. The public profile includes the explicit renewal test below. Its live result is still required.

The public profile also restarts the worker with its optional router egress
binding removed. Required Kubernetes and PostgreSQL bindings remain. Every
Gateway must lose its published address and report degraded status. The test
then restores the generated policy and waits for healthy addresses. It checks
SQL credential identities and provider data through public RPC. The partial
result is `gateway-public-network-recovery.json`. The input check passes and
the acceptance package compiles. Live network denial and recovery remain
unverified until this profile completes on the cluster.


## Public certificate renewal

The public profile requests renewal through the Certificate status operation
used by [cmctl at `7376e810`](https://github.com/cert-manager/cmctl/blob/7376e810c4e9d748dcc71d1522df7723739f6b4f/pkg/renew/renew.go).
The test requires an owned, ready Certificate with the expected issuer, DNS
name, and `rotationPolicy: Always`. It submits one status update with the
observed UID and resource version. A conflict fails the test. It does not
retry that write against newer state. This follows the
[cert-manager renewal contract](https://cert-manager.io/docs/usage/certificate/).

The fixture adds read access to `openshell-public-tls` and update access only
to that Certificate's status in an assigned Gateway namespace. It grants no
Certificate specification writes or Secret deletion. The existing namespace
allocator generates and bounds these permissions. Live access reviews check
the named status grant and denied writes to the internal certificate, the
Certificate specification, the retained state namespace, and another namespace.
The fixed operator installation must be updated to these generated inspection
rules before this test can run.

The test requires a newer certificate revision, a different certificate and
public key, a complete Deployment rollout, and a fresh TLS connection pinned
to the new certificate. A connection pinned to the old certificate must fail,
and a second connection with the new pin must pass. It checks anonymous denial
and a fresh authenticated owner read. SQL database and role OIDs, SQL credentials,
provider data, and internal TLS material must remain unchanged. Certificate,
Secret, and Deployment UIDs must remain stable. The generated client handles
TLS validation, bounds, deadlines, and token file reads.

The stage record is `gateway-public-certificate-rotation.json`. It contains
public certificate hashes and resource identities. It contains no private keys
or credentials. It proves nothing until the live stage and complete workflow
pass. The test permits a controlled Gateway restart; it does not establish
uninterrupted service. Focused renewal request checks and the generated fixture
permission checks pass. A live renewal result remains required.
