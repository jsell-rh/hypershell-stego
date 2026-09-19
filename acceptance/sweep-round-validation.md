# Sweep interval candidate validation

Candidate `6a8a882` uses the shared controller interval option from compiler
`3220812`. Account recovery waits one second after all ten status groups have
had a pass. Group work budgets, worker counts, page sizes, action reserves, and
the six capacity fixture files remain unchanged.

Journal run `35470987849` passed all 36 required tests with no failures or
skipped cases. The original six-account restart fixture is unchanged. It saved
partial progress, then closed all six accounts once and sealed the inventory
scope after restart. The parallel fixture also recovered partial progress:
all 32 rows and journals were visited, with 32 success audits and a bounded
callback count. See the [journal result](sweep-round-journal-evidence.json).

Full application run `35471009094` passed all 329 top-level tests and 844
cases. All prior cases remain, with the same five conditional exclusions.
The rendered browser, web console, and service image jobs passed. The Kata
workload job remains excluded. The source comparison confirms that Go tests
and the full-check workflow are unchanged. See the
[full result](sweep-round-full-evidence.json).

The exact full and journal results permitted the gate to start the seventh
capacity run, `35472389423`. Its source remains fixed at `6a8a882`. The six
capacity fixture files are unchanged. The run completed account cleanup in 61.5320 seconds and missed the 30-second target.
The correctness checks do not establish a cleanup-time improvement or qualify
total Gateway cleanup. The scheduling candidate is not yet on main.

The separate live workflow `35470884946` at `7f81556`, with compiler `d3ccd11`,
passed all 11 required tests and its cleanup audit. Main `dbd8363` contains
that qualified source and its evidence. That live run does not include the
scheduling change. See the [live result](sandbox-activation-live-evidence.json).


The seventh run preserved all 9,900 background account rows and 10,007 other
provider clients. All 100 selected clients and users were absent; all 100
account rows and protected journals were closed, with 100 success audits.
Source, binary, compiler pin, resource limits, and test cleanup checks passed.
The elapsed time decreased from 92.3174 to 61.5320 seconds with the same fixture.
This remains a failed target result. See the
[seventh measurement](sweep-round-capacity-evidence.json).

Saved samples first show all account rows closed at 50.8163 seconds. The scope
sealed at 61.1957 seconds. Earlier saved cycles retain failure flags and require
another scan. The 112 trace groups have no dropped groups, but the known RPC
status classification defect still limits the diagnosis. Some trace groups
cover preservation checks after the measured cleanup interval; concurrent span
durations cannot be added as wall time. A separate diagnostic correction will
change only the collector before another measurement.
