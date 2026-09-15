The generated CLI can apply Gateway, managed cluster, Gateway release, and
Gateway-network records. It accepts YAML or JSON files, directory
trees, and stdin. Hypershell supplies the kinds, paths, and create/patch fields.
STEGO supplies input loading, validation, target lookup, HTTPS requests, dry runs,
and result reporting. The current compiler pin is recorded in
[`.stego/compiler-revision`](../.stego/compiler-revision).

The input uses the envelope from the current reference CLI:

```yaml
apiVersion: hypershell/v1
kind: Gateway
metadata:
  name: example
spec:
  cluster_id: CLUSTER_ID
  release_id: RELEASE_ID
  image: registry.example/gateway:v1
```

Use returned IDs for the cluster and release. The installation supplies the
PostgreSQL server to the assigned controller. Gateway creation requires no
database catalog, database registration, or provider selection. The controller
creates a separate logical database and login for each Gateway.

Gateway requests reject `database_id`, including empty and null values.
`ManagedDatabase` documents are not supported. See the
[current database contract](controller-local-database.md). Apply does not resolve
a new cluster's name into an ID for another document. An offline dry run cannot
verify that the referenced cluster exists.

```sh
hsctl apply -f gateway.yaml --dry-run
hsctl apply -f gateway.yaml -o json
hsctl apply -f resources/ -o json
cat gateway.yaml | hsctl apply -f - -o json
```

The API version can be absent in existing files. A supplied version must be
`hypershell/v1`. Metadata requires a name. Set `metadata.id` to select an existing
resource exactly. The server assigns new IDs. The spec uses API field names and
types. A field cannot occur in both metadata and spec. The generated runtime
rejects unknown fields, invalid types, duplicate keys, unsupported kinds, aliases,
custom tags, symbolic links, and oversized input before resource writes.

Without an explicit ID, apply searches for an exact visible name. Zero matches
select creation. One match selects patch. More than one match is an error.
This avoids changing an arbitrary resource when names are duplicated. The lookup
uses escaped search values and bounded pages. The API applies its normal access
filters. A caller cannot use an ID to bypass an opaque denied read.

All documents pass local validation and target lookup before the first resource
write. The selected create or patch contract is then checked. A later malformed
document, failed lookup, or missing required create field stops the whole input
before resource writes. Server domain rules can still reject a later mutation.
For that case, earlier successful writes remain, later writes are not attempted,
and the command returns a nonzero exit status with partial results.

The dry run is local. It does not read credentials or contact an API, and it
cannot create a user, grant, event, or resource. It reports `validated`, rather
than claiming that the API will accept the input. It does not check permissions,
reference existence, or server-only constraints. The runtime has explicit file,
document, directory, depth, field, and network limits; see the
[compiler contract](https://github.com/jsell-rh/stego/blob/2f3a2c06bff4a0a6811757e9a168eb810f57ce53/specs/cli-apply.md).

Successful results report `created` or `configured` with the returned ID. A client
error reports `failed`. A transport error, server error, or invalid write response
reports `unknown`. Retrieve state before retrying an unknown result. Apply does
not retry mutations. It prints no spec values or API error bodies. JSON output
includes each unattempted resource. Default output quotes names to prevent
terminal control sequences. `--output-file` creates a new private file and cannot
overwrite an input file.

The normal API policy remains in force. Catalog and network writes require a
platform administrator. Controllers have separate scoped observation and cleanup
permissions. Gateway creation requires the
creator role. An owner can patch an owned Gateway without retaining that role.
Each API mutation preserves its transaction and event contract. Applying several
resources is not one database transaction. Name lookup and creation are also
separate requests. Concurrent creators can produce duplicate names because the
API permits them. Retain the returned ID for later exact selection.

`TestGeneratedCLIApplyWorkflow` starts with empty catalogs. It runs generated API
and CLI processes against PostgreSQL and a Kafka protocol fixture with mutual
TLS. The CLI uses verified HTTPS. The workflow proves:

- A dry run without credentials sends no API request and writes no event or row.
- Catalog creation, repeat patch, returned IDs, and catalog event delivery.
- Gateway creation, placement, and its committed owner grant.
- An exact name search containing quotes and search operators.
- Owner patch access, opaque denied reads, and agreement with gRPC.
- Full input validation before requests and preflight before resource writes.
- Gateway rollback when its event cannot be stored, with an unknown CLI result.
- Partial results and a failure exit status after a domain reference error.
- Ambiguous-name rejection and explicit ID selection.
- State and owner access after restart, with further CLI patch requests.
- Gateway creation without a database catalog and stable placement on reapply.
- Parent deletion denied until scoped cleanup, then deletion and queue drainage.

The converted tests use no database catalog. Their results are in the
[controller-local transition record](controller-local-test-transition.md).
Earlier provider-selection test results do not prove this release. The separate
[supplied CNPG workflow](cnpg-installation.md) checks actual SQL effects.

The [role-binding apply mapping](cli-immutable-apply.md) now uses immutable
identity fields and reports matching records as unchanged. The name and PATCH
rules above apply to the four named resource kinds. The reference role API registers
only reads, although its CLI has a role apply stub. Role mutation requires a new
API and access policy. Kustomize rendering is also
open; `-k` fails before any request. The port does not claim Kubernetes field
ownership, pruning, an atomic server upsert, or a complete CLI port. The
[CLI port table](cli-port.md) records the remaining work.

A full-suite run on 2026-09-09 found a queue-drain timeout after the API mode
restart. Five diagnostic reruns did not reproduce that failure. They confirmed
that the test restarts with pending events. A fixed five-second drain limit is
shorter than the generated 30-second outbox lease. The original failure's exact
remaining rows were not captured, so an unfinished claim is a possible cause.

The restart check now allows the generated lease duration, one delivery attempt,
and five seconds for polling and completion. Other queue-drain checks retain
their five-second limit. A new runtime test leaves actual claims unfinished,
starts the runtime, verifies that the leases remain held, and requires delivery
of the original Gateway event ID after expiry. Queue failures now report bounded
kind, attempt, retry, and lease data without payloads or resource IDs.
