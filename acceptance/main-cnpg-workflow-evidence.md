# Main CNPG Gateway workflow evidence

[Run 35188311598](https://github.com/jsell-rh/hypershell-stego/actions/runs/35188311598)
passed at `0175b0bb46494a62e9adfbb06053229dda3ddaf7`. All 11 required tests
passed. The complete browser workflow took 749.98 seconds. All 1,360 source
hashes and 415 generated-file hashes matched the saved records and archives.
Six browser runtime instances supplied 48 matching startup log/span pairs,
complete metrics, and no failed startup pairs. The three screenshots were
reviewed after artifact verification.

The workflow proved Gateway creation, grants, REST and gRPC access, event
delivery, process and database replacement, namespace and Deployment recovery,
SQL isolation and fault repair, encrypted credentials, real automation accounts,
durable deletion, session-key rotation, renewal, and logout. Provisioner outage
checks denied both account requests without creating rows. Recovery kept SQL
and credential identities and used no account write retries. The final record
matches an independent live capture.

Independent cleanup passed at `2026-09-17T07:04:39Z`. The test runtime, fixtures,
allocations, both observed volumes, and lease holder were absent. No later live
test was active during this check.

## Scheduling limit

The application test Pod initially could not fit on a node because free CPU and
memory were split across nodes. One exact, owned secondary database Pod was
replaced. The same application Pod then ran, and both database instances became
ready. Primary, claim, and volume identities were verified unchanged before the
application test's later deliberate database replacement. Resource limits and
unrelated workloads were unchanged.

This run does not prove fixture placement without intervention. The earlier
`cf232b0` workflow passed without that intervention. The source-specific records
retain this difference. Live Kata isolation and production capacity remain
separate requirements. The full enterprise goal remains active.

## Verified record

```json
{
  "run": "https://github.com/jsell-rh/hypershell-stego/actions/runs/35188311598",
  "source": "0175b0bb46494a62e9adfbb06053229dda3ddaf7",
  "compiler": "83592bee5a17de6936cf521b94629e2a225a8d37",
  "seconds": 749.98,
  "source_files_match": 1360,
  "generation_hashes_match": 415,
  "gateway_console_image_matches_qualified_record": true,
  "dashboard_session_and_reload": true,
  "editor_checks": 8,
  "account_cleanup_counts": 3,
  "postgres_signals_complete": true,
  "cleanup": {
    "checked_at": "2026-09-17T07:04:39.708177+00:00",
    "cnpg_run": 35188311598,
    "api_jobs": [],
    "api_fixtures": [],
    "service_runtime": [],
    "service_fixtures": [],
    "allocations": [],
    "cnpg_operator_runtime": [],
    "cnpg_runtime": [],
    "lease_holder": "",
    "retained_test_volumes": [],
    "test_volumes_checked": [
      "pvc-0f55fd79-6e04-41b6-8b47-541dd585bd70",
      "pvc-905803cc-64cf-44c1-a51f-346cbb01ba46"
    ]
  },
  "screenshots": {
    "dashboard-create.json.png": "8f46d7e624b592c757f4a0b1af37eade3d82bab58d3b3c0d6a17b6139efdcfa7",
    "dashboard-create.json.editor.png": "33068944f74c61a5f6d0988c496ea2d7c02baafa59ab434393ef72ba8a9ea55f",
    "dashboard-create.json.editor-selection.png": "af99e73d7a52d35a4689716b39745189c0cb6c4e3929f072161e1fd119dc3412"
  },
  "screenshots_viewed": true,
  "startup": {
    "failed_pairs": {
      "hypershell-console": {
        "170a75a1-05bd-47bc-979e-65cf7a31c3cd": [],
        "44c7775c-eab8-454a-a402-1a0ea3754492": [],
        "fd54ef44-c36f-4537-95ca-c9e84c32c047": []
      },
      "hypershell-gateway-console": {
        "6e7bae3c-bb28-4131-b9ee-0de867ae5f71": [],
        "8cea3f43-10a1-414b-99bc-a9880228eafb": [],
        "a884a902-5d0d-4797-8a04-31714997cf10": []
      }
    },
    "complete_instances": {
      "hypershell-console": [
        "170a75a1-05bd-47bc-979e-65cf7a31c3cd",
        "44c7775c-eab8-454a-a402-1a0ea3754492",
        "fd54ef44-c36f-4537-95ca-c9e84c32c047"
      ],
      "hypershell-gateway-console": [
        "6e7bae3c-bb28-4131-b9ee-0de867ae5f71",
        "8cea3f43-10a1-414b-99bc-a9880228eafb",
        "a884a902-5d0d-4797-8a04-31714997cf10"
      ]
    },
    "sha256": "f5fae20d3da7d235d0a147158db198ba2b9ae7daedc2869c662e59b327bae7e7",
    "same_relay_instance_required_by_test": true,
    "matching_success_pairs": 48
  },
  "scope": "Complete CNPG Gateway workflow with generated browser startup and relay telemetry at the named source. The original recovered CNPG startup cause remains unknown.",
  "provisioner_recovery": {
    "account_write_retries": 0,
    "current_unavailable_observations": true,
    "denied_account_requests": 2,
    "denied_requests_created_no_accounts": true,
    "gateways": 2,
    "running_healthy_gateways_after_restart": true,
    "sql_and_credential_identities_preserved": true
  },
  "provisioner_recovery_matches_live_capture": true,
  "evidence_archive_sha256": "d002eb95bdaff4aac53b32a221b5e16c1ac3fefe8e932e683587e51afcd7122f",
  "scheduling_without_intervention": false,
  "required_tests_passed": 11,
  "scheduling_intervention": {
    "checked_at": "2026-09-17T06:42:21.619848+00:00",
    "run": 35188311598,
    "cluster_uid": "8ef04d50-9c8d-4bbc-a5b3-d3067cdb1293",
    "primary": "gateway-database-1",
    "replica_uid": "f2d3ff22-3c5c-47b1-aaed-5cae53f0f6d9",
    "replica_node": "ip-10-0-1-214.ec2.internal",
    "application_pod_uid": "a13680c3-7f28-4173-9495-eb046b12ae50",
    "reason": "The secondary reserves 100 millicores on the only node with enough memory for the application test Pod. Reschedule this owned secondary to let the existing test Pod run.",
    "resource_limits_changed": false,
    "unrelated_workloads_changed": false,
    "delete_accepted": true,
    "verified_at": "2026-09-17T06:43:26.167452+00:00",
    "same_application_pod_running": true,
    "primary_preserved": true,
    "claim_and_volume_identities_preserved": true,
    "replacement_replica_uid": "eb586f87-89b7-40f3-97fe-8d4bc599a58c",
    "replacement_replica_node": "ip-10-0-1-187.ec2.internal",
    "ready_instances": 2
  },
  "not_proved": [
    "Cause of the earlier recovered browser startup failure",
    "Live Kata isolation",
    "Production capacity and remaining enterprise requirements"
  ],
  "cnpg_restart": {
    "cluster_spec_unchanged": true,
    "cluster_uid": "8ef04d50-9c8d-4bbc-a5b3-d3067cdb1293",
    "gateway_credentials_and_keys_unchanged": true,
    "installation_data_unchanged": true,
    "namespace": "stego-cnpg-database-ci",
    "namespace_uid": "d57b0776-5a99-45d6-a1cc-7b031a555359",
    "new_primary": "gateway-database-2",
    "new_primary_pod_uid": "eb586f87-89b7-40f3-97fe-8d4bc599a58c",
    "old_pod_uid": "9a6cd6ff-3e17-4c99-aa0a-4e1e6cb75b35",
    "old_primary": "gateway-database-1",
    "primary_changed": true,
    "provider_data_unchanged": true,
    "ready_instances": 2,
    "seconds": 73.435049891,
    "sql_object_ids_unchanged": true
  },
  "cnpg_cleanup": {
    "runtime_absent": true,
    "volumes_absent": true,
    "private_fixture_absent": true,
    "installation_retained": true
  },
  "cnpg_admission": [
    {
      "name": "operator-job-accepted",
      "policy": "stego-cnpg-operator-ci.bounded-jobs",
      "allowed": true
    },
    {
      "name": "operator-job-missing-deadline",
      "policy": "stego-cnpg-operator-ci.bounded-jobs",
      "allowed": false
    },
    {
      "name": "operator-job-suspended",
      "policy": "stego-cnpg-operator-ci.bounded-jobs",
      "allowed": false
    },
    {
      "name": "operator-job-delayed-cleanup",
      "policy": "stego-cnpg-operator-ci.bounded-jobs",
      "allowed": false
    },
    {
      "name": "database-job-accepted",
      "policy": "stego-cnpg-database-ci.bounded-jobs",
      "allowed": true
    },
    {
      "name": "database-job-missing-deadline",
      "policy": "stego-cnpg-database-ci.bounded-jobs",
      "allowed": false
    },
    {
      "name": "database-job-suspended",
      "policy": "stego-cnpg-database-ci.bounded-jobs",
      "allowed": false
    },
    {
      "name": "database-job-delayed-cleanup",
      "policy": "stego-cnpg-database-ci.bounded-jobs",
      "allowed": false
    },
    {
      "name": "database-lifetime-command",
      "policy": "stego-cnpg-database-ci.bounded-jobs",
      "allowed": false
    },
    {
      "name": "database-lifetime-environment",
      "policy": "stego-cnpg-database-ci.bounded-jobs",
      "allowed": false
    },
    {
      "name": "database-lifetime-probe-command",
      "policy": "stego-cnpg-database-ci.bounded-jobs",
      "allowed": false
    },
    {
      "name": "database-lifetime-network-label",
      "policy": "stego-cnpg-database-ci.bounded-jobs",
      "allowed": false
    },
    {
      "name": "operator-command",
      "policy": "stego-cnpg-operator-ci.bounded-jobs",
      "allowed": false
    },
    {
      "name": "operator-scope",
      "policy": "stego-cnpg-operator-ci.bounded-jobs",
      "allowed": false
    },
    {
      "name": "operator-environment-source",
      "policy": "stego-cnpg-operator-ci.bounded-jobs",
      "allowed": false
    },
    {
      "name": "operator-lifecycle",
      "policy": "stego-cnpg-operator-ci.bounded-jobs",
      "allowed": false
    },
    {
      "name": "operator-probe-command",
      "policy": "stego-cnpg-operator-ci.bounded-jobs",
      "allowed": false
    },
    {
      "name": "operator-executable-mount",
      "policy": "stego-cnpg-operator-ci.bounded-jobs",
      "allowed": false
    },
    {
      "name": "cluster-accepted",
      "policy": "stego-cnpg-database-ci.bounded-cluster",
      "allowed": true
    },
    {
      "name": "cluster-instance-count",
      "policy": "stego-cnpg-database-ci.bounded-cluster",
      "allowed": false
    },
    {
      "name": "cluster-storage-size",
      "policy": "stego-cnpg-database-ci.bounded-cluster",
      "allowed": false
    },
    {
      "name": "cluster-missing-owner",
      "policy": "stego-cnpg-database-ci.bounded-cluster",
      "allowed": false
    }
  ],
  "cnpg_network_policy": {
    "uid": "c9f7bc1f-c6cd-4436-a335-de73bddc0055",
    "resource_version": "7052686",
    "spec": {
      "egress": [
        {
          "ports": [
            {
              "port": 5432,
              "protocol": "TCP"
            },
            {
              "port": 8000,
              "protocol": "TCP"
            }
          ],
          "to": [
            {
              "namespaceSelector": {
                "matchLabels": {
                  "kubernetes.io/metadata.name": "stego-cnpg-database-ci"
                }
              },
              "podSelector": {
                "matchLabels": {
                  "cnpg.io/cluster": "gateway-database"
                }
              }
            }
          ]
        },
        {
          "ports": [
            {
              "port": 5353,
              "protocol": "UDP"
            },
            {
              "port": 5353,
              "protocol": "TCP"
            }
          ],
          "to": [
            {
              "namespaceSelector": {
                "matchLabels": {
                  "kubernetes.io/metadata.name": "openshift-dns"
                }
              }
            }
          ]
        },
        {
          "ports": [
            {
              "port": 6443,
              "protocol": "TCP"
            }
          ],
          "to": [
            {
              "ipBlock": {
                "cidr": "172.20.0.1/32"
              }
            }
          ]
        },
        {
          "ports": [
            {
              "port": 443,
              "protocol": "TCP"
            }
          ],
          "to": [
            {
              "ipBlock": {
                "cidr": "172.30.0.1/32"
              }
            }
          ]
        }
      ],
      "ingress": [
        {
          "from": [
            {
              "namespaceSelector": {
                "matchLabels": {
                  "kubernetes.io/metadata.name": "stego-cnpg-database-ci"
                }
              },
              "podSelector": {
                "matchLabels": {
                  "cnpg.io/cluster": "gateway-database"
                }
              }
            }
          ],
          "ports": [
            {
              "port": 5432,
              "protocol": "TCP"
            },
            {
              "port": 8000,
              "protocol": "TCP"
            }
          ]
        },
        {
          "from": [
            {
              "namespaceSelector": {
                "matchLabels": {
                  "kubernetes.io/metadata.name": "stego-cnpg-operator-ci"
                }
              },
              "podSelector": {
                "matchLabels": {
                  "app.kubernetes.io/name": "cloudnative-pg"
                }
              }
            }
          ],
          "ports": [
            {
              "port": 8000,
              "protocol": "TCP"
            }
          ]
        },
        {
          "from": [
            {
              "namespaceSelector": {
                "matchLabels": {
                  "kubernetes.io/metadata.name": "stego-service-ci"
                }
              },
              "podSelector": {
                "matchLabels": {
                  "app": "stego-fixture"
                }
              }
            },
            {
              "namespaceSelector": {
                "matchLabels": {
                  "kubernetes.io/metadata.name": "stego-service-ci"
                }
              },
              "podSelector": {
                "matchLabels": {
                  "app.kubernetes.io/name": "hypershell-gateway-workload"
                }
              }
            },
            {
              "namespaceSelector": {
                "matchLabels": {
                  "stego.dev/allocation-profile": "gateway",
                  "stego.dev/allocator": "5305aa89c5f40a73316e60bd033ec8ab"
                }
              },
              "podSelector": {
                "matchLabels": {
                  "app.kubernetes.io/name": "hypershell-gateway-console"
                }
              }
            },
            {
              "namespaceSelector": {
                "matchLabels": {
                  "stego.dev/allocation-profile": "gateway",
                  "stego.dev/allocator": "5305aa89c5f40a73316e60bd033ec8ab"
                }
              },
              "podSelector": {
                "matchExpressions": [
                  {
                    "key": "stego.test/network-probe",
                    "operator": "Exists"
                  }
                ]
              }
            },
            {
              "namespaceSelector": {
                "matchLabels": {
                  "stego.dev/allocation-profile": "gateway",
                  "stego.dev/allocator": "5305aa89c5f40a73316e60bd033ec8ab"
                }
              },
              "podSelector": {
                "matchExpressions": [
                  {
                    "key": "hypershell.redhat.io/gateway-id",
                    "operator": "Exists"
                  }
                ]
              }
            }
          ],
          "ports": [
            {
              "port": 5432,
              "protocol": "TCP"
            }
          ]
        }
      ],
      "podSelector": {
        "matchLabels": {
          "cnpg.io/cluster": "gateway-database"
        }
      },
      "policyTypes": [
        "Ingress",
        "Egress"
      ]
    },
    "spec_sha256": "4878e83cf4d42a4f3097d86f9747b97b4ffbd76e68160bbcf3370ebf41829228"
  },
  "namespace_recovery_checks": {
    "database_and_role_oids_unchanged": true,
    "other_gateway_sql_unchanged": true,
    "provider_data_unchanged": true,
    "source_data_unchanged": true,
    "sql_credentials_unchanged": true,
    "ungranted_rpc_denied": true,
    "viewer_lists_filtered": true,
    "viewer_membership_recovered": true,
    "viewer_writes_denied": true,
    "workspace_and_gateway_revocation_verified": true
  },
  "sql_cleanup_denial_checks": {
    "cleanup_pending": true,
    "other_gateway_ready": true,
    "source_data_unchanged": true,
    "sql_object_ids_unchanged": true,
    "sqlstate": "42501"
  },
  "journal_contains_only_resource_identity": true,
  "screenshot_review": "The workspace is active. Invalid JSON disables submission. The editor selection is visible. No Sandbox runtime is proved."
}
```
