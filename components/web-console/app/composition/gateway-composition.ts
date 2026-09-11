import { createGatewayOperations } from "@openshift-online/hypershell-gateway-management-ui";
import type { ProbeDeliveryFailure } from "@openshift-online/hypershell-domain-probes/fan-out";

import { createApiClient } from "../adapters/api/api.client";
import { createGatewayControlPlaneAdapter } from "../adapters/api/gateway-operations";
import { createGatewayObservability } from "../adapters/observability/gateway-observability";
import { createGatewayTracing } from "../adapters/observability/gateway-trace-sink";

// The trace sink is created before the observability publisher because the
// publisher takes the sink as one of its fan-out targets, yet a failed span
// export must report back into that publisher's delivery health. A late-bound
// reporter breaks the cycle: export failures raised before the publisher exists
// are dropped, which is correct because no span can be exported until the sink
// is wired into the publisher and receiving probes.
let reportDeliveryFailure: (
  failure: Readonly<ProbeDeliveryFailure>,
) => void = () => undefined;

const tracing = createGatewayTracing(
  {},
  {
    reportDeliveryFailure: (failure) => {
      reportDeliveryFailure(failure);
    },
  },
);

const gatewayObservability = createGatewayObservability({
  additionalSinks: [tracing.sink],
});

reportDeliveryFailure = (failure) => {
  gatewayObservability.reportDeliveryFailure(failure);
};

const gatewayControlPlane = createGatewayControlPlaneAdapter(
  () => createApiClient(),
  () => {
    createApiClient().login();
  },
  (correlationId) => tracing.traceParentFor(correlationId)?.traceparent,
);

export const gatewayOperations = createGatewayOperations({
  controlPlane: gatewayControlPlane,
  probes: gatewayObservability.probes,
  runtime: gatewayObservability.runtime,
});
