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
