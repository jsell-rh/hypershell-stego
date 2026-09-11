Hypershell pins STEGO compiler
`79c006cf1d4be2308b7087f5b6eedba570c269cf`. It adopts go-sdk 2.0.2,
http-application 1.5.2, and cli-application 1.6.2.

The common HTTPS client now preserves 304 Not Modified responses to GET and
HEAD. Redirects are still errors. The client does not follow Location.
The API, CLI, and SDK transports receive this change through generation.
The application has no local transport correction.

A bounded cluster check generated the application twice. All 129 generated,
state, and dependency hashes matched both passes, the post-test output, and
the checkout. TestGeneratedProjectInputManifest passed under race detection
in 1.053 seconds. The Job completed. Source, results, and Job status are in
`/tmp/stego-browser-runtime-gn_86o_n` on the test workstation.

STEGO's generated transport test checked cache responses and redirect denial
with HTTPS. Its browser runtime test checked status 304 and ETag through the
API proxy. The new browser component remains outside the registry. This
application change does not claim a complete Hypershell browser workflow.
Full application CI for this compiler pin remains a separate check.
