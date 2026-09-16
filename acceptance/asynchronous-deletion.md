# Durable Gateway deletion

The approved contract is HTTP 202 after a durable request commit. New account
reservations must then fail. The Gateway remains visible as deleting until every
cleanup owner and retained target is complete. Finalization and the final event
share one transaction. Late cleanup must not restore public visibility.

This work is on `codex/gateway-cleanup-20260916`. It is not qualified for the
default branch. STEGO supplies stored finalization, pending-deletion reads,
checkpoints, bounded scans, scheduling, and HTTP response handling. Hypershell
supplies account policy and the Gateway owner set.

Source `711659a` reproduced the former failure with three retained accounts:
three interrupted requests reached only one account. Source `c0ab6e5` passed
`TestGatewayAccountCleanupRecoveryReachesTailAfterRestart` and
`TestGatewayAccountCleanupRecoveryKeepsPageCheckpoint` on jshell. The first test
proved that an account failure does not stop independent cleanup. The second
proved resumption after a stored page boundary without repeating that page.
Generation hashes matched. Job `gateway-api-06e3e8773918` and its private
fixtures were removed. Tests took 1.18 and 0.53 seconds. These are correctness
checks, not capacity measurements.

An earlier recovery attempt stopped before tests because its compiler build
record lacked Git metadata. The corrected build came from a clean clone with
its revision verified. Preserve both attempts. Results and frozen source are in
`/home/jsell/.local/state/stego/runs/gateway-cleanup-20260916`.

Source `31c8646` enables durable deletion in REST and gRPC and connects the account
worker to STEGO's scheduler. Source `9386f88` avoids repeated events for unchanged
cleanup state. The new process gate must prove request and final-event rollback,
access filtering, account rejection, pending reads, process replacement, final
visibility, and event delivery. It is still running on the earlier source.

The console, earlier synchronous-deletion tests, and field search must be aligned
before this branch can replace the default branch. In particular, phase search
must agree with the public `Deleting` value. Real Keycloak, browser, CNPG, and
regeneration gates must then pass. Large provider inventories remain an open
requirement; the existing inventory call still uses a bounded full scan.
