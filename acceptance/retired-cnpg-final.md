# Final result for the retired CNPG fixture

The last frozen CNPG workflow passed at source `53ddb67` in
[run 35229065005](https://github.com/jsell-rh/hypershell-stego/actions/runs/35229065005).
It began before the user selected external PostgreSQL as the only Hypershell
database model. It is a historical result, not a current CNPG support claim.

All 11 required tests passed. The complete Gateway case took 700.42 seconds.
Independent checks matched 1,438 source files, 416 repeated generation hashes,
the published compiler bytes, and the generated console image. The three
dashboard images were reviewed. Both browser services supplied complete startup
logs, traces, and metrics from three instances each.

The workflow proved access rules, event delivery, Gateway and database restart,
workload namespace recovery, encrypted state, account denial during provisioner
outage, account cleanup, and durable Gateway deletion. The database and operator
Jobs each had a 2,400-second deadline; the application Job had 1,800 seconds.
Scheduling took 252 seconds. No manual scheduling change was made.

Independent cleanup passed at 14:13:42 UTC on 2026-09-17. The application,
allocations, database runtime, operator runtime, and both test volumes were
absent. The shared test lease was clear. The full
[evidence record](retired-cnpg-final-evidence.json) retains hashes and limits.
No further CNPG test is required for the current product scope. Sandbox,
production capacity, and the remaining enterprise requirements are still open.
