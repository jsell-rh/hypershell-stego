# CNPG Gateway workflow during provider adoption

The CNPG job in [run 35035649199](https://github.com/jsell-rh/hypershell-stego/actions/runs/35035649199)
passed at application revision `fff749b522afd8c57d313fa4784886c0deacbd8d` with
compiler pin `cae56de17fa6e8298e1de08a3523ba23c2689f4c`.
This revision uses common Keycloak client reads and one-time credential proof.
It precedes the common Gateway grant and service-account role changes.

All ten top-level deployment checks passed, with no failed or skipped test.
`TestGeneratedKubernetesBrowserGatewayWorkflow` took 483.67 seconds. The process
returned zero. Generated file hashes were equal before the test, after repeated
generation, and after the test.

The recorded CNPG recovery changed the primary from `gateway-database-1` to
`gateway-database-2`. Both instances were ready after 65.40 seconds. The cluster
identity and specification, installation data, Gateway credentials and keys,
SQL object IDs, and provider data remained unchanged.

Cleanup records confirm that test runtime resources, volumes, private fixture
data, and allocated namespaces were absent. The operator installation was
retained. A later direct read also found no Job or Pod in `stego-service-ci`.
The source, runtime output, browser artifacts, regeneration hashes, recovery
record, and cleanup records are retained under:

`/home/jsell/.local/state/stego/runs/keycloak-service-roles-20260915/baseline-cnpg`

This is evidence for the complete earlier CNPG application workflow. It does
not qualify later role changes, the new protected resource-state mechanism,
RDS operation, production capacity, or backup restoration. The separate core
acceptance job was still running when this record was written.
