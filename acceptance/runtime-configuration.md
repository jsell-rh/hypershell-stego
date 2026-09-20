# Worker runtime configuration

The namespace allocator and Sandbox count worker use STEGO typed configuration.
Hypershell declares environment names, defaults, and limits in `service.yaml`.
The workers pass the validated settings to the existing generated clients and
domain constructors. Connection cleanup remains explicit in each worker.

Configuration is read before provider setup. Invalid scalar input returns a
private typed error. It cannot include a supplied value or a parser error.
Kubernetes connections still use the system trust store when the CA file is
empty. RPC connections still require their explicit trust file. Certificate,
token, URL, namespace, and assignment checks remain in their existing providers.

The Sandbox count watch limit retains its zero value, which selects the client
default. Resync retains its zero value, which selects the domain default of two
minutes. The domain constructor still rejects nonzero resync values below one
second. A value above five minutes fails before any provider setup.

An explicitly empty numeric setting now fails. Previously, an empty setting
selected the default. Omit the setting or use its declared zero value to select
the default. Integer values must use decimal form without a plus sign or leading
zeros. These rules prevent ambiguous operator configuration.

Compiler `8eeb1169` passed its branch and main checks and is published as an
authenticated immutable release. Hosted
[regeneration run 35538564130](https://github.com/jsell-rh/hypershell-stego/actions/runs/35538564130)
passed for seed `72cc1786`. Independent review checked all 428 output and module
files. Of those files, 422 are unchanged. The new configuration package matches
the declared four groups and 12 fields. Shared helpers match the compiler
template. The CLI change contains only compiler identity; three constructor
diagnostic indexes increase by one without changing the calls or their order.

All 419 generated output hashes and 41 captured input hashes match the state
records. Module files and all Gateway console runtime files are unchanged, so
the selected Gateway console module remains valid. No compiler or application
binary was run on the workstation. The first review stopped at an incorrect
estimate of one changed constructor diagnostic index. The complete diff showed
three. The same artifacts passed after that estimate was corrected.
See the [generation evidence](runtime-configuration-generation-evidence.json).

Source `7ed1a7b7` passed the full hosted check with 1,165 core cases and 364
top-level tests. Its service Deployment workflow passed in 119.43 seconds.
Review checked seven signed images, 429 generation hashes, and ready API and
identity worker Pods before and after replacement. All observed ready Pod restart
counts were zero. Cleanup removed the test namespace and owned resources,
released the shared Lease, and left all 32 standing resources unchanged.
See the [service evidence](runtime-configuration-service-evidence.json).

The base browser workflow remains under review. The later shared worker settings
change has separate [hosted evidence](control-worker-configuration-ci-evidence.json).
Complete live count and browser checks remain required before main promotion.
This change does not complete worker connection assembly or the remaining
enterprise requirements.
