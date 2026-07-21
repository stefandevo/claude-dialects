import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError, DashboardApi } from './api';
import type { DialectInput } from './types';

const input: DialectInput = {
  name: 'demo',
  preset: '',
  model: 'provider/model',
  subagentModel: '',
  opusModel: '',
  sonnetModel: '',
  haikuModel: '',
  effortLevel: 'auto',
  concurrency: 3,
  port: 0,
  bridgePort: 0,
  baseUrl: '',
  authTokenEnv: '',
  effort: false,
  toolSearch: false,
};

function jsonResponse(body: unknown, init: ResponseInit = {}) {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json', ...init.headers },
    ...init,
  });
}

afterEach(() => vi.restoreAllMocks());

describe('DashboardApi', () => {
  it('sends CSRF and a quoted strong ETag on updates', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(jsonResponse({ version: '1.2.3', url: 'http://127.0.0.1/', csrfToken: 'csrf-token' }))
      .mockResolvedValueOnce(jsonResponse({ dialect: { name: 'demo' }, created: false, revision: 'revision-2' }, { headers: { ETag: '"revision-2"' } }));
    const api = new DashboardApi();

    await api.bootstrap();
    const result = await api.updateDialect('demo', input, 'revision-1');

    expect(result.revision).toBe('revision-2');
    const [url, request] = fetchMock.mock.calls[1];
    expect(url).toBe('/api/v1/dialects/demo');
    expect(request?.method).toBe('PUT');
    const headers = new Headers(request?.headers);
    expect(headers.get('X-CC-Dialect-CSRF')).toBe('csrf-token');
    expect(headers.get('If-Match')).toBe('"revision-1"');
    expect(headers.get('Content-Type')).toBe('application/json');
  });

  it('encodes the resource name and sends exact deletion confirmation', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(jsonResponse({ version: '1', url: 'http://127.0.0.1/', csrfToken: 'csrf' }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }));
    const api = new DashboardApi();

    await api.bootstrap();
    await api.deleteLauncher('native_name', 'revision-7');

    const [url, request] = fetchMock.mock.calls[1];
    expect(url).toBe('/api/v1/native-launchers/native_name');
    expect(request?.body).toBe(JSON.stringify({ confirmation: 'native_name' }));
    expect(new Headers(request?.headers).get('If-Match')).toBe('"revision-7"');
  });

  it('exposes revision conflicts without leaking transport details', async () => {
    vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(jsonResponse({ version: '1', url: 'http://127.0.0.1/', csrfToken: 'csrf' }))
      .mockResolvedValueOnce(jsonResponse({ error: { code: 'revision_conflict', message: 'configuration changed; reload and try again' } }, { status: 412 }));
    const api = new DashboardApi();
    await api.bootstrap();

    const promise = api.updateDialect('demo', input, 'stale');
    await expect(promise).rejects.toMatchObject({ status: 412, code: 'revision_conflict', isConflict: true });
  });
});
