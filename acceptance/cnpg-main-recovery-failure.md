# CNPG Gateway recovery failure

[Run 35212810788](https://github.com/jsell-rh/hypershell-stego/actions/runs/35212810788)
failed at source `8852dd376458ed1381ca039720d061511ca99f52`. The selected compiler
was `00573709fb15a2a54de4242aa8fdbabee325179a`.

The browser workflow reached a ready Gateway and created and read provider data.
After Gateway Pod replacement, its next provider read reached the ten-second
RPC deadline. The error was `DeadlineExceeded`. This does not establish data
loss. The stored result has no observation from a new RPC connection at that
point, so the cause remains open. The full workflow failed after 278.95 seconds.
It did not reach the CNPG database replacement or later recovery checks.

Independent checks matched 1,398 source files and 415 generated file hashes.
The two generation results match. The check for generation changes after the
tests was not reached. Ten earlier top-level tests passed. This run does not
qualify the complete CNPG workflow.

The application Pod waited 232 seconds for scheduling. No manual placement
change occurred. A proposed secondary replacement stopped before any write
because the application Pod was already assigned.

CI cleanup and independent cluster reads confirmed removal of the application,
allocated namespaces, CNPG test runtime, private test inputs, and both database
volumes. The shared live-test Lease has no holder. The operator-installed test
configuration remains.

The retained evidence archive has SHA-256
`b73f26f8c2b8e2d667332afe2a5e6bfc67b5ce1f9644e7c9976552d020b198cf`.
The deployment log has SHA-256
`63e30a6d6d7501bd245b30e0a217bdd9c7a3a3c1cbab1203fb36e175bf97a368`.
A later check must distinguish a failed read from a successful read with changed
data. Failure observations must preserve the original failed result.
