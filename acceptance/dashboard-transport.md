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

## Redirect probe correction

Run `35147622468` at `137d375` passed the readiness request from the browser
fixture, then failed its new `/workspaces` check. That check used the generated
service client, which rejects all redirects other than HTTP 304. It cannot
return the HTTP 303 response that the test requires. The check was incorrect.
Its zero response status does not establish another connection failure.

The probe now uses the existing browser fixture transport with verified TLS.
It sets a five-second request deadline, limits each response to 1 KiB, and
returns redirects without following them. It still checks the exact readiness
body, redirect status, destination, and return path. The service client's
redirect restriction remains unchanged.

A focused TLS-server test passed in 0.027 seconds. It covers the valid response,
foreign redirects, changed return paths, oversized responses, wrong readiness
and status, and caller cancellation. It also proves that the probe does not
follow a redirect or call the original client's redirect handler. The live
workflow must be repeated after complete cleanup. The earlier Chromium
`ERR_CONNECTION_CLOSED` remains unresolved.

## Verified browser certificate

Run `35149129268` at `b63eca8` passed both HTTPS probe requests from the
browser fixture. The browser then reported `net::ERR_CERT_AUTHORITY_INVALID`
for `/workspaces`. Independent operator checks found that both public console
hosts passed CA and hostname verification. Each host sent its configured leaf
certificate without the root CA. The test had supplied the CA's public-key
hash to Chromium instead of the leaf's hash.

Chromium checks the supplied hashes against the certificates that the server
sends. See its [certificate verifier](https://chromium.googlesource.com/chromium/src/+/HEAD/services/network/ignore_errors_cert_verifier.cc),
file blob `5bbf537b285f8b2e09858d7e8c2bcceb1cd656f0` at inspection.
The absent CA could not match. This explains the certificate error in this
run. It does not establish the cause of the earlier connection-closed error.

The test now obtains the leaf certificate from the verified HTTPS probe and
uses that certificate for its browser pin. Both probe requests must verify
the same leaf. A failed response, unverified peer, changed certificate, or
certificate larger than 16 KiB returns no certificate. Production TLS and
the browser's existing test-pin mechanism are unchanged.

Focused tests passed in 0.028 seconds. They include an unverified-peer
rejection and a CA-signed server that omits the root from its chain. A live
result for this correction is still required.

Run `35149129268` completed with failure after 288.81 seconds in the live
test. Its evidence was collected. Independent operator inspection at
`2026-09-16T21:07:51.822175Z` confirmed no remaining test runtime, fixture
resources, or allocated namespaces, and an empty test lease.

## Authorization callback boundary

Run `35150654630` at `3d8f9d6` passed repeat generation with 366 matching file
hashes. Both dashboard applications and generated backends were ready without
restarts. Verified SQL isolation, Gateway RPC denial and data recovery, and the
public HTTPS probes passed. Chromium then reached `/auth/callback` with HTTP
400. The page showed only `Bad Request`; it did not show a TLS error. The live
test failed after 278.09 seconds.

The backend requests `openid profile email`. The common Keycloak browser client
removes shared scopes and supplies client-owned claim mappers. Its provider
checks request only `openid`. This is a component contract mismatch. The live
artifact did not retain the authorization error parameter, so it does not prove
that this mismatch caused the callback failure. STEGO candidate `42c7ea1` adds
minimal default scopes and explicit additional scopes, with common CI pending.
The application has not adopted that candidate yet.

Browser failure evidence now saves only a fixed authorization error category
from a callback on the expected origin. It does not save code, state, provider
error descriptions, or complete URLs. This will distinguish a provider rejection
from other callback failures in a later test.

Independent operator cleanup passed at `2026-09-16T21:23:58.133877Z`. Runtime,
fixture resources, and allocated namespaces were absent, and the lease was
empty. The complete rendered dashboard, editor, telemetry, recovery, and access
gate remains open.
