import { SDKError } from '@stego/browser-client';
import { apiFetch, setApiBasePath, setSessionExpiredHandler } from '../client';

const mockRequest = jest.fn();
jest.mock('@stego/browser-client', () => {
  const actual = jest.requireActual('@stego/browser-client');
  return { ...actual, createBrowserClient: () => ({ request: mockRequest }) };
});

beforeEach(() => {
  mockRequest.mockReset();
  setApiBasePath('');
  setSessionExpiredHandler(null);
});

test('workspace writes use the generated session client', async () => {
  mockRequest.mockResolvedValue({ status: 201, body: { name: 'workspace' } });
  const result = await apiFetch('/api/v1/workspaces', { method: 'POST', body: JSON.stringify({ name: 'workspace' }) });
  expect(result).toEqual({ name: 'workspace' });
  expect(mockRequest).toHaveBeenCalledWith('POST', '/api/v1/workspaces', { name: 'workspace' }, undefined);
});

test('reads and cancellation use the same transport', async () => {
  mockRequest.mockResolvedValue({ status: 200, body: [] });
  const signal = new AbortController().signal;
  await apiFetch('/api/v1/workspaces', { signal });
  expect(mockRequest).toHaveBeenCalledWith('GET', '/api/v1/workspaces', undefined, { signal });
});

test('session expiry preserves the upstream notification', async () => {
  const expired = jest.fn();
  setSessionExpiredHandler(expired);
  mockRequest.mockRejectedValue(new SDKError('reauth_required', 401));
  await expect(apiFetch('/api/v1/workspaces')).rejects.toMatchObject({ status: 401, message: 'Session expired' });
  expect(expired).toHaveBeenCalledTimes(1);
});

test('caller credentials and invalid JSON do not reach the transport', async () => {
  await expect(apiFetch('/api/v1/workspaces', { headers: { Authorization: 'private' } })).rejects.toMatchObject({ status: 0, message: 'Invalid browser API request' });
  await expect(apiFetch('/api/v1/workspaces', { method: 'POST', body: '{private' })).rejects.toMatchObject({ status: 0, message: 'Invalid browser API request' });
  expect(mockRequest).not.toHaveBeenCalled();
});
