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

## First measured baseline

[Run 35202114784](https://github.com/jsell-rh/hypershell-stego/actions/runs/35202114784)
passed at `dd8529e33dd1b8fb32989e96a53956607f190eb5`, with compiler
`00573709fb15a2a54de4242aa8fdbabee325179a` and Go 1.26.8 on Linux amd64.
All six required samples passed, with three timed scans per sample. Every scan
retained all expected grant and user identities. The container exited with
status zero and was not killed for memory use. CI verified container removal.
There was no separate operator inspection of the hosted runner after cleanup.

| Synthetic grants | Rows per scan | Pages per scan | Median time | Observed time range | Median allocated bytes per scan |
| --- | --- | --- | --- | --- | --- |
| 10,000 | 10,001 | 101 | 0.1028 s | 0.1017–0.1029 s | 15,004,778 |
| 100,000 | 100,001 | 1,001 | 1.0207 s | 1.0085–1.0333 s | 149,765,877 |

The process maximum RSS was 46,186,496 bytes, about 44.05 MiB. This differs
from cumulative allocation bytes: the garbage collector can reuse memory during
a scan. It is the high-water mark for the complete test process, including setup.
These two input sizes show approximately proportional scan time in this fixture.
They do not establish that relationship at other sizes or under concurrent load.

Independent verification checked the exact source archive, compiler pin,
executable hash, toolchain version, terminal container state, resource limits,
raw measurements, and result parser. The executable was not run locally.

| Record | SHA-256 |
| --- | --- |
| Source archive | `bfc13fde4adb11cb96835129b47907e43b032a77871345f169bd0d816dedcd92` |
| Test executable | `9867c14512449ada3526447865e613e5ab12b06adf5f8ff3658c9a4f439817b8` |
| Raw measurements | `ce814d6c96fae328e6e3db1e647477e791dc7d1f421907b55a72a66206712412` |

This closes the first complete retained-grant scan baseline. Full account and
journal cleanup costs, provider calls, transport, concurrent load, and complete
controller recovery remain separate requirements. No production SLO is claimed.
