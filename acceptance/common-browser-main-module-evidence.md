# Main module and journal results

These checks apply to source `881379731d3b78be8344637942b6160e0e138c53`
and compiler `00573709fb15a2a54de4242aa8fdbabee325179a`.

The [management asset build](https://github.com/jsell-rh/hypershell-stego/actions/runs/35201603516)
passed. Independent inspection matched the complete source archive hash, compiler
pin, ZIP entries, and checksum. Its 54-entry, 852,967-byte bundle matches the
committed bundle and the qualified branch bundle.

The [Gateway module check](https://github.com/jsell-rh/hypershell-stego/actions/runs/35201603464)
passed. All 129 archived source files match the module. Repeated generation,
dependency checks, private deployment checks, and image inspection passed.
The image contains the checked executable and uses UID and GID 65532. Its
executable is byte-for-byte identical to the one in the qualified branch run.

The [journal recovery check](https://github.com/jsell-rh/hypershell-stego/actions/runs/35201603483)
passed all 28 required tests, with no failed or skipped tests in the saved log.
Independent verification compared the result list with the required set in the
exact tested workflow source.

The API, public browser, and full main suite have separate active checks. These
module and journal results do not establish their completion. The prior branch
qualification remains in [the composition record](common-browser-composition.md).
The full enterprise goal remains open.

The following records retain the exact artifact and executable hashes.

```json
{
  "console-assets": {
    "checked_at": "2026-09-17T08:50:28.484614+00:00",
    "source": "881379731d3b78be8344637942b6160e0e138c53",
    "run": "https://github.com/jsell-rh/hypershell-stego/actions/runs/35201603516",
    "compiler": "00573709fb15a2a54de4242aa8fdbabee325179a",
    "source_archive_sha256": "bf29db1facbe3f827bad296d5beaec3709498e1563fd95b48007ddbfdc4a7d98",
    "asset_sha256": "677404ba290aa54e40fe78e656a6cbd31df273820036e7453b2cbd24eaaac6ec",
    "asset_bytes": 852967,
    "asset_entries": 54,
    "matches_qualified_bundle": true,
    "scope": "Exact source, compiler, ZIP entries, and checksums passed. The bundle matches the qualified branch artifact. Complete application behavior remains a separate gate.",
    "matches_committed_bundle": true
  },
  "gateway-module": {
    "run": "https://github.com/jsell-rh/hypershell-stego/actions/runs/35201603464",
    "source": "881379731d3b78be8344637942b6160e0e138c53",
    "compiler": "00573709fb15a2a54de4242aa8fdbabee325179a",
    "source_files_match": 129,
    "repeated_generation_matches": true,
    "browser_image": "ghcr.io/jsell-rh/hypershell-stego-gateway-console-ci@sha256:0f87099c3a1341793d74185436b3e7de099f307be189844f9c8d4f0fee979831",
    "image_id": "sha256:829a3878af1761523e82b57b8bc63a42ab7368c4a5dca456349d20acb581666d",
    "binary_sha256": "07a677758dcc555968926360cdd2fb4c1227b4a5835d8fc6c117774211babc06",
    "generated_source_sha256": "78f8131930f910ca7f73d6dbdee8259ffa4342349b0506e34c350324491cfd5c",
    "nonroot_user": "65532:65532",
    "entrypoint": [
      "/service"
    ],
    "binary_matches_module": true,
    "pulled_digest_matches_build": true,
    "scope": "Generated source, dependency scan, module builds, exact image binary, and published image digest passed. This does not prove a running Gateway dashboard or anonymous image pull access."
  },
  "journal": {
    "run": "https://github.com/jsell-rh/hypershell-stego/actions/runs/35201603483",
    "source": "881379731d3b78be8344637942b6160e0e138c53",
    "required_tests_passed": 28,
    "sha256": "dacc60389604893976506cea9c51a287c137c085560df614551d0e6ca397c895",
    "bytes": 74254,
    "scope": "Journal recovery at the named source. Live application tests have separate results."
  }
}
```
