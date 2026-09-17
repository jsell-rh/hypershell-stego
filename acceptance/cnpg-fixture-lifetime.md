# CNPG fixture lifetime

The database fixture previously expired after 25 minutes. The application Job
allows 30 minutes and starts after database readiness. The database could thus
expire before the application's allowed test window ended.

The database now uses the same 40-minute resource limit as the CNPG operator.
That budget covers the 30-minute application Job, the six-minute database
readiness wait, and a three-minute cleanup reserve. The credential gate still
requires 45 minutes. The application deadline remains 30 minutes. CPU, memory,
storage, instance counts, and immediate lifetime-owner cleanup are unchanged.

One common constant supplies the database and operator templates and their
admission limits. A regression test reads the application Job's actual deadline
and checks the dependency budget against it. It also checks both generated
templates and their admission policies.

This change has not been applied to the running cluster test. Run 35224348179
retains its original source and limits. Its final result is still required.
The new fixture requires a checked installation update before a live repeat.
