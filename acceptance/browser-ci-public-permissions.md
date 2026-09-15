The public Gateway test requires an operator update to the existing browser CI
installation. The first-install command rejects an existing namespace. Keep
that check. The saved installation from browser run `34972072109` and a fresh
inspection render have the same eighteen cluster resource names.

Only two ClusterRoles change:

| Role | Added permission |
| --- | --- |
| `fixture-gateway-inspector` | Read the named `openshell-public-tls` Secret and Certificate; update only that Certificate's status for renewal |
| `gateway-worker` | Create, get, patch, and delete OpenShift Routes |

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

The live update remains pending. The operator context returned `Unauthorized`
on 2026-09-15. The offline plan is based on a saved snapshot; it does not prove
that the current cluster has the same state. The
[plan record](browser-public-permission-plan.json) preserves source, manifest,
and plan hashes. Six bounded local tests passed, including rejected permission
expansion, changed policy, invalid identity, duplicate resource, and bad hash
cases. These results do not establish a public Gateway workflow pass.
