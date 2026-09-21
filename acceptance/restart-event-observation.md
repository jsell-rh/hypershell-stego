# Event observation after restart

The provider deadline tests stop and restart the API before they check the
recovery event. A stopped process can leave an unfinished queue claim. The
generated worker keeps that claim until its lease expires. Its default lease
is thirty seconds.

The tests now require the queue to become empty within the existing generated
recovery limit: one lease, one delivery attempt, and five seconds for polling
and acknowledgement. They then use the unchanged ten-second Kafka read and
the same event contract checks. The change applies only to the event after
restart. Earlier event checks retain their existing limits.

This does not change production timeouts, clear leases, or discard events.
`TestGeneratedRuntimeRecoversUnfinishedClaim` separately claims real messages
before runtime startup. It checks that the new process retains the active
claims, waits for expiry, and delivers the original message identity.

The earlier failed run `35547597853` remains a failed result. Its queue record
showed active leases with 18.626 seconds left after a ten-second event wait.
That record does not prove which operation retained those leases. This change
corrects the test's recovery window; it does not establish the cause of that
historical failure or prove a ten-second recovery service level.
