The public Gateway test requires an operator update to the existing browser CI
installation. The first-install command rejects an existing namespace. Keep
that check. The saved installation from browser run `34972072109` and a fresh
inspection render have the same eighteen cluster resource names.

Only two ClusterRoles change:

| Role | Added permission |
| --- | --- |
| `fixture-gateway-inspector` | Read the named `openshell-public-tls` Secret and Certificate; update only that Certificate's status for renewal |
| `gateway-worker` | Create, get, patch, and delete OpenShift Routes; create `routes/custom-host` |

Both role names have the prefix
`stego-service-ci.hypershell-namespace-allocation.`. Existing bindings restrict
these roles to allocated Gateway namespaces. The update adds no binding and
changes no admission policy. Sixteen cluster resources remain unchanged.

Use `scripts/plan-browser-public-permissions.py` to compare an installation
snapshot with output from `prepare-browser-cluster.py --render-only`. Render
from a frozen inspection source with the pinned compiler and the installation's
namespace file group. The planner makes no cluster requests. For example:

```sh
python3 scripts/plan-browser-public-permissions.py \
  --installation /path/to/installation.json \
  --rendered /path/to/render-results \
  --output /path/to/new-plan.json
```

The planner checks the installation identity, manifest hashes, resource inventory,
and exact permission additions. It rejects removed grants, broader permissions,
changed bindings or policies, changed role metadata, and added or removed cluster
resources. The output file has mode `0600` and must not already exist. The output
is an update plan, not a Kubernetes apply document.

The proposed installation data has `pending-live-verification` as its policy
check state. Before an operator applies this plan:

1. Acquire the shared live-test Lease. Keep it until verification is complete.
2. Require the same namespace UID and installation UID, revision, and data.
3. Require no test workloads or allocated namespaces.
4. Check all eighteen live resource UIDs and complete specifications against
   the saved installation. A different resource must stop the update.
5. Patch only the two rule lists. Each patch must test its fresh UID and
   resourceVersion. Save a journal before the first write.
6. Verify the resulting resources and current admission policy type checks.
   Record success only after these checks pass.
7. Replace the immutable installation record with UID and revision preconditions.
   Keep the old data and journal for recovery. Verify the new record, then release
   the Lease. An interrupted update requires inspection before another test.

The live update passed on 2026-09-15 after the operator restored access. A fresh
render used consumer `0befe58` and compiler `7ebd678`. The update changed only the
two rule lists. All eighteen resource UIDs and complete specifications passed
verification. Admission type checks passed. The immutable installation record
was replaced with identity and revision checks. The shared Lease was released
after the new record passed verification.

The [live update record](browser-public-permission-update.json) contains resource
receipts and recovery journal hashes. The earlier
[offline plan](browser-public-permission-plan.json) remains a historical record.
The public workflow started in run `34983965151` with the existing jshell router,
its current address, and the public certificate from the selected test issuer.
The complete public Gateway result remains pending. These installation checks do
not prove application behavior or Gateway network isolation.

The first complete public run, `34983965151`, failed. Its Gateway Pod and
certificates were ready, but OpenShift denied the Route's explicit host under
the controller identity. A server dry run reproduced this error. The
[OpenShift host assignment code](https://github.com/openshift/library-go/blob/main/pkg/route/hostassignment/assignment.go)
requires `create` on `routes/custom-host` when a caller sets the host.

The earlier two-Role update lacked this permission. The current initial plan
includes it. For an installation that already has the earlier update, select
`--phase route-host`. This phase permits only that one added grant on the existing
Gateway worker role. It rejects additional changes. Seven planner tests pass.

The [Route host update record](public-route-host.json) preserves the failed run,
its complete cleanup, the rejected dry run, and the live correction. Seventeen
resources remain unchanged. All eighteen identities and specifications passed
verification before the immutable record was replaced. The new record passed
verification before the Lease was released. A fresh complete public run remains
required. No public connection result is claimed from the failed run.
