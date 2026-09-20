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

This source is a draft. Compiler release qualification, regeneration, hosted
application checks, and complete live workflow checks are required before main
promotion. It does not complete worker connection assembly or the remaining
enterprise requirements.
