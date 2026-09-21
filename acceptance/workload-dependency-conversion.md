# Common workload dependency conversion

Hypershell now calls `workload.DependencyFromData` for its verified ConfigMap
and Secret contents. `kubernetes.NestedMap` accepts both generated objects and
maps from the Kubernetes API. The local conversion helper is removed.

STEGO checks entry counts, encoded and decoded size limits, canonical base64,
and valid ConfigMap text. It returns owned byte slices. Invalid input returns
no dependency and a fixed error. The configuration digest format is unchanged.
Hypershell retains dependency selection, ownership, certificate trust, placement,
OpenShell settings, image selection, and account rules.

Compiler `d8c37d20` passed 145 generated cases, all 37 compiler packages, and
both generated examples. Its signed immutable release and fresh installation
passed verification. Regeneration checked all 428 output and module files,
419 recorded generated hashes, and 41 input hashes. Only the two common helpers,
compiler identity, and three generation records changed. All 422 other files
were unchanged. The selected Gateway console module therefore remains valid;
its complete source comparison is still required in application CI.

The application adds rejection checks for line separators in encoded Secret
data and invalid UTF-8 in configuration text. All 45 focused cases and 1,194
core cases passed. The full check retains all 1,192 prior cases and the five
recorded exclusions. All seven application images passed source, content,
registry fixture, and signature verification. See the
[application evidence](workload-dependency-application-evidence.json) and
[image evidence](workload-dependency-image-evidence.json).

The complete live workflow passed all 11 required tests. Review checked REST
and gRPC access, denied requests, event delivery, restart, generated output,
telemetry, four browser views, and cleanup. The ready console retained the common
configuration digest on its Deployment, ReplicaSet, and Pod. All test resources
were removed, the shared Lease was free, and 32 standing resources were unchanged.
See the [live evidence](workload-dependency-browser-evidence.json).

A separate regeneration check on the acceptance branch matched all 428 generated
and module files, 419 output hashes, and 41 input hashes. The patch was empty.
See the [regeneration evidence](workload-dependency-regeneration-evidence.json).
This accepts the common dependency conversion. Production capacity, live Kata
isolation, complete response mapping, and the remaining enterprise requirements
stay open.

The largest normal cleanup observation with 100 live accounts was 32.34
seconds. The 30-second target was not met. The target remains open.
