# Generated console configuration rollout

The Gateway console uses the common STEGO deployment renderer. Hypershell
selects the verified credential set and supplies its content digest through
`deployment.Options.ConfigurationDigest`. STEGO validates that value and sets
`stego.dev/config-sha256` on the Pod template. Hypershell requires a nonempty
digest and retains its placement, owner, allocated account, and Service
separation checks.

The existing common Secret digest excludes metadata. A credential change must
change the Pod template; a Secret version change alone must not change it.
Credential contents must not enter the Deployment or Service. The consumer test
checks these conditions and compares all other resource fields.

Existing Deployments can retain the old application annotation. The controller
does not update that annotation. The new common annotation controls later
configuration rollouts. Adding it can cause one rollout during adoption.

Pinned regeneration and module selection passed full inventory review. The
focused check passed 43 cases across 11 tests. The full application check passed
1,192 core cases across 368 tests, retaining all 1,176 prior cases and the same
five recorded exclusions. Its browser, UI, and image jobs passed. All seven
application images passed independent source, content, private registry, and
signature checks. See the [application evidence](console-rollout-application-evidence.json)
and [image evidence](console-rollout-image-evidence.json).

The complete live Gateway workflow passed all 11 required tests. Review checked
REST and gRPC access, denied requests, event delivery, restart, regeneration,
telemetry, four browser images, and cleanup. The console Deployment, ReplicaSet,
and ready Pod used the same common digest. The old annotation was absent, and
the console Pod did not match the Gateway Service. This observation read no
Secret contents and changed no cluster resources. See the
[live workflow evidence](console-rollout-browser-evidence.json).

The frozen fixture supplies the existing test inspection permissions. The test
resources were removed, the shared Lease was free, and all 32 standing resources
were unchanged. This accepts the common console rollout path. It does not prove
production capacity, live Kata isolation, or OpenShell Sandbox execution.

The largest normal cleanup observation with 100 live accounts was 32.69
seconds. The 30-second target was not met. The target remains open.
