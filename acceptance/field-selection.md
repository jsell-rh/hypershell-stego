# REST list field selection

The seven reference list resources accept `fields`: Gateways, Gateway networks,
managed clusters, managed databases, Gateway releases, roles, and role bindings.
For example, `GET /api/hypershell/v1/gateways?fields=id,name&size=20` returns only
`id` and `name` in each item. The list keeps its kind, href, page, size, and total.
Selection does not add an ID or other item metadata that the caller omitted.

Empty input and `*` select all declared public fields. Field names are exact and
case sensitive. Unknown names, duplicate selectors, empty terms, repeated query
parameters, invalid wildcards, and storage-only fields return HTTP 400. Invalid
selectors fail even when the caller has no visible rows or requests only a count.

The current Hypershell responses declare top-level public fields. String arrays,
such as `server_dns_names`, and JSON values, such as role `permissions`, are
atomic fields: select the whole field. The generated runtime also supports
declared child paths and object arrays for applications with structured schemas.
An atomic field does not accept a child path or wildcard.

STEGO validates and projects the response. Hypershell declares the public JSON
names and calls the projector after its access checks and presenters. SQL access
filters, counts, ordering, and current-status presentation run before selection.
The implementation does not use client-selected SQL columns. It does not change
get, create, patch, delete, watch, or gRPC shapes. The service-account API has no
reference `fields` parameter and retains its separate list contract.

The generated projector preserves exact JSON numbers, nulls, absent fields, and
array order. It checks response size, structure, and duplicate members before
returning output. A malformed response fails as a server error. The compiler's
[field contract](https://github.com/jsell-rh/stego/blob/main/specs/response-fields.md)
states the bounds and includes nested-array and concurrent-use tests.

HTTP application component 1.4.0 leaves the normal response path in use when
the request has no `fields` parameter. It does not encode and project a full
response before the endpoint encodes it again. Explicit empty input or `*`
still uses the projector. Both paths receive only authorized public responses.
The normal endpoint keeps its response-size and encoding checks.

`TestRESTFieldSelectionPreservesAccessAcrossRestart` creates two Gateways for one
caller and a hidden Gateway for another. It checks selected pages and counts,
ordering, an OR search that includes hidden data, empty visibility, string arrays,
full-field selection, and invalid selectors. It exercises all seven list
resources. Complete gRPC reads prove that projection did not change storage.
REST deletion, generated event delivery, and API restart complete the workflow.
A separate test checks that every public response field has a declaration.

The original application returned HTTP 400 for `fields=id,name`; the regression
failed in 3.42 seconds. With HTTP application component 1.3.0, it passed in 5.23
seconds. Search, placement, and Gateway-network regressions also passed. The
focused acceptance package took 18.834 seconds. These durations include setup.

The first full suite failed two old assertions that required `fields=id` to
return HTTP 400. The role and role-binding checks now require an unknown field
to fail. The new workflow supplies the positive selection checks and also
rejects an unknown field on a count-only request. The failed suite is not counted
as a pass.

With component 1.4.0, the six focused workflows passed in 26.747 seconds. The
field-selection workflow took 4.82 seconds. The other checks cover grant
discovery, role discovery, placement, Gateway networks, and access-filtered
search. HTTP unit race tests, static checks, and module verification passed.

The final full PostgreSQL/Keycloak race suite passed on 2026-09-10. Its acceptance
package took 673.013 seconds. This run includes transport, event, restart,
controller, identity, and CLI checks. The separate Kubernetes workload gates
were not repeated locally for this HTTP change; CI runs them separately.

A local benchmark on 2026-09-10 used Go 1.26.8 on Linux amd64 and an Intel Core
Ultra 9 185H. Three samples each made 100 requests for 20 visible Gateways from
a 200-row fixture with current PostgreSQL statistics. The full race suite ran
on the same host. The test includes JWT checks, SQL, and an HTTP recorder; it
excludes network transfer.

| Reply | Time per request | Allocated bytes | Allocations | Response body bytes |
| --- | --- | --- | --- | --- |
| Normal | 2.565–2.632 ms | 268750–271009 | 3395–3398 | 9692–9708 |
| `fields=id,name` | 3.026–3.427 ms | 434054–435195 | 8392–8395 | 1244–1250 |

Selection reduces transfer size but adds server work. The first implementation
also projected normal replies; it allocated 513687–525174 bytes per request in
the same benchmark. The optional generated path removes that extra work.
These local samples do not establish production capacity or a latency target.

```sh
STEGO_REQUIRE_POSTGRES=1 GOWORK=off go test -count=3 -mod=readonly -run '^$' -bench '^BenchmarkREST(Filtered|Selected)Page$' -benchtime=100x -benchmem ./acceptance
```

Set `STEGO_TEST_POSTGRES_DSN` to a PostgreSQL test server before this command.

The pinned reference describes selection on list items. Its Gateway handler
passes an item slice to a reflection function that expects a list struct. This
implementation follows the documented selection behavior and preserves list
metadata. It does not copy that unsafe reflection path.

Related-resource search, larger REST pages, remaining client options, and
production capacity remain open. Projection reduces the selected response; it
does not remove database work or establish a latency improvement.
