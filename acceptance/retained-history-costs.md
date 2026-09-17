# Retained grant history costs

Run the `Retained grant history costs` workflow in hosted CI. Do not run its
benchmarks on the developer workstation. The workflow uses one CPU and 768 MiB
for the test process, and one CPU and 512 MiB for PostgreSQL. It does not use
the jshell cluster or privileged containers. The test container has a read-only
root, no capabilities, no privilege escalation, and a fixed process limit.

The benchmark reads complete histories of 10,000 and 100,000 synthetic grants,
plus the Gateway owner grant. Ninety percent of the synthetic grants are
deleted. Every scan checks the exact ordered grant and user identities. An
unauthorized request must fail before measurement. The page size is 100.

Each size has three samples with three complete scans per sample. Each scan
has a one-minute context. The complete test has a four-minute timeout and an
outer five-minute limit. The job has a twenty-minute limit. A failed, incomplete,
skipped, or out-of-memory result cannot pass the result verifier.

Measurements include the generated SQL cursor, domain authorization, transaction
cost, and row validation. They exclude provider calls, REST and gRPC, and complete
controller recovery. The test uses the explicit literal-loopback database TLS
exception. It does not measure production TLS cost. The race detector is disabled
for these timing measurements; separate correctness gates use it.

The result includes elapsed time, allocation bytes and count, rows, pages, and
process maximum RSS. Linux RSS is a process high-water mark. It includes fixture
setup and earlier cases, so it is not memory use for one scan. Container limits
remain the hard bound. PostgreSQL memory use is bounded but is not included in
the Go process RSS result.

Source commit, source archive hash, compiler pin, toolchain version, executable
hash, terminal container state, limits, and raw measurements remain in the CI
artifact. These measurements provide a baseline. They do not establish a
production SLO, end-to-end cleanup capacity, or complete enterprise readiness.
