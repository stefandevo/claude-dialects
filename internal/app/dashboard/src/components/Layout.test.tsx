import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';

vi.mock('../AppContext', () => ({
  useDashboard: () => ({
    bootstrap: { version: '1.0.0' },
    refresh: vi.fn(async () => undefined),
    loading: false,
    refreshing: false,
    reportError: vi.fn(),
  }),
}));

import { Layout } from './Layout';

describe('mobile navigation sheet', () => {
  it('exposes expanded state, closes with Escape, and restores trigger focus', async () => {
    render(
      <MemoryRouter>
        <Routes><Route element={<Layout />}><Route index element={<div>Overview content</div>} /></Route></Routes>
      </MemoryRouter>,
    );
    const trigger = screen.getByRole('button', { name: 'Open navigation' });
    expect(trigger).toHaveAttribute('aria-expanded', 'false');
    expect(trigger).toHaveAttribute('aria-controls', 'mobile-navigation');

    fireEvent.click(trigger);
    expect(trigger).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByRole('dialog', { name: 'Dashboard navigation' })).toBeInTheDocument();

    fireEvent.keyDown(document, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Dashboard navigation' })).not.toBeInTheDocument());
    expect(trigger).toHaveFocus();
    expect(trigger).toHaveAttribute('aria-expanded', 'false');
  });
});
