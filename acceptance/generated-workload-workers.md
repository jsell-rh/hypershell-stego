The database, Gateway workload, and sandbox-count processes now use STEGO worker
declarations, as the identity process already does. The compiler pin remains
`bbfacd13a018261b5c9e11041ec55e62fd60cbc3`. No new generator behavior is needed.

| Worker | Generated command | Domain setup |
| --- | --- | --- |
| Database | `out/deploy/workers/database` | `internal/databaseapp` |
| Gateway workload | `out/deploy/workers/gateway-workload` | `internal/gatewayworkloadapp` |
| Sandbox count | `out/deploy/workers/sandbox-count` | `internal/sandboxcountapp` |

Each setup function selects and opens domain providers, connects to the generated
API client, and calls its domain controller. It closes those connections before
it returns. STEGO owns the main function, signals, monitor, probes, callback abort
handling, and fixed process failure output. The former commands under `cmd/` are
removed. The database provider still defaults to `deployment`; `cnpg` remains an
explicit selection. API contracts, provider configuration, access rules, and
reconciliation actions do not change.

All four workers use `STEGO_CONTROLLER_MONITOR_ADDR`, with default
`127.0.0.1:9081`. Processes that share a network namespace need separate ports.
The old `HYPERSHELL_METRICS_ADDR` setting no longer controls these commands.
The generated `--stego-probe=live` and `--stego-probe=ready` commands query that
local listener. Readiness describes source availability, not complete provider
convergence. A controller still must finish its work when its context ends.

The existing database, CNPG, Gateway, and sandbox acceptance gates now build the
generated commands. Their process helper requires successful live and ready
probes and a clean signal exit, including after restart. The real Gateway tests
retain provider creation and retrieval, denied access, Pod and database recovery,
and offline deletion. The startup privacy check supplies invalid private provider
settings to each new worker. It requires exit code 1 and one fixed failure record,
with no private setting or stack output. CI also builds all four worker images
and checks their non-root user and entry point.

The compiler also emits restricted worker Deployment templates. These templates
have no automatic Kubernetes credential or RBAC grant. A site must supply the
required credentials and bind its exact Kubernetes API endpoint. The later
compiler pin `77e2133` adds `external_endpoints: [kubernetes]` to these workers.
The generated renderer requires `--egress kubernetes=IP:PORT`; repeat the flag
for each required address. STEGO emits each exact IP and TCP port rule. No
hand-written Kubernetes API egress policy is needed. Applying a manifest alone
does not establish a working Kubernetes provider deployment. These process tests do not claim that
deployment, automatic failover, provider fencing, or production capacity is proved.

The focused checks passed on jshell on 2026-09-11. The input-manifest package
passed under race detection in 1.054 seconds. The final startup privacy test
passed in 8.94 seconds; its package took 9.980 seconds. For the Gateway worker,
the fixture first confirmed that provider setup returned a private file path.
The generated process excluded that path from its failure output.

Both generation passes and the post-test check preserved all 129 output, state,
and dependency hashes. The hashes and generated archive match the checkout.
The Job reached `Complete`, and namespace deletion was verified. The initial
test also passed, before the stronger
Gateway fixture was added. That test had finished before its source was replaced
for the second check. Separate logs and exit records retain both results in
`/tmp/stego-workload-workers-mpcfzwnr`. The full provider workflows require the
new revision's CI results.

The [external egress check](external-egress.md) tests the generated policy
against a real Kubernetes API endpoint. It is separate from the controller
process and provider-workflow tests.
