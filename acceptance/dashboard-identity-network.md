# Gateway console identity connection

The jshell workflow at `f79b46d` created owned image pull Secrets and started
the upstream dashboard containers in both Gateway namespaces. The separate
registry token passed its authenticated identity check and seven permission
checks. The generated browser backend then failed during its constructor.

Inspection of the active identity provider NetworkPolicies showed a missing
connection. Ingress was permitted from Pods with the Gateway owner label in
assigned Gateway namespaces. The console Pods deliberately do not have that
label, because it selects the Gateway server Service. No ingress rule matched
the console Pod labels. The browser backend needs that connection for OIDC
discovery and login.

The test fixture now permits TCP port 8443 from Pods named by the
`app.kubernetes.io/name=hypershell-gateway-console` label, only in namespaces
with both the expected allocator marker and the `gateway` allocation profile.
The existing Gateway, probe, and control namespace rules remain in place.

The regression test failed for both assigned console cases before the change.
It now accepts those cases and rejects a foreign allocator, a state namespace,
an unrelated Pod, and an unassigned namespace. It also checks that the existing
management console rule applies only within the control namespace.

The current cluster run used its original frozen source. The next complete
run must prove that this rule permits browser startup and the rendered
workflow. The constructor error alone does not identify every possible
startup failure.
