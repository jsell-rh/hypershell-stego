# Dashboard public transport check

Run `35145842952` at `d4ea7f7` passed repeated generation with 366 matching
file hashes. Its contract, controller, schema, and cleanup checks passed. In
the live workflow, both Gateway servers, upstream dashboard applications,
and generated browser backends became ready without container restarts.
This verifies the identity fixture ingress correction.

The workflow also verified separate Gateway database logins, denied access
between databases, Gateway RPC access rules, and provider data recovery after
Gateway Pod replacement. It then failed on the first dashboard navigation.
The retained screenshot and page text show `ERR_CONNECTION_CLOSED`. No HTTP
response appears in the retained browser response list. This is not evidence
that the rendered dashboard or its login completed.

The prior controller probe used verified HTTPS and checked the public console
address. The next test also checks that address from the browser fixture Pod.
It requires the exact readiness response and the protected-document redirect
with its return path. Each request has a five-second deadline. Browser failure
evidence now retains connection error categories as well as HTTP responses.
It limits request tracking to 64 entries and saved network records to 128.
It does not save request headers, query strings, or raw network error text.

The test retains the login correction described in [dashboard login](dashboard-login.md).
Neither that correction nor the new diagnostic checks has a passing live
result yet. The transport failure must be resolved before the editor, telemetry,
access revocation, session recovery, and full deletion gate can pass.

The failed workflow completed and removed its resources. Independent operator
inspection at `2026-09-16T20:35:13.920529Z` confirmed that the test runtime,
fixture resources, and allocated namespaces were absent and the test lease was
empty. An earlier inspection occurred during cleanup and did not pass. The
subsequent complete inspection supplies the cleanup result.
