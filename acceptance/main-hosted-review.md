# Main hosted checks

The hosted checks below passed at source `0aa8f0d`. The saved source, test,
compiler, and build records passed independent checks. See the
[provider, console, and journal results](main-provider-console-journal-evidence.json).

- All 41 required journal tests passed. Both restart tests passed. The original
  six-account fixture is unchanged. The result includes 33 retry status cases,
  22 application retry cases, and 11 cleanup owner cases.
- All six required provider tests and 15 snapshot cases passed. The unknown
  client test retained 61 closed identities. Its 21 journals and checkpoint
  survived API and provisioner restart. The unrelated client was unchanged.
- The repeated console asset archives are identical. Compiler public records
  match the verified release.
- All 130 archived Gateway console files match committed source. The image
  binary hash matches the built module. The private listener, separate mounts,
  and image pull reference checks passed.
- All 13 dashboard router tests passed. The saved runtime dependency checks
  reported no vulnerabilities. The generated runtime and dependency inputs
  match source, and the asset archive matches the committed archive. The saved
  published image fields match the build fields.

These checks do not replace the live browser, API, or full test results.
The last complete Gateway cleanup observation remains 53.55 seconds. It does
not prove the 30-second target or identify the cause of the delay.

The separate test-only candidate `80b0e00` passed its
[hosted phase observation check](cleanup-phase-hosted-evidence.json). All three
new cases passed. They cover repeated completion reads, a later pending read,
and a complete read without an earlier pending read. The existing adapter and
collection checks passed. This result contains no live phase measurements.

## Full hosted suite

The [full main suite](https://github.com/jsell-rh/hypershell-stego/actions/runs/35479995448)
passed at the same source `0aa8f0d`. Independent comparison retained all 816
baseline cases and verified the added coverage. There are 950 passing core
cases across 334 top-level tests. Both cleanup restart cases, 22 application
retry cases, and 11 cleanup owner cases passed. The browser, web console, and
service image jobs passed. See the [full result](main-full-evidence.json).

The five core exclusions are unchanged. They require separate live Kubernetes,
SQL, or provider fixtures. The live main browser and provider capacity results
have separate records. The API gate is still active. The Kata Sandbox job was
not selected because the live VM test remains deferred. This full hosted result
does not prove the 30-second Gateway target or production capacity.
