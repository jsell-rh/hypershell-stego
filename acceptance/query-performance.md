# Gateway list query performance

STEGO `2a04dbe34000bed5cafa956e3b580214149fe647` generates stable field order for
implicit and related filter maps. The application still supplies its access
rules. The generator keeps equivalent filters on one prepared-query shape.
Its PostgreSQL regression reduced six cached statements to two: one count and
one page query. It also checks that every request returns the permitted row.

## Statistics and test conditions

The earlier generation measurement used newly loaded tables before statistics
collection. A database profile showed that the access-filtered count dominated
the request in both the old and new compiler output. The page query took about
0.2 ms, while the count took about 8 ms.

A plan probe on the actual fixture found a nested-loop join before `ANALYZE`.
It scanned the grant index 200 times, removed 14,950 join rows, and touched about
12,400 shared buffers. It took about 6.5 ms. With current statistics, PostgreSQL
used a hash join, touched 11 buffers, and took about 0.1 ms. Both custom and
generic prepared plans had the initial problem.

The normal service, REST, and gRPC list benchmarks now collect statistics after
loading their fixture. `BenchmarkGatewayFilteredPageFreshStatistics` retains the
initial state as a separate case. Application requests do not collect statistics
or force a planner setting. An import procedure must account for statistics
collection. The benchmark setup does not prove a production capacity limit.

Three paired service samples used 200 Gateways, 100 visible to the caller, and
pages of 20. Each sample read 1,000 pages with current statistics. Before stable
field order, the samples averaged 1.08–1.19 ms per page. After the change, they
averaged 1.08–1.12 ms. Both allocated about 111 kB per page. These ranges overlap.
The measured benefit is stable cache use; this sample does not prove a latency
improvement. The generation metadata and row-allocation cost remain.

## Validation

The generated PostgreSQL cache regression passed with race detection. The full
compiler run passed apart from an old source-text assertion about map iteration;
that assertion was updated, and the complete PostgreSQL generator package then
passed. `go vet ./...` passed.

Six application checks passed with race detection in 23.030 seconds. They cover
REST and gRPC access, filters before counts and paging, rejected invalid filters,
current-status search, mutation events, restart, and retained recovery IDs.
Pinned regeneration reproduced the tested generated code and dependencies.

Large-table status filters, concurrent request capacity, startup bursts, and
recovery backlogs still need measurements. Keep those requirements separate from
this small list fixture and its current-statistics result.

The updated transport benchmarks also passed. A sample of 100 reads averaged
2.51 ms for the in-process REST handler, including JWT checks, and 2.91 ms through
gRPC to a separate generated process after TLS setup. These are local samples
with different transport boundaries, not production latency guarantees.
