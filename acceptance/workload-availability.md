# Gateway workload availability

The workload controller now uses STEGO's `DeploymentAvailable` helper. It
requires the current Deployment generation and the expected total, updated,
ready, and available replicas. It also checks owner labels, resource identity,
conditions, and deletion state. It no longer treats ready replicas alone as a
complete rollout.

STEGO owns observation validation. Hypershell owns the expected Gateway labels,
replica count, and phase mapping. Missing or incomplete Deployments return
`ErrPending`; the controller reports provisioning or degradation. Invalid
ownership or denied reads cannot produce a healthy observation.

The compiler pin is `5e9c89d9201db4532de1787dcff234e3988cfa00`.
Small generated runtime tests and Hypershell adapter tests passed. They include
a ready but unavailable Deployment, stale generation, an old replica, wrong
owner, deletion, missing resources, and a denied read. Regeneration added one
runtime file and updated compiler records. Existing API schemas, generated
deployment permissions, and other runtime files did not change. The withdrawn
pinned-admission prototype is absent from generated output.

Full compiler [CI passed](https://github.com/jsell-rh/stego/actions/runs/34950180723).
The [API gate](https://github.com/jsell-rh/hypershell-stego/actions/runs/34950339329)
passed all 30 required workflows at application revision `3f188b2`. Verification
matched 886 source files, 229 generated files, and all four generation records.
The Job, Pods, and fixtures are absent. See the
[verified evidence](workload-availability-evidence.json).

The [complete supplied PostgreSQL browser run](https://github.com/jsell-rh/hypershell-stego/actions/runs/34950339472)
passed in 387.32 seconds at the same application revision. Verification matched
886 source files, 230 generated files, and all three browser generation records.
All 16 CI access probes, 57 application access checks, and six admission probes
passed. Namespace replacement took 43.78 seconds and retained SQL identities,
keys, credentials, and provider data. SQL fault recovery, credential encryption,
session checks, deletion, and automated cleanup passed. The Job and Pods are
absent; the operator installation remains.

This result proves the availability check in the complete internal workflow.
It does not cover the later viewer and live-account deletion checks. The
screenshots still show no public connection command. Public connectivity is
not established.
The [external connection gate](https://github.com/jsell-rh/stego/blob/main/specs/hypershell-external-connection.md)
remains open, including TLS and address-ownership decisions.

The [full CI run](https://github.com/jsell-rh/hypershell-stego/actions/runs/34950339394)
also finished at `3f188b2`. Core acceptance passed in 1301.692 seconds. Ordinary
browser, console, and service-image jobs passed. The overall run failed on the
unfinished CNPG and Sandbox jobs; this is not a complete release result.
