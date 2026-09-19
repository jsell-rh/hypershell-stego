# Account cleanup ownership

Gateway recovery scans every retained account row and provider journal for a
deleted Gateway. It keeps that work eligible after Gateway finalization. The
account recovery stream also used to repeat deletion for those same deleted
account rows. Both paths ran within each scheduler round.

The account stream now reads the retained parent Gateway before provider work.
If that exact Gateway is deleted, the Gateway scan owns the work. A live or
absent parent still uses account recovery. A storage failure leaves the action
eligible for a later round. A wrong result type, wrong parent ID, missing
retained-reader contract, or invalid parent ID stops the invalid action.

The parent read and account deletion share the existing four-second action
context. This selection does not cache completion, change account or Gateway
state, or authorize new provider work. It does not remove rows, journals,
inventory checks, or repeated Gateway scans. If the parent is deleted after
the read, a repeated provider call is still permitted and must remain safe.

Hypershell owns this rule because it defines the account-to-Gateway relation and
selects which application cleanup path has responsibility. STEGO still owns
the bounded scheduler, saved scan, retry, state protection, and provider client
lifecycle.

The ninth capacity result prompted this change. Aggregated traces cannot prove
how much time the repeated account stream used. Hosted correctness checks and
a later unchanged-fixture capacity run are required. No timing gain is claimed.
