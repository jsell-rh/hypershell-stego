# Main API verification

[Run 35201603474](https://github.com/jsell-rh/hypershell-stego/actions/runs/35201603474)
passed at source `881379731d3b78be8344637942b6160e0e138c53` with compiler
`00573709fb15a2a54de4242aa8fdbabee325179a`. All 52 required tests passed with
no failed or skipped required tests. Independent checks matched all 1,369
source files and all 415 generated-file hashes in the committed, first,
second, and final snapshots and in the saved archive.

The parent cleanup restart test passed in 9.93 seconds. The deterministic
unfinished-claim test passed in 33.56 seconds and retained the original event
identity after lease expiry. The original API Job reached `Complete` without
a failed condition. Independent cleanup at `2026-09-17T08:57:29Z` found no test
runtime, fixtures, allocations, or held Lease. The next public workflow began
after this API workflow; its result remains separate.

The [module and journal results](common-browser-main-module-evidence.md) are
also verified. The public browser and complete main suite remain active. The
full enterprise goal remains open.

```json
{
  "run": "https://github.com/jsell-rh/hypershell-stego/actions/runs/35201603474",
  "source": "881379731d3b78be8344637942b6160e0e138c53",
  "required_tests_passed": 52,
  "source_files_match": 1369,
  "generation_hashes_match": 415,
  "independent_cleanup": {
    "checked_at": "2026-09-17T08:57:29.585752+00:00",
    "api_run": 35201603474,
    "api_jobs": [],
    "api_fixtures": [],
    "service_runtime": [],
    "service_fixtures": [],
    "allocations": [],
    "cnpg_operator_runtime": [],
    "cnpg_runtime": [],
    "lease_holder": ""
  },
  "scope": "API creation, access, events, deletion, restart, and regeneration at the named source. This does not prove a rendered Gateway dashboard."
}
```
