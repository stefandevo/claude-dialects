import { render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { DialectView } from '../types';

const getDialect = vi.fn();
const registerRefreshHandler = vi.fn(() => () => undefined);

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

function mixedDialect(overrides: Partial<DialectView> = {}): DialectView {
  return {
    name: 'cc-mixed', preset: 'mixed-frontier', provider: 'mixed', model: 'claude-fable-5',
    opusModel: 'gpt-5.6-sol', sonnetModel: 'kimi-k3', haikuModel: 'grok-4.5',
    effort: true, concurrency: 3, toolSearch: false, port: 43170,
    authProviders: ['claude', 'codex', 'kimi', 'xai'],
    status: { state: 'stopped', proxy: { state: 'stopped', port: 43170 } },
    ...overrides,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe('multi-provider authentication surfacing', () => {
  it('lists each provider still needing OAuth with a copyable auth command', async () => {
    getDialect.mockResolvedValueOnce({
      data: mixedDialect({ unauthenticatedProviders: ['kimi', 'xai'] }),
      revision: 'revision-1',
    });
    render(
      <MemoryRouter initialEntries={['/dialects/cc-mixed']}>
        <Routes><Route path="/dialects/:name" element={<DialectDetailPage />} /></Routes>
      </MemoryRouter>,
    );

    await screen.findByText('Authentication');
    expect(screen.getByText('2 to authenticate')).toBeInTheDocument();
    expect(screen.getByText('cc-dialect auth cc-mixed kimi')).toBeInTheDocument();
    expect(screen.getByText('cc-dialect auth cc-mixed xai')).toBeInTheDocument();
    // codex has credentials, so it shows as authenticated rather than pending.
    expect(screen.queryByText('cc-dialect auth cc-mixed codex')).not.toBeInTheDocument();
  });

  it('marks the dialect ready when every expected provider is authenticated', async () => {
    getDialect.mockResolvedValueOnce({
      data: mixedDialect({ unauthenticatedProviders: undefined }),
      revision: 'revision-1',
    });
    render(
      <MemoryRouter initialEntries={['/dialects/cc-mixed']}>
        <Routes><Route path="/dialects/:name" element={<DialectDetailPage />} /></Routes>
      </MemoryRouter>,
    );

    await screen.findByText('Authentication');
    expect(screen.getByText('Ready')).toBeInTheDocument();
    expect(screen.getAllByText('Authenticated').length).toBe(4);
  });
});
