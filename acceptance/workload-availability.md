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

Full compiler CI and the complete supplied-server browser workflow remain
required for this revision. This check does not prove an external endpoint.
The [external connection gate](https://github.com/jsell-rh/stego/blob/main/specs/hypershell-external-connection.md)
remains open, including TLS and address-ownership decisions.
