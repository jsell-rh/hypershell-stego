# Gateway identity discovery

Gateway recovery first scans retained Gateway IDs from the service database.
Provider discovery then finds native and console clients that can need recovery
after an offline deletion. Discovery does not prove absence or authorize a
provider change.

The previous provider scan listed every client in the realm. Its 10,000-client
window included unrelated service-account clients. An instance at the target
of 100 Gateways with 100 service accounts each can exceed that window before
the scan reaches all Gateway identities.

The application now selects its two canonical name prefixes, `hs-gateway-` and
`hs-console-`. STEGO supplies the name-query cursor and bounded page scan.
Hypershell supplies only the prefixes and Gateway ownership policy. A provider
substring match is only a candidate. The adapter checks the exact name and
reads its current representation before it checks ownership. It accepts the
existing complete legacy native binding. It rejects mixed or foreign ownership.
It returns each Gateway once, including when both native and console clients
exist. A changed name, failed read, repeated page ID, or scan limit returns an
error and no partial result. A list projection cannot supply ownership proof.

The unit test uses a TLS provider fixture. It checks page continuation, current
reads, legacy and console ownership, false list attributes, unrelated substring
matches, cross-page duplicates, denied searches, missing and failed reads,
changed names, scan limits, and parent cancellation. Hosted execution is pending.
The existing application workflow remains required for real-provider evidence.

This change removes unrelated service-account cardinality from the normal
Gateway identity query. It does not remove the common per-query scan window or
the controller's 20-second discovery deadline. Offset pages are not a snapshot;
later full scans and retained IDs remain required. Larger Gateway populations
and slow provider reads still need recovery and capacity evidence. The sixth
account cleanup measurement uses the earlier frozen source and unchanged
fixture so this discovery change cannot affect that comparison.
