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

The domain and issuer are required together. When neither is set, the existing
internal TLS path remains in use. CA changes require a controller restart.

Focused checks passed for trusted and untrusted issuers, wrong hostnames,
incorrect keys, expired certificates, client-only certificates, private-key
rejection in the trust bundle, separate mounts, and internal names. The
application still needs Route creation, network permissions, a verified public
TLS and RPC probe, and address publication. This change does not prove public
connectivity or certificate rotation in a live Gateway.

Compiler `0953fcc` supplies the shared check. Its generated runtime checks pass
for an independent widget service. Hypershell uses that check directly; it has
no separate public certificate verifier. Focused application checks and repeated
generation pass. Full compiler and application CI results remain required for
this revision.

The 1.8.1 runtime also rejects private-key blocks and extra text in `tls.crt`.
A regression check reproduced this defect in 1.8.0 before the correction.
Hypershell tests this rejection through its real Kubernetes client fixture.
