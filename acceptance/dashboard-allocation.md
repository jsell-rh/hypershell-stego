# Console storage allocation

The namespace allocator and Gateway workload worker use the operator's
`HYPERSHELL_GATEWAY_CONSOLE_DOMAIN` for their assigned managed cluster. When
the domain is set, the allocator prepares the console state namespace before
the Gateway workload namespace. The domain must pass the same hostname checks
as the console origin.

Allocation does not depend on `console_address`. That field reports a verified
endpoint and can be empty during initial provisioning or recovery. The worker
publishes the address after it verifies the complete console. STEGO continues
to enforce namespace ownership, permissions, and cleanup boundaries.

[Cluster run 35139202243](https://github.com/jsell-rh/hypershell-stego/actions/runs/35139202243)
failed before the Gateway became ready. The old allocator waited for a console
address before it created console storage. The console could not become ready
without that storage. Both Gateway Pods ran, but no console state namespaces
were allocated.

The regression check covers an empty initial address, a published address, and
an address cleared during recovery. It also covers consoles disabled by the
operator and invalid domain configuration. Local checks passed. The corrected
complete cluster workflow still requires verification.
