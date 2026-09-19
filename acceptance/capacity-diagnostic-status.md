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
measurement. The diagnostic checks passed in run `35472990779`. The changed collector must
be identified in later timing comparisons.


The eighth run used source `c1b3580` and completed account cleanup in 44.9706
seconds. It failed the 30-second target. All 100 selected account rows, protected
journals, provider clients, provider users, and success audit records were
checked. All 9,900 background rows and 10,007 other clients stayed unchanged.
Source, binary hashes, limits, and test cleanup were verified. See the
[eighth result](capacity-eighth-evidence.json).

All 34 canonical client and server status cases passed. The first time bucket
contains three client `Delete` deadlines, two client `Delete` internal errors,
and two journal `Save` aborted calls. The aggregate cannot prove that the save
and delete failures belong to the same requests. Later cancellation records
occur after the measured cleanup interval and must not be counted as cleanup
failures.

The saved account count reaches 95 at 15.1323 seconds and remains there until
the first failed scan finishes. A later scan closes the remaining rows. The
first saved count of 100 occurs at 34.6770 seconds. The journal scope closes at
44.6510 seconds. This supports further review of failed-work retries and repeated
journal reads. It does not justify removal of checks for late provider effects.

The test journal API uses real authenticated handlers and storage, but it does
not attach the generated server trace hook. The production API does attach that
hook. The database fixture has no configured one-connection limit. The current
records do not establish a database pool wait or a production server tracing
defect. The next diagnostic change must preserve these distinctions.
