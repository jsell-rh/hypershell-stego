# Gateway cleanup phase review

Run [35481347942](https://github.com/jsell-rh/hypershell-stego/actions/runs/35481347942)
used source `80b0e00bc6f7088ceddb9ea70e871a452df08a3f` and signed compiler
`ee348b819bb43a738a6aad2fc14cb76ce3b1ec21`. Its eleven required tests passed.
The full browser workflow took 720.95 seconds. Source and generated file hashes,
compiler records, four screenshots, account records, and cluster cleanup were
checked. The shared lease is free. All 32 standing installation resources are
unchanged. See [the evidence](cleanup-phase-evidence.json).

The added timing records expose a missing deletion invariant. The Gateway was
finalized while retained state namespaces still existed. Passing the earlier
workflow checks did not prove that finalization waited for all cleanup.

| Observation after the accepted deletion | Seconds |
| --- | ---: |
| Gateway namespace first observed absent | 13.82 |
| Sandbox namespace first observed absent | 22.40 |
| SQL and workload cleanup first observed complete | 25.49 |
| Gateway first observed finalized | 29.73 |
| Console state namespace still observed present | 41.22 |
| Gateway state namespace still observed present | 51.69 |
| Gateway state namespace first observed absent | 52.77 |
| Final account proof complete | 54.34 |

These are sequential read-return observations, not exact provider transition
times. They still prove the ordering defect: a state namespace was observed
present after finalization was observed. The account proof used about 1.42
seconds after the final owner GET. The 30-second whole-Gateway target remains
unproved. This is one normal deletion with 100 REST-created accounts and 100
verified token issuances; it is not a production capacity test.

The account cleanup flag changed from complete back to pending 13 times. The
combined cleanup predicate changed back to pending seven times. This requires
separate investigation. It does not establish the cause of namespace removal
latency. Do not reduce error backoff based only on these observations.

The candidate fix declares an `allocation` cleanup owner and uses STEGO's
existing conditional cleanup records. It also blocks ManagedCluster deletion
until allocation cleanup is complete. A database regression reproduced the
old defect; candidate checks are in progress. The fix has not yet passed the
complete live workflow. A new live check will stop the allocator, restore SQL
access, require the deleting Gateway through REST and gRPC, then resume the
allocator and require complete removal.

Native Sandbox traffic, live Kata execution, RDS failover, and remaining
enterprise requirements are outside this result. Their gates remain open.
