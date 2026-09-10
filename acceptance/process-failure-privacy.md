Hypershell uses STEGO compiler
`62772729cb205747e21d36bd99c7923b12efe3f3` for safe process failure records.
The generated main program reports a fixed stage and failed task names. It does
not print component or driver error text. Its GORM connection discards raw SQL,
query parameters, driver errors, and source paths from GORM logs.

The behavior belongs to the compiler. No Hypershell log filter or database logger
was added. The [shared contract](https://github.com/jsell-rh/stego/blob/62772729cb205747e21d36bd99c7923b12efe3f3/specs/process-failure-privacy.md)
defines the fields, error identity, output deadline, and limits.

The tests first ran against compiler `777d591`:

- Startup with an invalid URL or refused connection exposed private connection
  details. Other startup failures did not produce the required JSON record.
- A database trigger rejected Gateway creation with private error text. The test
  verified rollback, removed the trigger, created the Gateway and owner grant,
  and received its event. It then found the private database error in the logs.

With the new compiler, seven selected application tests passed under race
detection with PostgreSQL required. The package took 52.220 seconds:

| Test | Evidence |
| --- | --- |
| `TestGeneratedStartupFailurePrivacy` | Missing configuration, invalid URL, invalid keyword syntax, and connection refusal each produce one safe JSON record and exit code 1. |
| `TestGatewayDatabaseFailureLogPrivacy` | Failed creation rolls back the Gateway and owner grant. Recovery commits both and delivers the event. Logs and the error response exclude private data. |
| `TestGatewayServiceLogsWithoutCollector` | Local lifecycle logs and event delivery work without a collector. |
| `TestGatewayLogsMetricsAndTracesAcrossRestart` | Request signals, denied access, restart, and collector failure remain covered. |
| `TestGatewayTelemetrySeparatesReplicasAndRestart` | Runtime identity remains separate across replicas and replacement. |
| `TestGatewayWatchSourceFailureStopsGeneratedRuntime` | Source failure closes the watch and listeners. The failure record retains the generated task name and `service.run` stage. |
| `TestGatewayControllerTelemetryAcrossFailureAndRestart` | Controller signals survive provider failure, API restart, and collector loss. |

All 19 internal and contract package results passed, and static checks passed.
Repeat generation preserved all 97 output, state, and dependency hashes.
The [provider evidence](ci-evidence.md) records five real provider workflow runs.
STEGO [CI run 34534558105](https://github.com/jsell-rh/stego/actions/runs/34534558105)
passed for the pinned compiler. Hypershell CI must pass at the new revision.

Early failure records are local and have no telemetry instance identity. The
process has not yet created that runtime. OTLP export for these failures,
complete process lifecycle logging, typed fault codes, database signals, and
safe domain event declarations remain open. This change does not cover database
server logs, panics, or application-owned log calls. Full observability and the
enterprise goal remain open.
