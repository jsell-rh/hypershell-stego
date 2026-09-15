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

The next installation gap is the worker's public router egress binding. A render
with `--egress gateway-public=192.0.2.3:443` fails because that destination is not
declared for the worker. The address was test input; no connection was made.
The public path must stay unready until a generated network policy permits the
operator-selected router. Optional public exposure must retain a closed network
policy when no binding is supplied. The complete live gate must also configure
the issuer, trust, router, worker grants, and the test actor's access.
