# Controller trace evidence

Status: acceptance candidate. The selected compiler still uses the old trace
boundary. Do not run this source as a live qualification or promote it until the
new signed STEGO compiler is selected and its generated output is committed.
The existing pending-result workflow uses a separate frozen source.

The live browser workflow now checks each controller span from its worker
telemetry collector. Reconciliation, scans, cleanup samples, and watch sessions
must have valid nonzero IDs and no parent, link, event, or trace-state data.
Names, attributes, and outcomes must use the fixed common runtime values.
Paired logs and spans must agree on operation, outcome, and retry status.

Each expected worker instance must supply a validated pair. For each worker
service, an instance must prove at least two different traces for each required
operation: reconciliation, scan, and cleanup. A short-lived old instance does
not need to perform a second scan. The three repeated operations must come from
one instance of each service; separate incomplete instances cannot combine their
counts to satisfy this check. Existing worker instance counts, metrics,
provider child-span checks, and restart checks remain required.

Collection retains at most 256 pairs and trace owners per worker instance.
Operation counts stop at two. Duplicate delivery does not increase a count.
Unmatched records can be removed at the bound; they cannot establish a pair.
A repeated trace with a different span is rejected while its owner is retained.
Every received controller span must have no parent even after owner eviction.
This bounded observation does not claim complete historical trace coverage.

The workflow saves `controller-traces.json` after Gateway cleanup. It records
fixed operation counts and service instance IDs. It contains no resource keys,
credentials, provider response bodies, or private error text.

Focused CI checks invalid ancestry and fields, duplicate and reordered delivery,
collection limits, exact instance counts, and missing operation evidence. These
checks qualify the evidence collector only. A live run with the released compiler
must still prove the application behavior.
