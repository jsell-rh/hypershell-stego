# Control account installation

The browser fixture uses the generated STEGO control account guard. The operator
creates the allocator and Gateway workload accounts after the generated policy
is installed. Both accounts have automatic token mounting disabled. CI receives
its bindings only after the policies, accounts, and immutable installation record
are ready. Neither CI nor an application worker receives the installer capability.

The browser CI identity can read the new admission policy. Its permission check
also requires deletion of the workload account to be denied. Existing checks
still require denial of allocator account deletion, cluster role creation,
foreign secret reads, and installation record changes.

Ten small local fixture checks passed, including failure before account creation
when policy installation fails. The new compiler pin, regenerated output, and
complete live consumer workflow are still required. This preparation does not
establish consumer adoption or close the enterprise goal.

The compiler, common registry, and installer tooling now select `931f712`.
The compiler comes from its published immutable release. The common installer
verified the exact revision, compiler hash, and signatures. The same source
passed full compiler CI and the live control-account regression. Hosted
regeneration and the complete consumer workflow remain required.

Hosted regeneration passed at `06f5f86`. The imported archive matches all 415
output files and modes. All three drift checks passed. The compiler installation
record matches the verified release. The application allocation policy is
unchanged. The generated manifest adds the account guard, its binding, and an
unbound installer role. The live consumer workflow remains required.

The corrected fixture verifier at `fed0042` passed the hosted installation plan
[35260879782](https://github.com/jsell-rh/hypershell-stego/actions/runs/35260879782).
The plan checks 1,450 source files and 68 account cases. It preserves the 25
installed resources and adds only the generated guard, its binding, and the
unbound installer role. The fixture adds its fixed account to the reserved names.
The verifier rejects other account, permission, and guard changes.

The operator installed the plan on jshell. Server validation and policy type
checks passed. All 28 installed resources match the record. The allocator,
workload, and fixture account identities are unchanged. CI gained only read
access to the new policy. The immutable installation record was replaced with
UID preconditions. Cleanup passed and the lease was released.

Hosted checks passed for the generated console, seven images, 231 UI tests,
the rendered browser workflow, provider recovery, and 28 required journal tests.
The earlier plan failure is retained in the evidence record. Full core checks
and the [live consumer run](https://github.com/jsell-rh/hypershell-stego/actions/runs/35261639502)
remain pending. This result does not close the enterprise goal.
