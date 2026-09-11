import type { GatewayProbe } from "@openshift-online/hypershell-gateway-management-ui";
import type {
  DomainProbeSink,
  ProbeDeliveryFailure,
} from "@openshift-online/hypershell-domain-probes/fan-out";
import {
  ROOT_CONTEXT,
  SpanKind,
  SpanStatusCode,
  TraceFlags,
  context as otelContext,
  isSpanContextValid,
  trace as otelTrace,
  type Span,
  type Tracer,
} from "@opentelemetry/api";
import { createBrowserTelemetry } from "@stego/browser-telemetry";

/** W3C `traceparent`/`tracestate` header pair for outbound propagation. */
export interface GatewayTraceContext {
  traceparent: string;
  tracestate?: string;
}

/** A gateway trace sink plus the propagation reader that feeds the API client. */
export interface GatewayTraceSink {
  /**
   * Reads the active workflow (or in-flight dependency) span for one
   * correlation identifier and renders its W3C context, or `undefined` when no
   * span is active for that correlation identifier.
   */
  traceParentFor: (correlationId: string) => GatewayTraceContext | undefined;
  sink: DomainProbeSink<GatewayProbe>;
}

/** A gateway trace sink together with its provider lifecycle controls. */
export interface GatewayTracing extends GatewayTraceSink {
  forceFlush(): Promise<void>;
  shutdown(): Promise<void>;
}

export interface GatewayTraceSinkOptions {
  /**
   * Primes the trace id that the next root workflow span adopts, so the
   * workflow span is a true root that owns the app-chosen trace id rather than
   * descending from a synthetic remote parent. Wired to the provider's
   * {@link RootTraceIdGenerator}. When omitted, a workflow keeps its probe
   * trace id only for propagation and the exported root uses a generated id.
   */
  beginTrace?: (traceId: string) => void;
}

export interface GatewayTracingConfig {
  sampleRatio?: number;
}

export interface GatewayTracingOptions {
  /**
   * Records a span delivery failure that surfaces after buffering, when the
   * batch exporter cannot reach the collector. Span export is asynchronous, so
   * a failed batch would otherwise be dropped silently; routing it here makes
   * the loss observable through the domain probe delivery-health accounting.
   */
  reportDeliveryFailure?: (failure: Readonly<ProbeDeliveryFailure>) => void;
}

const sinkId = "gateway-trace";
interface SpanEntry {
  workflow: Span;
  dependency?: Span;
}

/** Terminal outcomes that mark a span failed rather than ok. */
function isFailureOutcome(outcome: GatewayProbe["fields"]["outcome"]): boolean {
  return outcome !== "started" && outcome !== "succeeded";
}

function applyTerminalOutcome(span: Span, probe: GatewayProbe): void {
  const { failureKind, outcome } = probe.fields;
  span.setAttribute("gateway.outcome", outcome);
  if (failureKind !== null) {
    span.setAttribute("gateway.failure_kind", failureKind);
  }
  // The operation identifier is present only on failing terminal probes and is
  // the sole bridge from a failed workflow to its API-side operation record.
  if (probe.context.operationId !== undefined) {
    span.setAttribute("hypershell.operation_id", probe.context.operationId);
  }
  span.setStatus({
    code: isFailureOutcome(outcome) ? SpanStatusCode.ERROR : SpanStatusCode.OK,
  });
}

/**
 * Builds a domain probe sink that projects gateway workflow and dependency
 * probes onto OpenTelemetry spans. A workflow span is a true root that adopts
 * the trace id carried on each probe context (through the primed
 * {@link RootTraceIdGenerator}), so a workflow span joins the same trace the
 * browser propagates to the BFF and API while remaining the origin of that
 * trace. Span names are drawn from a bounded action template so cardinality
 * stays fixed.
 */
