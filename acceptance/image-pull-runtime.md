# Private Gateway console images

The development branch pins STEGO compiler and common registry revision
`bd7d9ea85ec73d60be3557bc4df743fb38392f9c`. This revision adds a common Kubernetes
operation to install and rotate owned image pull Secrets. It also adds named
pull Secret references to the generated deployment renderer.

All three services were regenerated with a compiler built from that exact,
clean Git revision. Repeated generation kept state unchanged. The focused
project input manifest and management console deployment checks passed.

The Gateway console CI job also renders the actual two-container dashboard
with a pull Secret reference. It requires every other resource field to remain
equal to the deployment without that reference. This check does not use real
registry credentials.

This is a compiler adoption step. The workload controller does not yet install
the Secret or select it for Gateway console Pods. The root module still uses
its previously verified Gateway console module revision. Update that module
only after the new module build and image evidence pass their checks.

The next live test needs a separate registry identity with permission to pull
the selected test image. It must not copy the CI or controller API token into
Gateway namespaces. The previous browser test stopped before the dashboard
was ready. The full rendered workflow remains unproven.

## Test registry identity

`deploy/ci/gateway-console-pull.json` declares a separate ServiceAccount in
`stego-ci-access`. Its added role grants `get` on only the
`hypershell-gateway-console` image stream layers in `stego-service-ci`. The
`service-check` test driver can request a token for that account. Admission
requires an explicit token lifetime from 600 through 1,800 seconds.

The operator installed the seven declared resources while no browser Job was
present and held the shared test Lease during installation. Ten live access
reviews passed. The pull account could read the selected image layers. It
could not read another test image, push layers, read application Secrets,
create Pods or role bindings, or request another token. The test driver could
request the pull token but could not request a token for the CI account.
A real request for a 1,801-second token was denied by the new admission policy.
The installation record contains no token.

OpenShift also supplies default authenticated-user permissions and image pull
rights inside the account's own namespace. This account is outside the test
image namespace. The added role does not grant general image access there.
See [Red Hat's image pull permission description](https://docs.redhat.com/en/documentation/openshift_container_platform/3.0/html/developer_guide/dev-guide-image-pull-secrets).

These checks prove the declared permission boundary and lifetime rejection.
They do not yet prove that a Gateway Pod can pull its image. CI must use the
new account and the generated Secret operation in the next workflow run.
