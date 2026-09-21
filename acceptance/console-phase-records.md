# Console check phase records

The main web-console job in run 35588644726 ended with exit code 124 after
its UI build. The log does not identify the command that timed out. The
successful UI tests do not prove that asset checks and regeneration passed.

The console check and compiler setup now record fixed phase names, times,
and exit codes. Set `STEGO_CHECK_PHASE_ROOT` to an absolute directory to
keep one file for each script call. The records contain no commands,
arguments, credentials, or environment values. CI uploads only these public
TSV files, including when a check fails. A missing final record is not a pass.
The existing command deadlines and compiler signature checks still apply.

The manual `console` scope runs the same web-console job as the full check.
It includes UI checks, asset comparison, and regeneration. It does not prove
that the other jobs passed. Push and pull request checks retain all jobs.

Five small stub tests check failure codes, phase order, separate files,
file permissions, and removal of credentials before UI commands. A later
successful CI run cannot establish the cause of the original timeout.
