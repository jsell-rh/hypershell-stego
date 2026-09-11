The generated database, Gateway workload, and sandbox-count workers require a
Kubernetes API connection. Each declares the external endpoint name `kubernetes`.
STEGO's renderer requires the operator to bind this name to exact IP addresses
and TCP ports. This removes the need for a hand-written egress policy.

Run `scripts/check-external-egress.sh` with `STEGO_TEST_CONTEXT` set to the saved
cluster context and `STEGO_TEST_RENDERER` set to the compiled
`out/deploy/render` command. The script does not compile code on the workstation.
The cluster must enforce Kubernetes NetworkPolicy and expose a ready IPv4 API
EndpointSlice in the `default` namespace.

The script creates a dedicated namespace and one restricted, bounded Pod. Its
short-lived projected service-account token permits only a read of that Pod.
No user token is copied to the Pod, and the projected token is not returned to
the workstation. Verified HTTPS uses the cluster CA and the Kubernetes service
hostname. The test connects directly to an API endpoint IP and port.

The check first proves that the authenticated API request succeeds. It then
requires default denial. The policy from the actual generated database-worker
renderer must permit the request. A rule with the wrong port must deny it. A
rule with the wrong IP must also deny it. Restoring the correct generated rule
must restore access. Each denial requires three consecutive connection timeouts.
Each phase has a bounded wait; the Pod has a five-minute deadline.

The test uses the generated worker's policy with a dedicated HTTPS probe. It
does not run the database controller in that Pod. The real provider workflows
have separate CI gates. Service-address translation, IPv6 enforcement, API
address rotation, credentials, RBAC, and production deployment still need their
own evidence. The renderer supports IPv6, with generated runtime tests for
exact `/128` prefixes and rejection of invalid addresses.

The live check passed on jshell on 2026-09-11 with compiler
`77e2133b0deb96fcc8ec1954c3f7cd888acf7894`. The API request returned 200 before
isolation, after the correct generated rule, and after recovery. Default denial,
the wrong port, and the wrong address each produced three consecutive connection
timeouts. The saved manifests preserve the exact rules used for all phases.
The frozen check source matched the final repository files. The namespace was
deleted. Evidence is retained in `/tmp/stego-external-egress.SSCIGfwK`.

The compiler's deployment and registry packages passed under race detection in
3.803 and 2.123 seconds. Hypershell's input-manifest check passed under race
detection in 1.055 seconds. Two generation passes and the post-test check
preserved all 129 output, state, and dependency hashes. They also match the
checkout. The build Job completed, and its namespace was deleted. Those records,
the generated renderer binary, and both source archives are retained in
`/tmp/stego-external-render-pd9okcl0`. Full compiler and application CI results
remain separate checks.
