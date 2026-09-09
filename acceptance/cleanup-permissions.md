Cleanup observations now require explicit grants. Being a configured control-plane
subject is necessary but does not grant permission to record cleanup.
STEGO generates the policy parser and exact matching. Hypershell supplies these
resource and operation names:

| Resource | Operation | Target |
| --- | --- | --- |
| Gateway | cleanup.identity | Empty string |
| Gateway | cleanup.workload | The configured ManagedCluster ID |
| ManagedDatabase | cleanup.provider | Empty string |

Set `HYPERSHELL_CLEANUP_GRANTS` to a JSON array. Each entry must contain the exact
`issuer`, verified token `subject`, `resource`, `operation`, and `target` strings.
For example, one workload controller can have this grant:

```json
[
  {
    "issuer": "https://issuer.example/realms/hypershell",
    "subject": "cluster-controller-subject",
    "resource": "Gateway",
    "operation": "cleanup.workload",
    "target": "0ujtsYcgvSTl8PAuAdqWYSMnLOv"
  }
]
```

Replace the issuer, subject, and cluster ID with deployment values. The issuer
must match the token verifier. Subjects are token `sub` values, not usernames
or role names. Also list the subject in `HYPERSHELL_CONTROL_PLANE_SUBJECTS`.
Use separate subjects and grants for independent controller responsibilities.
An empty target matches only an empty target. It does not permit other targets.
There is no wildcard or administrator-role fallback.

An absent variable or `[]` denies every cleanup observation. Invalid JSON,
unknown or repeated fields, non-string fields, and invalid or repeated grants
fail API initialization. The generated policy has bounded input and copies its
configuration into an immutable map. Applications cannot change that map during
a request.

The API checks the grant before the mutation. The generated store still requires
a deleted resource, a recorded target where applicable, and the observed
revision. Permission alone cannot create target history. A successful observation
and its event still commit together. Denied calls leave observations unchanged.

The application tests use separate subjects for two cluster targets and identity
cleanup. They reject cross-target, cross-owner, and cross-resource writes,
including requests from a configured subject with an administrator role. A
configured subject with no grant is denied. The tests also remove a grant,
restart the API, and verify that the old token can no longer write that scope.
The remaining subject can still write its permitted target.

Update every API instance when granting or revoking access. An older process
keeps its old policy until it is replaced. There is no live policy reload or
shared transactional policy store yet. Provider credentials still govern external
operations. This change scopes cleanup observation writes; broad controller
reads, other mutations, and observation groups still need separate permissions.

Local validation on 2026-09-09 passed the focused REST and TLS gRPC checks,
the full application race suite with PostgreSQL and Keycloak required, and the
real Gateway Kubernetes workflow. The full acceptance package took 555.058
seconds. The Gateway workflow took 243.581 seconds. Remote pinned regeneration
reproduced all 69 generated, dependency, and state file hashes. Module verification
and `go vet ./...` passed.
