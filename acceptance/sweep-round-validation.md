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
capacity fixture files are unchanged. This run has no timing result yet.
The correctness checks do not establish a cleanup-time improvement or qualify
total Gateway cleanup. The scheduling candidate is not yet on main.

The separate live workflow `35470884946` at `7f81556`, with compiler `d3ccd11`,
passed all 11 required tests and its cleanup audit. Main `dbd8363` contains
that qualified source and its evidence. That live run does not include the
scheduling change. See the [live result](sandbox-activation-live-evidence.json).
