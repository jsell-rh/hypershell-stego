# Gateway and grant REST mappings

This candidate moves field conversion for Gateway and RoleBinding responses
into STEGO. The application selects current observations, resolves the creator,
and checks access. The common converter checks and copies the resulting values.

All 24 Gateway fields and all nine grant fields have explicit mappings. The DNS
list has the same byte and item bounds as the gRPC response. Grant scope must
match the OpenAPI enum. An invalid row returns a private error with no partial
response. List selection and access checks run before conversion.

The existing acceptance checks use separate response decoders. Their assertions
remain unchanged. New checks cover field presence, owned storage, observation
selection, invalid stored values, list bounds, access, restart, and recovery
after repair. The required live API set also includes the complete grant
REST/gRPC/event/restart workflow.

This source branch contains application source and compiler pins only. Generated
output is pending. The compiler release must pass signature and installation
checks before CI regeneration starts. Application tests, generated output review,
and the bounded live Gateway workflow remain required. This is not an accepted
application release.
