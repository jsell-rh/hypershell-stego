Hypershell uses STEGO compiler
`d7717302c80fe3251673e1d94f2177dd25ed9371`, gRPC component 1.9.0, and telemetry
component 1.7.0 for outbound RPC signals. The generated request and controller
boundaries pass their active runtime through context. Generated clients reuse
that runtime for logs, metrics, spans, and trace propagation. No Hypershell
client wrapper, exporter, or provider ownership code was added.

The local telemetry manifest still reported 1.2.0 before this change, although
the pinned compiler generated the later telemetry implementation. Its version
now records 1.7.0. Compiler identity continues to identify executable generation
code. The [compiler contract](https://github.com/jsell-rh/stego/blob/d7717302c80fe3251673e1d94f2177dd25ed9371/specs/rpc-client-observability.md)
records the method allowlist, metadata policy, lifecycle, and measured costs.

The service-account recovery workflow now verifies outbound signals as well as
its [sweep signals](sweep-observability.md). Before restart, the provisioner
rejects revocation. The recovery span must have a child client span with
`UNAVAILABLE`. Its client log must have the same trace and span IDs. A client
duration metric must record that status. All three signals must use the recovery
runtime's instance ID.

After restart, revocation must finish and the provider identity must be absent.
The same checks must find an `OK` client call beneath successful recovery. The
replacement runtime must have a new instance ID. The workflow retains Gateway
event delivery, account and credential checks, owner access, denied access,
stored revocation across restart, and private-data exclusion from all signals.

The added child-span check first failed on compiler `facefeb` after a real
account creation and pending revocation. With the new compiler, the extended
workflow passed in 7.87 seconds. Sixteen selected application tests passed under
race detection with PostgreSQL and Keycloak required, in 151.922 seconds. The
real Keycloak workflow passed in 34.03 seconds. Other checks cover HTTP and gRPC
tracing, watch lifetime, collector failure, recovery across deleted and partial
pages, process privacy, HTTP diagnostics, task abort and restart, and replica
identity. Internal and contract race tests and static checks passed.

Repeat generation preserved all 98 output, state, and dependency hashes. The
compiler tests separately verify wire propagation with a TLS gRPC server,
sampled and unsampled logs and metrics, stream EOF, cancellation, handshake and
call deadlines, unknown method privacy, and caller metadata isolation. These
are workflow and runtime results, not a production capacity result.

The latest application revision needs its own full CI result. Kubernetes and
virtual-machine workflows were not repeated locally for this change. Outbound
HTTP, database signals, independent CLI and worker entry points, process
resource metrics, and complete process lifecycle export remain open. The full
enterprise goal remains active.