export function createGatewayTraceSink(
  tracer: Tracer,
  options: GatewayTraceSinkOptions = {},
): GatewayTraceSink {
  const beginTrace = options.beginTrace ?? ((): void => undefined);
  const spansByCorrelation = new Map<string, SpanEntry>();

  function startWorkflow(probe: GatewayProbe): void {
    const { correlationId, traceId } = probe.context;
    // A workflow with a chosen trace id is the trace root: prime the generator
    // and start it with no parent. Without a chosen id, fall back to the active
    // context so any caller-established parent still nests.
    let parent = otelContext.active();
    if (traceId !== undefined) {
      beginTrace(traceId);
      parent = ROOT_CONTEXT;
    }
    const workflow = tracer.startSpan(
      `gateway.workflow.${probe.fields.action}`,
      {
        attributes: { "gateway.action": probe.fields.action },
        kind: SpanKind.INTERNAL,
      },
      parent,
    );
    spansByCorrelation.set(correlationId, { workflow });
  }

  function startDependency(probe: GatewayProbe): void {
    const entry = spansByCorrelation.get(probe.context.correlationId);
    if (entry === undefined) {
      return;
    }
    const parent = otelTrace.setSpan(otelContext.active(), entry.workflow);
    entry.dependency = tracer.startSpan(
      `gateway.dependency.${probe.fields.action}`,
      {
        attributes: { "gateway.action": probe.fields.action },
        kind: SpanKind.CLIENT,
      },
      parent,
    );
  }

  function completeDependency(probe: GatewayProbe): void {
    const entry = spansByCorrelation.get(probe.context.correlationId);
    if (entry?.dependency === undefined) {
      return;
    }
    applyTerminalOutcome(entry.dependency, probe);
    entry.dependency.end();
    entry.dependency = undefined;
  }

  function completeWorkflow(probe: GatewayProbe): void {
    const entry = spansByCorrelation.get(probe.context.correlationId);
    if (entry === undefined) {
      return;
    }
    // Defend against a dependency span left open by a dropped completion probe.
    if (entry.dependency !== undefined) {
      entry.dependency.end();
    }
    applyTerminalOutcome(entry.workflow, probe);
    entry.workflow.end();
    spansByCorrelation.delete(probe.context.correlationId);
  }

  const sink: DomainProbeSink<GatewayProbe> = {
    id: sinkId,
    publish(probe) {
      switch (probe.name) {
        case "gateway.workflow.started":
          startWorkflow(probe);
          return;
        case "gateway.dependency.attempted":
          startDependency(probe);
          return;
        case "gateway.dependency.completed":
          completeDependency(probe);
          return;
        case "gateway.workflow.completed":
          completeWorkflow(probe);
          return;
      }
    },
  };

  function traceParentFor(
    correlationId: string,
  ): GatewayTraceContext | undefined {
    const entry = spansByCorrelation.get(correlationId);
    const span = entry?.dependency ?? entry?.workflow;
    if (span === undefined) {
      return undefined;
    }
    const spanContext = span.spanContext();
    if (!isSpanContextValid(spanContext)) {
      return undefined;
    }
    const flags =
      (spanContext.traceFlags & TraceFlags.SAMPLED) === 0 ? "00" : "01";
    const traceparent = `00-${spanContext.traceId}-${spanContext.spanId}-${flags}`;
    const tracestate = spanContext.traceState?.serialize();
    return tracestate === undefined || tracestate === ""
      ? { traceparent }
      : { traceparent, tracestate };
  }

  return { sink, traceParentFor };
}

/** Connects the domain probe sink to the generated telemetry runtime. */
export function createGatewayTracing(
  config: GatewayTracingConfig,
  options: GatewayTracingOptions = {},
): GatewayTracing {
  const telemetry = createBrowserTelemetry({
    sampleRatio: config.sampleRatio,
    reportDeliveryFailure: (failure) => {
      options.reportDeliveryFailure?.({
        errorType: failure.reason,
        probeName: `browser.${failure.signal}.export`,
        schemaVersion: 0,
        sinkId,
      });
    },
  });
  const projected = createGatewayTraceSink(telemetry.tracer, {
    beginTrace: telemetry.beginTrace,
  });
  const probes = telemetry.meter.createCounter("gateway.probes");
  const sink: DomainProbeSink<GatewayProbe> = {
    id: sinkId,
    publish(probe) {
      const starts =
        probe.name === "gateway.workflow.started" ||
        probe.name === "gateway.dependency.attempted";
      if (starts) projected.sink.publish(probe);
      const parent = projected.traceParentFor(
        probe.context.correlationId,
      )?.traceparent;
      const context =
        parent === undefined
          ? ROOT_CONTEXT
          : otelTrace.setSpanContext(ROOT_CONTEXT, {
              traceId: parent.slice(3, 35),
              spanId: parent.slice(36, 52),
              traceFlags: parent.endsWith("-01")
                ? TraceFlags.SAMPLED
                : TraceFlags.NONE,
            });
      const attributes = {
        "gateway.action": probe.fields.action,
        "gateway.outcome": probe.fields.outcome,
        "gateway.probe": probe.name,
      };
      telemetry.logger.emit({
        body: probe.name,
        severityNumber: isFailureOutcome(probe.fields.outcome) ? 17 : 9,
        attributes,
        context,
      });
      probes.add(1, attributes, context);
      if (!starts) projected.sink.publish(probe);
    },
  };
  return {
    ...projected,
    sink,
    forceFlush: telemetry.forceFlush,
    shutdown: telemetry.shutdown,
  };
}
