# Capacity RPC diagnostic limit

The capacity summary uses Go-style status names, such as `DeadlineExceeded`,
in its error allowlist. The generated runtime emits canonical names, such as
`DEADLINE_EXCEEDED`. The summary converts those values to `other`. It also
omits `rpc.response.status_code` from its saved result.

In the sixth capacity run, `35468703943` at `6c54674`, eight RPC groups contain
23 spans with an error class of `other`. The discarded class cannot be recovered
from these aggregate records. The source comparison confirms that the relevant
runtime and diagnostic files are unchanged in candidate `6a8a882`. See the
[source and result evidence](capacity-diagnostic-status-evidence.json).

This defect limits the diagnostic explanation of failed calls. The generated
runtime emits the canonical status. The account counts, preserved background
state, elapsed cleanup time, and failed 30-second target remain valid.

Keep the seventh measurement fixture unchanged for the scheduling comparison.
A separate correction must retain the fixed canonical RPC status set and test
unknown values, private input, and the existing group and time limits. It must
not retain raw spans, provider responses, or arbitrary status strings. Later
capacity results must identify the changed diagnostic fixture.
