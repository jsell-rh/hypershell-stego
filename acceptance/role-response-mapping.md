# Role response mapping

The application declares the Role fields in `service.yaml`. STEGO generates
the response model and conversion. Hypershell retains authentication, Role
lookup, filtering, paging, projection, and grant policy in its domain services
and transport adapter.

The permissions field is an optional JSON object. Absent source bytes omit
the field. A present empty object remains `{}`. The generated conversion
preserves JSON numbers without conversion to floating point. The application
sets these bounds: 262,144 raw bytes, 4,096 nodes, 16 container levels, and
16,384 bytes for each decoded name, string, or number. These are response
conversion bounds, not Gateway or account capacity limits.

Malformed objects, invalid Unicode, duplicate decoded keys, and limit
violations return the private conversion error. An invalid stored Role must
produce the existing HTTP 500 shape without a partial list. Authentication
and list selection occur before conversion. Field projection does not hide
an invalid Role that the list has selected. Count-only requests do not
convert Role records.

The compatibility baseline uses the prior handwritten adapter at
`17983c64a0740b9f83f6254f395770ec3aa9179f`. Its focused run `35591090433`
passed 261 cases, including all 256 prior transport cases. It checks every
Role field, absent and empty values, and exact JSON number values. Acceptance
checks decode the original HTTP shape independently of generated Go types.

The new conversion must also pass ownership, malformed input, exact bound,
and stored-fault tests. The stored-fault test checks private errors, filtering,
projection, count-only requests, repair, and restart. The bounded jshell API
suite includes Role discovery and this stored-fault test. Existing grant,
CLI, network, and browser viewer checks remain required.

The immutable compiler release is `52306a6b16f2e68e55d7884104a4614f82f1938b`.
Its signatures and installation passed verification. Generation run
`35591897980` changed six files: the Role mapping, its common object decoder,
the CLI compiler identity, and three state records. The other 429 generated
and module files are unchanged. The decoder matches the released template.

Candidate application checks and the complete Gateway workflow remain pending.
