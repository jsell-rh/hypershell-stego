# Main browser workflow evidence

Run [35188311004](https://github.com/jsell-rh/hypershell-stego/actions/runs/35188311004)
passed at `0175b0bb46494a62e9adfbb06053229dda3ddaf7`. All 11 required tests
passed. The complete workflow took 653.82 seconds. All source and generation
hashes match. The screenshots were reviewed after the archive checks passed.

The independent cleanup check found no remaining browser runtime, fixtures,
or Gateway allocations. The next authorized API test had already started in
its own namespace. Its identity, ownership, resource limits, and lease were
checked without changing its resources. This is cleanup evidence for the
completed browser run. It is not a claim that the entire test area was empty.

The API and CNPG checks for this main update are separate results. The full
enterprise goal remains active.

```json
{
  "run": "https://github.com/jsell-rh/hypershell-stego/actions/runs/35188311004",
  "source": "0175b0bb46494a62e9adfbb06053229dda3ddaf7",
  "compiler": "83592bee5a17de6936cf521b94629e2a225a8d37",
  "seconds": 653.82,
  "source_files_match": 1360,
  "generation_hashes_match": 415,
  "gateway_console_image_matches_qualified_record": true,
  "dashboard_session_and_reload": true,
  "editor_checks": 8,
  "account_cleanup_counts": 3,
  "postgres_signals_complete": true,
  "cleanup": {
    "checked_at": "2026-09-17T06:26:21.585619+00:00",
    "run": 35188311004,
    "source": "0175b0bb46494a62e9adfbb06053229dda3ddaf7",
    "ci": {
      "conclusion": "success",
      "headSha": "0175b0bb46494a62e9adfbb06053229dda3ddaf7",
      "status": "completed"
    },
    "test_job_uid": "591cb2fa-eb86-4ebd-bf51-b5ec5965b886",
    "runtime": [],
    "fixtures": [],
    "allocations": [],
    "ci_jobs": [
      {
        "kind": "Job",
        "name": "gateway-api-a7d98e4e5e8d",
        "namespace": "stego-ci",
        "uid": "3dbfae18-aa58-4d47-ae5b-6f96ee41f146"
      },
      {
        "kind": "Pod",
        "name": "gateway-api-a7d98e4e5e8d-v72pc",
        "namespace": "stego-ci",
        "uid": "22736921-86f0-4c7f-95ab-8a4cfb088325"
      }
    ],
    "lease_uid": "473d5b75-ff67-413c-8ae7-b22f70770c59",
    "lease_holder": "gateway-api-a7d98e4e5e8d",
    "empty": false,
    "completed_run_cleanup_verified": true,
    "scope": "The completed public browser workflow only. Its runtime, fixtures, and allocations are absent. The authorized successor API test owns the current lease and remains unchanged.",
    "successor": {
      "run": 35188311212,
      "source": "0175b0bb46494a62e9adfbb06053229dda3ddaf7",
      "job_uid": "3dbfae18-aa58-4d47-ae5b-6f96ee41f146",
      "pod_uid": "22736921-86f0-4c7f-95ab-8a4cfb088325",
      "name": "gateway-api-a7d98e4e5e8d",
      "checked_at": "2026-09-17T06:27:23.998955+00:00",
      "bounds_and_ownership_verified": true
    }
  },
  "screenshots": {
    "dashboard-create.json.png": "ab64d065ce30c144a06744b6a30240801bb4aa088a5f8cf847bd227b9a498d9b",
    "dashboard-create.json.editor.png": "8cbd0affe747c205a231890aa9e9c9abdec128b6fed7489532ecfb50c27cfa90",
    "dashboard-create.json.editor-selection.png": "976573f6d5c8af864597b9b4142ffb23fc6fb44d604ddcc6c72f34a5924e5fa2"
  },
  "screenshots_viewed": true,
  "startup": {
    "failed_pairs": {
      "hypershell-console": {
        "1d2143f2-c69f-47d6-9b9d-f7371d917979": [],
        "73260a0e-2ff4-4f1b-8fe2-51c43d912e71": [],
        "a1b92832-8553-4008-8908-c37dcbbd21bb": []
      },
      "hypershell-gateway-console": {
        "3876e656-8cdb-456f-bccb-8e5a8c6260d8": [],
        "7dace68d-563a-4f84-8987-dfb0e0640bbc": [],
        "c0f8303f-b563-4e7d-a319-2bbeb6b481d1": []
      }
    },
    "complete_instances": {
      "hypershell-console": [
        "1d2143f2-c69f-47d6-9b9d-f7371d917979",
        "73260a0e-2ff4-4f1b-8fe2-51c43d912e71",
        "a1b92832-8553-4008-8908-c37dcbbd21bb"
      ],
      "hypershell-gateway-console": [
        "3876e656-8cdb-456f-bccb-8e5a8c6260d8",
        "7dace68d-563a-4f84-8987-dfb0e0640bbc",
        "c0f8303f-b563-4e7d-a319-2bbeb6b481d1"
      ]
    },
    "sha256": "d2e69bae6887611b1b012c82b7eecda570301ac6a28b8fce2e664df90e8a5682",
    "same_relay_instance_required_by_test": true,
    "matching_success_pairs": 48
  },
  "scope": "Complete public Gateway workflow with generated browser startup and relay telemetry at the named source. The original recovered CNPG startup cause remains unknown.",
  "required_tests_passed": 11,
  "provisioner_recovery": {
    "account_write_retries": 0,
    "current_unavailable_observations": true,
    "denied_account_requests": 2,
    "denied_requests_created_no_accounts": true,
    "gateways": 2,
    "running_healthy_gateways_after_restart": true,
    "sql_and_credential_identities_preserved": true
  },
  "screenshot_review": "The workspace is active. Invalid JSON disables submission. The editor selection is visible. No Sandbox runtime is proved.",
  "evidence_archive_sha256": "6f1f77e12f1c386a1b14ba3d18ccbc483c61e1bb46c4ccfb2d7ded655fa6af04"
}
```
