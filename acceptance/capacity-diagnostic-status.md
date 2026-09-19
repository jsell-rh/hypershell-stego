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


The seventh run `35472389423` retained the original collector. Account cleanup
completed in 61.5320 seconds and still missed the 30-second target. All selected
accounts and journals were closed and background state was preserved. The
saved samples retain failed cycles before a later successful scan. The result
still cannot identify every RPC failure class.

Correction `c1b3580` changes only the diagnostic fixture and its documentation
from scheduling source `6a8a882`. It is pushed after the seventh test and its
cleanup finished. Its hosted workflow checks the canonical status cases before
measurement. Qualification and the next result remain pending. The changed
collector must be identified in later timing comparisons.
