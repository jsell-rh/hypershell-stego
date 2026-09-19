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

The source remains fixed while its full application run `35471009094` completes.
The capacity gate requires that exact full result and the verified journal
result before it can start the seventh measurement. These journal checks do
not establish a cleanup-time improvement or qualify total Gateway cleanup.

The separate live workflow `35470884946` uses `7f81556` and compiler `d3ccd11`.
It checks the corrected Sandbox inspection permissions without the scheduling
change. Its complete result and cleanup remain required before promotion.
