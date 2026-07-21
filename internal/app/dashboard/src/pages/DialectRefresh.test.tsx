import { act, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { DialectView } from '../types';

const getDialect = vi.fn();
let activeRefresh: (() => Promise<void> | void) | undefined;
const registerRefreshHandler = vi.fn((handler: () => Promise<void> | void) => {
  activeRefresh = handler;
  return () => { if (activeRefresh === handler) activeRefresh = undefined; };
});

const dashboardContext = {
  api: { getDialect, dialectAction: vi.fn(), deleteDialect: vi.fn() },
  presets: [],
  dialectRevision: 'list-revision',
  refreshAfterMutation: vi.fn(async () => undefined),
  registerRefreshHandler,
  reportError: vi.fn(),
  notify: vi.fn(),
};

vi.mock('../AppContext', () => ({
  useDashboard: () => dashboardContext,
}));

import { DialectDetailPage } from './DialectDetailPage';
import { DialectFormPage } from './DialectFormPage';

function dialect(model: string): DialectView {
  return {
    name: 'demo', preset: 'custom', provider: 'custom', model, effort: false,
    concurrency: 3, toolSearch: false, port: 43170,
    status: { state: 'stopped', proxy: { state: 'stopped', port: 43170 } },
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  activeRefresh = undefined;
});

describe('active dialect route refresh', () => {
  it('refreshes detail data through the global refresh callback', async () => {
    getDialect.mockResolvedValueOnce({ data: dialect('old-model'), revision: 'revision-1' });
    render(<MemoryRouter initialEntries={['/dialects/demo']}><Routes><Route path="/dialects/:name" element={<DialectDetailPage />} /></Routes></MemoryRouter>);
    await screen.findAllByText('old-model', { exact: false });
    expect(activeRefresh).toBeTypeOf('function');

    getDialect.mockResolvedValueOnce({ data: dialect('new-model'), revision: 'revision-2' });
    await act(async () => { await activeRefresh?.(); });

    await waitFor(() => expect(screen.getAllByText('new-model', { exact: false }).length).toBeGreaterThan(0));
  });

  it('refreshes edit values and revision through the global refresh callback', async () => {
    getDialect.mockResolvedValueOnce({ data: dialect('old-model'), revision: 'revision-1' });
    render(<MemoryRouter initialEntries={['/dialects/demo/edit']}><Routes><Route path="/dialects/:name/edit" element={<DialectFormPage />} /></Routes></MemoryRouter>);
    await waitFor(() => expect(screen.getByLabelText('Primary model')).toHaveValue('old-model'));
    expect(screen.getByText('Leave empty to preserve the current proxy port.')).toBeInTheDocument();
    expect(screen.getByText('Maximum concurrent tool uses.')).toBeInTheDocument();
    expect(activeRefresh).toBeTypeOf('function');

    getDialect.mockResolvedValueOnce({ data: dialect('new-model'), revision: 'revision-2' });
    await act(async () => { await activeRefresh?.(); });

    await waitFor(() => expect(screen.getByLabelText('Primary model')).toHaveValue('new-model'));
  });
});
