# Main API and core checks

The main API workflow passed at `0175b0b` in
[run 35188311212](https://github.com/jsell-rh/hypershell-stego/actions/runs/35188311212).
All 51 required tests passed. All 1,360 source hashes match the exact Git source.
All 415 generated-file hashes match the committed, first, second, and final
snapshots and the saved source archive. The original test Job reached `Complete`.
Independent cleanup passed at `2026-09-17T06:36:45Z`, before the queued CNPG
workflow created its resources.

The core job in
[run 35188311598](https://github.com/jsell-rh/hypershell-stego/actions/runs/35188311598)
also passed at the same source. It passed 307 top-level tests, with 651 test pass
events. Its four named live exclusions have separate workflow gates. The CNPG
job in that run is a separate result and is not yet complete.

These records prove the listed checks at the named source. They do not close
the full enterprise goal.

## API record

```json
{
  "run": "https://github.com/jsell-rh/hypershell-stego/actions/runs/35188311212",
  "source": "0175b0bb46494a62e9adfbb06053229dda3ddaf7",
  "required_tests_passed": 51,
  "source_files_match": 1360,
  "generation_hashes_match": 415,
  "independent_cleanup": {
    "checked_at": "2026-09-17T06:36:45.597969+00:00",
    "api_run": 35188311212,
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

## Core record

```json
{
  "run": "https://github.com/jsell-rh/hypershell-stego/actions/runs/35188311598",
  "source": "0175b0bb46494a62e9adfbb06053229dda3ddaf7",
  "job_id": 105095066035,
  "core_result": "success",
  "test_pass_events": 651,
  "top_level_passes": 307,
  "separate_live_exclusions": [
    "TestGatewaySQLUsesDurableStateAndRetainsSuppliedServer",
    "TestNamespaceCountWithLiveKubernetes",
    "TestGeneratedKubernetesBrowserGatewayWorkflow",
    "TestGeneratedKubernetesServiceGatewayWorkflow"
  ],
  "acceptance_seconds": 1554.046,
  "log_sha256": "66a00864e12c8ad09bad217595962b4334d0369989e281e3e66ba1563266db3e",
  "scope": "The core suite at this exact main source. Rendered and live cluster tests have separate results."
}
```
