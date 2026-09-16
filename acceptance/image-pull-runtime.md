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
