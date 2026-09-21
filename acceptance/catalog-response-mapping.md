# Catalog response mapping

This candidate uses STEGO compiler `6d68417d` to generate the protobuf response
mappings for ManagedCluster, GatewayRelease, and GatewayNetwork. Hypershell
declares their fields, kinds, and href prefixes. STEGO supplies checked
conversion and complete output field coverage.

The application retains catalog authorization, request validation, pagination,
watch delivery, and public error policy. The new tests compare every field with
the prior response shapes for absent, empty, and set optional values. They also
check invalid stored text without a partial response or a private error value.
The existing invalid timestamp, denied request, list, and watch tests remain.

Hosted generation checked all 430 generated and module files. It added one
mapping file and changed only the CLI compiler identity and three generation
records elsewhere. No compiler or application binary ran on the workstation.
See the [generation evidence](catalog-mapping-generation-evidence.json).

The generation input intentionally had the prior committed output. Three seed
checks stopped at the generated-file comparison before runtime tests. The
candidate includes the reviewed output and requires new source checks. The
archive review also corrected a header assertion to account for the compiler's
standard header before the generator header. The original records are retained.

Application checks, repeated generation, signed images, restart checks, and
the complete live Gateway workflow remain required. This change does not
complete Gateway or grant mapping, stored JSON conversion, REST mapping, the
30-second cleanup target, or production capacity qualification.
