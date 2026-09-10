The controller metrics now include pending cleanup outside the admitted queue.
STEGO supplies the aggregate storage query, independent sampler, and fixed metric
names. Hypershell supplies the owner, target, provider scope, and access checks.
The compiler pin is `93cacec0cb6d3a2b5f8c2376159e03574f6ed571`, with PostgreSQL
adapter 3.11.0 and controller 1.10.0.

The private gRPC API provides two summary methods:

- `GetGatewayCleanupSummary` selects the identity owner or one workload target.
- `GetDatabaseCleanupSummary` selects the provider owner and one database provider.

Each response repeats its scope and returns the pending deleted-resource count,
the oldest original deletion time, and the database statement time. The oldest
time is absent when the count is zero. No resource IDs or payloads are returned.
The query uses retained cleanup state. Completion removes a row from that scope's
count; reopening restores it with its original deletion time.

A caller must be a configured controller with the matching cleanup grant.
Gateway workload grants select an exact cluster target. Identity grants select
the identity owner. The database cleanup grant covers its provider owner; the
request then selects the exact `deployment` or `cnpg` provider. Ordinary users
and platform administrators do not gain access through their public catalog
permissions. These reads neither record completion nor authorize deletion.

The deployment database controller samples only `deployment` records. Gateway
workload controllers sample their configured cluster target. The identity
controller samples its identity owner. The sandbox-count controller has no
cleanup source. A summary for another provider can be read by an authorized
operator, but this change does not add another provider implementation.

When `HYPERSHELL_METRICS_ADDR` enables collection, the generated runtime samples
cleanup separately from recovery scans. It uses each controller's resync interval
and request timeout. A blocked scan cannot stop sampling. With metrics disabled,
no summary request is sent. Use the existing loopback metrics endpoint described
in [controller metrics](controller-metrics.md).

The added metrics have the `stego_controller_` prefix:

- `cleanup_enabled` and `cleanup_available` identify the source and last-read state.
- `cleanup_pending_resources` reports the last count.
- `cleanup_oldest_pending_timestamp_seconds` reports the oldest deletion time.
- `cleanup_last_success_timestamp_seconds` reports local receipt of a successful read.
- `cleanup_reads_total` uses fixed success and failure labels.

Calculate cleanup age from the oldest timestamp only while the count is positive,
the sample is available, and the last success is recent. Set freshness limits
from the configured interval and request timeout. A read failure retains the old
values and marks them unavailable. Clocks must be synchronized for wall-clock
age. A successful read is not proof that every external effect is absent.

A missing API method, contradictory scope, invalid timestamp, or malformed
sample stops the controller when collection is enabled. Transient read failures
are reported without private error text and sampled again. Replace API instances
with this private contract before enabling these metrics in new controllers.
No public REST shape or deletion visibility changes. No database migration is
required for the aggregate query.

The three cleanup workflows use PostgreSQL, generated REST and TLS gRPC servers,
and a Kafka protocol fixture. They create and delete records, restart the API,
and then verify two pending records. Public callers and an ungranted cleanup
owner are denied. The database test adds a deleted CNPG record and proves that
it does not enter the deployment count.

Each workflow then forces a provider retry and holds one resource while the
other completes. HTTP metrics report available pending cleanup without resource
IDs or private errors. Direct summary reads show one pending resource, then zero
and no oldest timestamp after both complete. Existing revision, event delivery,
public 404, and one-action-per-key checks remain in force.

The three workflows and offline CLI version check passed with race detection in
20.603 seconds. Unit, contract, and static checks passed. The full compiler race
suite passed with PostgreSQL required. The selected application checks do not
claim a full application or real Kubernetes suite run for this change. The
[compiler evidence](https://github.com/jsell-rh/stego/blob/93cacec0cb6d3a2b5f8c2376159e03574f6ed571/specs/cleanup-summaries.md)
records rollback, target-history, reopening, collation, and performance tests.
Durable per-resource conditions, history retirement, distributed ownership,
persistent retries, and production capacity remain open.
