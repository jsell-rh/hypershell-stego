Gateway controllers require `HYPERSHELL_GATEWAY_INTERNAL_CA_FILE`. Set it to an
absolute path to the operator's CA certificates for the selected
`HYPERSHELL_GATEWAY_CLUSTER_ISSUER`. Mount this file from installation settings.
Do not obtain its contents from a Gateway TLS Secret. The same issuer can serve
several components, but each component keeps its own identity and credentials.

The internal and public server checks use STEGO's `VerifyServerTLSSecret`.
Explicit CA files use `ParseServerTLSRoots`. The common runtime checks the
Secret type, namespace, name, owner, UID, revision, deletion state, certificate,
private key, hostname, expiry, and server use. A Secret's `ca.crt` cannot add a
trust anchor. No workload is created from a failed TLS observation. An absent
or invalid operator CA prevents controller startup.

Both jshell workflows use environment variable `JSHELL_GATEWAY_INTERNAL_CA_PEM`
from the `jshell-ci` GitHub environment. This contains only the selected public
CA certificates. The test mounts them separately from database trust and public
Gateway configuration. A manual run must set
`STEGO_TEST_GATEWAY_INTERNAL_CA_FILE` before it starts the deployment script.
The internal RPC client also uses this independent trust source.

The [regression record](internal-tls-trust.json) contains six invalid cases that
the old internal verifier accepted. The corrected focused checks and frozen
inspection passed. The earlier public workflow uses the previous source. A
complete run with this correction remains required. Sandbox client-certificate
distribution, certificate revocation, production CA rotation, and Gateway network
isolation remain separate open requirements.
