# Account cleanup verification

The allocation workflow observed the account cleanup flag return to pending
14 times. A separate regression reproduced one cause at source `bf6b4e5` in
[run 35486074519](https://github.com/jsell-rh/hypershell-stego/actions/runs/35486074519).
The test completed cleanup of 101 retained journal entries and sealed their
scope. A reconstructed service then read one clean verification page. No
provider action failed, and the scope and resource generation did not change.
The application nevertheless changed the completed account observation to
pending. The other 169 passing cases from the prior journal check still passed.

The candidate retains the existing observation during a clean, incomplete
verification pass of a sealed scope. It makes no observation write in that
case. STEGO still owns scan progress, failure retention, conditional writes,
scope sealing, and invalidation when resource inputs change. The application
selects how those results affect its account cleanup policy.

A failed action or inventory check still records pending cleanup. A changed
resource input resets the observation through the generated storage contract.
The new checks require both behaviors. The candidate has not yet passed CI or
the complete workflow. This change does not establish the cause of namespace
removal latency or prove the 30-second whole-Gateway target.
