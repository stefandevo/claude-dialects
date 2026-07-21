import { act, render, screen, waitFor } from '@testing-library/react';
import { useEffect } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { DashboardProvider, useDashboard } from './AppContext';
import { dashboardApi } from './api';

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
}

let triggerRefresh: () => Promise<void>;
let triggerRefreshAfterMutation: () => Promise<void>;
const localRefresh = vi.fn(async () => undefined);

function Probe() {
  const { loading, refreshing, refresh, refreshAfterMutation, registerRefreshHandler } = useDashboard();
  triggerRefresh = refresh;
  triggerRefreshAfterMutation = refreshAfterMutation;
  useEffect(() => registerRefreshHandler(localRefresh), [registerRefreshHandler]);
  return <div>{loading ? 'loading' : 'ready'}:{refreshing ? 'refreshing' : 'idle'}</div>;
}

function mockSuccessfulData() {
  vi.spyOn(dashboardApi, 'bootstrap').mockResolvedValue({ version: '1', url: 'http://127.0.0.1/', csrfToken: 'csrf' });
  vi.spyOn(dashboardApi, 'listDialects').mockResolvedValue({ data: { dialects: [], revision: 'dialect-1' }, revision: 'dialect-1' });
  vi.spyOn(dashboardApi, 'listLaunchers').mockResolvedValue({ data: { launchers: [], revision: 'launcher-1' }, revision: 'launcher-1' });
  vi.spyOn(dashboardApi, 'cursorStatus').mockResolvedValue({ runtimeInstalled: false, runtimeCurrent: false, requiredVersion: '1.0.0', apiKeySet: false });
  vi.spyOn(dashboardApi, 'presets').mockResolvedValue([]);
}

afterEach(() => {
  vi.restoreAllMocks();
  localRefresh.mockClear();
});

describe('DashboardProvider refresh', () => {
  it('re-runs bootstrap, refreshes active-page handlers, and deduplicates overlapping refreshes', async () => {
    mockSuccessfulData();
    render(<DashboardProvider><Probe /></DashboardProvider>);
    await screen.findByText('ready:idle');

    vi.mocked(dashboardApi.bootstrap).mockClear();
    vi.mocked(dashboardApi.listDialects).mockClear();
    vi.mocked(dashboardApi.listLaunchers).mockClear();
    vi.mocked(dashboardApi.cursorStatus).mockClear();
    vi.mocked(dashboardApi.presets).mockClear();
    localRefresh.mockClear();

    const nextBootstrap = deferred<{ version: string; url: string; csrfToken: string }>();
    vi.mocked(dashboardApi.bootstrap).mockReturnValueOnce(nextBootstrap.promise);
    let first!: Promise<void>;
    let second!: Promise<void>;
    act(() => {
      first = triggerRefresh();
      second = triggerRefresh();
    });

    expect(first).toBe(second);
    expect(screen.getByText('ready:refreshing')).toBeInTheDocument();
    expect(dashboardApi.bootstrap).toHaveBeenCalledTimes(1);

    nextBootstrap.resolve({ version: '2', url: 'http://127.0.0.1/', csrfToken: 'fresh-csrf' });
    await act(async () => { await Promise.all([first, second]); });

    await waitFor(() => expect(screen.getByText('ready:idle')).toBeInTheDocument());
    expect(dashboardApi.listDialects).toHaveBeenCalledTimes(1);
    expect(dashboardApi.listLaunchers).toHaveBeenCalledTimes(1);
    expect(dashboardApi.cursorStatus).toHaveBeenCalledTimes(1);
    expect(dashboardApi.presets).toHaveBeenCalledTimes(1);
    expect(localRefresh).toHaveBeenCalledTimes(1);
  });

  it('can recover from an initial bootstrap failure through the same refresh path', async () => {
    mockSuccessfulData();
    vi.mocked(dashboardApi.bootstrap)
      .mockRejectedValueOnce(new Error('server restarted'))
      .mockResolvedValueOnce({ version: '2', url: 'http://127.0.0.1/', csrfToken: 'fresh-csrf' });
    render(<DashboardProvider><Probe /></DashboardProvider>);
    await screen.findByText('ready:idle');

    await act(async () => { await triggerRefresh(); });

    expect(dashboardApi.bootstrap).toHaveBeenCalledTimes(2);
    expect(dashboardApi.listDialects).toHaveBeenCalledTimes(1);
  });

  it('turns a post-mutation reload failure into a secondary warning', async () => {
    mockSuccessfulData();
    render(<DashboardProvider><Probe /></DashboardProvider>);
    await screen.findByText('ready:idle');
    vi.mocked(dashboardApi.bootstrap).mockRejectedValueOnce(new Error('reload failed'));

    await act(async () => { await triggerRefreshAfterMutation(); });

    expect(screen.getByText(/The change succeeded, but the latest dashboard data could not be reloaded/)).toBeInTheDocument();
  });
});
