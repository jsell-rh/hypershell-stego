import { RootTraceIdGenerator } from "@stego/browser-telemetry";
import type {
  GatewayProbe,
  GatewayProbePublisher,
  GatewayWorkflowRuntime,
} from "@openshift-online/hypershell-gateway-management-ui";
import {
  FanOutDomainProbePublisher,
  type DomainProbeSink,
  type DomainProbeSinkSet,
  type ProbeDeliveryFailure,
  type ProbeDeliveryHealthSnapshot,
} from "@openshift-online/hypershell-domain-probes/fan-out";

const recentProbeLimit = 100;
const recentFailureLimit = 20;
const traceIds = new RootTraceIdGenerator();

interface PerformanceProbeTarget {
  clearMarks(name?: string): void;
  mark(name: string, options?: PerformanceMarkOptions): PerformanceMark;
}

export interface GatewayObservability {
  deliveryHealth(): Readonly<ProbeDeliveryHealthSnapshot>;
  probes: GatewayProbePublisher;
  recentDeliveryFailures(): readonly Readonly<ProbeDeliveryFailure>[];
  recentProbes(): readonly GatewayProbe[];
  // Records a span delivery failure that surfaces asynchronously, outside the
  // synchronous probe fan-out, so an export the browser could not complete is
  // counted in delivery health instead of being lost.
  reportDeliveryFailure(failure: Readonly<ProbeDeliveryFailure>): void;
  runtime: GatewayWorkflowRuntime;
}

export interface GatewayObservabilityOptions {
  additionalSinks?: readonly DomainProbeSink<GatewayProbe>[];
  createCorrelationId?: () => string;
  createTraceId?: () => string;
  now?: () => string;
  performanceTarget?: PerformanceProbeTarget;
}

function appendBounded<T>(target: T[], value: T, limit: number) {
  target.push(value);
  if (target.length > limit) {
    target.splice(0, target.length - limit);
  }
}

export function createGatewayObservability(
  options: GatewayObservabilityOptions = {},
): GatewayObservability {
  const failures: Readonly<ProbeDeliveryFailure>[] = [];
  const recent: GatewayProbe[] = [];
  const performanceTarget = options.performanceTarget ?? globalThis.performance;
  const recentSink: DomainProbeSink<GatewayProbe> = {
    id: "gateway-product-health",
    publish(value) {
      appendBounded(recent, value, recentProbeLimit);
    },
  };
  const performanceSink: DomainProbeSink<GatewayProbe> = {
    id: "gateway-performance",
    publish(value) {
      performanceTarget.clearMarks(value.name);
      performanceTarget.mark(value.name, { detail: value });
    },
  };
  const additionalSinks = options.additionalSinks ?? [];
  const [firstAdditionalSink, ...remainingAdditionalSinks] = additionalSinks;
  const sinks: DomainProbeSinkSet<GatewayProbe> =
    firstAdditionalSink === undefined
      ? [recentSink, performanceSink]
      : [
          recentSink,
          firstAdditionalSink,
          ...remainingAdditionalSinks,
          performanceSink,
        ];
  const publisher = new FanOutDomainProbePublisher<GatewayProbe>({
    failureReporter: {
      report(failure) {
        appendBounded(failures, failure, recentFailureLimit);
      },
    },
    sinks,
  });

  return {
    deliveryHealth: () => publisher.healthSnapshot(),
    probes: publisher,
    recentDeliveryFailures: () => Object.freeze([...failures]),
    recentProbes: () => Object.freeze([...recent]),
    reportDeliveryFailure: (failure) => {
      publisher.reportDeliveryFailure(failure);
    },
    runtime: {
      createCorrelationId:
        options.createCorrelationId ?? (() => globalThis.crypto.randomUUID()),
      createTraceId:
        options.createTraceId ?? (() => traceIds.generateTraceId()),
      now: options.now ?? (() => new Date().toISOString()),
    },
  };
}
