import { render } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { DashboardTelemetry, dashboardRoute } from '../telemetry.stego';

const mockLog = jest.fn();
const mockCount = jest.fn();
const mockEnd = jest.fn();
const mockSpan = jest.fn(() => ({ end: mockEnd, spanContext: () => ({ traceId: 'a'.repeat(32), spanId: 'b'.repeat(16), traceFlags: 1 }) }));
jest.mock('@stego/browser-telemetry', () => ({
  createBrowserTelemetry: () => ({
    tracer: { startSpan: mockSpan },
    logger: { emit: mockLog },
    meter: { createCounter: () => ({ add: mockCount }) },
  }),
}));

test('document signals use fixed routes and contain no resource identifiers', () => {
  const privateName = 'private-resource-value';
  const routes = [
    ['/', '/'], ['/gateway', '/gateway'], ['/global-policy', '/global-policy'],
    ['/settings', '/settings'], ['/workspaces', '/workspaces'],
    [`/workspaces/${privateName}`, '/workspaces/{id}'],
    [`/workspaces/${privateName}/sandboxes/${privateName}`, '/workspaces/{id}/sandboxes/{id}'],
    [`/workspaces/${privateName}/providers/${privateName}`, '/workspaces/{id}/providers/{id}'],
    [`/${privateName}`, 'unmatched'],
  ];
  for (const [path, expected] of routes) {
    jest.clearAllMocks();
    const view = render(<MemoryRouter initialEntries={[path+'?search='+privateName+'#'+privateName]}><DashboardTelemetry /></MemoryRouter>);
    expect(mockSpan).toHaveBeenCalledWith('dashboard.document.rendered', { attributes: { 'http.route': expected } });
    expect(mockLog).toHaveBeenCalledTimes(1);
    expect(mockLog.mock.calls[0][0].body).toBe('dashboard.document.rendered');
    expect(mockCount).toHaveBeenCalledWith(1, { 'http.route': expected }, expect.anything());
    expect(mockEnd).toHaveBeenCalledTimes(1);
    expect(JSON.stringify(mockLog.mock.calls)).not.toContain(privateName);
    view.unmount();
  }
  expect(dashboardRoute('/'+privateName.repeat(1000))).toBe('unmatched');
  expect(dashboardRoute('/settings?search='+privateName)).toBe('unmatched');
});
