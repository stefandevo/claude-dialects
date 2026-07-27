import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { DialectView } from '../types';

const getDialect = vi.fn();
const updateDialect = vi.fn();

const dashboardContext = {
  api: { getDialect, updateDialect, createDialect: vi.fn(), dialectAction: vi.fn(), deleteDialect: vi.fn() },
  presets: [] as DialectView[],
  dialectRevision: 'list-revision',
  refresh: vi.fn(async () => undefined),
  refreshAfterMutation: vi.fn(async () => undefined),
  registerRefreshHandler: vi.fn(() => () => undefined),
  reportError: vi.fn(),
  notify: vi.fn(),
};

vi.mock('../AppContext', () => ({
  useDashboard: () => dashboardContext,
}));

import { DialectDetailPage } from './DialectDetailPage';
import { DialectFormPage } from './DialectFormPage';

function dialect(contextWindow?: number): DialectView {
  return {
    name: 'demo', preset: 'custom', provider: 'custom', model: 'vendor-model', effort: false,
    concurrency: 3, toolSearch: false, contextWindow, port: 43170,
    status: { state: 'stopped', proxy: { state: 'stopped', port: 43170 } },
  };
}

function renderForm() {
  return render(
    <MemoryRouter initialEntries={['/dialects/demo/edit']}>
      <Routes><Route path="/dialects/:name/edit" element={<DialectFormPage />} /></Routes>
    </MemoryRouter>,
  );
}

beforeEach(() => vi.clearAllMocks());

describe('dialect context window', () => {
  it('loads the stored capacity into the form and sends it back on save', async () => {
    getDialect.mockResolvedValueOnce({ data: dialect(262144), revision: 'revision-1' });
    updateDialect.mockResolvedValueOnce({ data: { dialect: dialect(262144), created: false, revision: 'revision-2' }, revision: 'revision-2' });
    renderForm();

    await waitFor(() => expect(screen.getByLabelText('Context window')).toHaveValue(262144));

    fireEvent.click(screen.getByRole('button', { name: /save changes/i }));

    await waitFor(() => expect(updateDialect).toHaveBeenCalled());
    expect(updateDialect.mock.calls[0][1]).toMatchObject({ contextWindow: 262144 });
  });

  it('rejects a capacity above the supported bound before contacting the API', async () => {
    getDialect.mockResolvedValueOnce({ data: dialect(262144), revision: 'revision-1' });
    renderForm();
    await waitFor(() => expect(screen.getByLabelText('Context window')).toHaveValue(262144));

    fireEvent.change(screen.getByLabelText('Context window'), { target: { value: '20000001' } });
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }));

    await screen.findByRole('alert');
    expect(screen.getByRole('alert')).toHaveTextContent('Context window must be between 1 and 20,000,000 tokens.');
    expect(updateDialect).not.toHaveBeenCalled();
  });

  // An empty field means "leave it to the preset or the stored value", which the
  // Go side represents as zero — not a real capacity of zero tokens.
  it('sends zero for an empty capacity field', async () => {
    getDialect.mockResolvedValueOnce({ data: dialect(262144), revision: 'revision-1' });
    updateDialect.mockResolvedValueOnce({ data: { dialect: dialect(262144), created: false, revision: 'revision-2' }, revision: 'revision-2' });
    renderForm();
    await waitFor(() => expect(screen.getByLabelText('Context window')).toHaveValue(262144));

    fireEvent.change(screen.getByLabelText('Context window'), { target: { value: '' } });
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }));

    await waitFor(() => expect(updateDialect).toHaveBeenCalled());
    expect(updateDialect.mock.calls[0][1]).toMatchObject({ contextWindow: 0 });
  });

  it('flags an uncalibrated dialect on the detail page', async () => {
    getDialect.mockResolvedValueOnce({ data: dialect(undefined), revision: 'revision-1' });
    render(
      <MemoryRouter initialEntries={['/dialects/demo']}>
        <Routes><Route path="/dialects/:name" element={<DialectDetailPage />} /></Routes>
      </MemoryRouter>,
    );

    expect(await screen.findByText('Context window not set')).toBeInTheDocument();
  });

  it('shows the configured capacity on the detail page', async () => {
    getDialect.mockResolvedValueOnce({ data: dialect(372000), revision: 'revision-1' });
    render(
      <MemoryRouter initialEntries={['/dialects/demo']}>
        <Routes><Route path="/dialects/:name" element={<DialectDetailPage />} /></Routes>
      </MemoryRouter>,
    );

    expect(await screen.findByText('Context 372,000 tokens')).toBeInTheDocument();
  });
});
