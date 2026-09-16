import { useEffect } from 'react';
import { useLocation } from 'react-router-dom';
import { ROOT_CONTEXT, trace } from '@opentelemetry/api';
import { createBrowserTelemetry } from '@stego/browser-telemetry';

// Fixed route names exclude Gateway, workspace, and Sandbox identifiers.
const routes: readonly [RegExp, string][] = [
  [/^\/$/, '/'],
  [/^\/gateway\/?$/, '/gateway'],
  [/^\/global-policy\/?$/, '/global-policy'],
  [/^\/settings\/?$/, '/settings'],
  [/^\/workspaces\/?$/, '/workspaces'],
  [/^\/workspaces\/[^/]+\/?$/, '/workspaces/{id}'],
  [/^\/workspaces\/[^/]+\/sandboxes\/[^/]+\/?$/, '/workspaces/{id}/sandboxes/{id}'],
  [/^\/workspaces\/[^/]+\/providers\/[^/]+\/?$/, '/workspaces/{id}/providers/{id}'],
];
export const dashboardRoute = (pathname: string): string => {
  if (pathname.length > 4096) return 'unmatched';
  return routes.find(([pattern]) => pattern.test(pathname))?.[1] ?? 'unmatched';
};

// The generated runtime reads the backend's signal settings. It owns exporters,
// session checks, limits, and page-hide flush. This module records domain events.
let runtime: ReturnType<typeof createBrowserTelemetry> | undefined;
const recordDocument = (route: string): void => {
  runtime ??= createBrowserTelemetry();
  const attributes = { 'http.route': route };
  const span = runtime.tracer.startSpan('dashboard.document.rendered', { attributes });
  const context = trace.setSpan(ROOT_CONTEXT, span);
  runtime.logger.emit({
    body: 'dashboard.document.rendered',
    severityNumber: 9,
    severityText: 'INFO',
    attributes,
    context,
  });
  runtime.meter.createCounter('dashboard.document.views').add(1, attributes, context);
  span.end();
};

export const DashboardTelemetry: React.FC = () => {
  const { pathname } = useLocation();
  useEffect(() => { recordDocument(dashboardRoute(pathname)); }, [pathname]);
  return null;
};
