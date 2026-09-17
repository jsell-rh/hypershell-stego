# Current Pod readiness evidence

The main browser run 35248320996 failed at source
`110d9c4eeb5d830ec8b8d1d03a780a5e5fbfbc47`. The console Pod was not ready
when the test checked the baseline before removal of public network access.
The API and Deployment readiness observations came from earlier reads.
The saved result is a failure. Independent cleanup passed.

The test now repeats valid pending Pod observations within its existing
180-second readiness wait. It saves account evidence only after both current
workload Pods are ready. Account and token errors, incomplete inventories,
multiple current Pods, and invalid readiness data still cause failure.
Terminating Pods must also use the required account and token setting.

The focused CI checks and the complete live browser repeat are pending.
This change does not establish that the live workflow passes.
