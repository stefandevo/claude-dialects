import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { NativeLauncherView } from '../types';

const createLauncher = vi.fn();
const updateLauncher = vi.fn();
const refresh = vi.fn(async () => undefined);
const refreshAfterMutation = vi.fn(async () => undefined);
const reportError = vi.fn();
const notify = vi.fn();

vi.mock('../AppContext', () => ({
  useDashboard: () => ({
    api: { createLauncher, updateLauncher },
    launcherRevision: 'revision-2',
    refresh,
    refreshAfterMutation,
    reportError,
    notify,
  }),
}));

import { LauncherDialog } from './LaunchersPage';

beforeEach(() => {
  vi.clearAllMocks();
  createLauncher.mockResolvedValue({ data: { launcher: {}, revision: 'revision-3' } });
  updateLauncher.mockResolvedValue({ data: { launcher: {}, revision: 'revision-3' } });
});

describe('LauncherDialog', () => {
  it('rejects tilde and relative install directories in the form', () => {
    render(<LauncherDialog trigger={<button>Install launcher</button>} />);
    fireEvent.click(screen.getByRole('button', { name: 'Install launcher' }));
    fireEvent.change(screen.getByLabelText('Command name'), { target: { value: 'native-safe' } });
    fireEvent.change(screen.getByLabelText('Install directory'), { target: { value: '~/.local/bin' } });
    fireEvent.click(screen.getAllByRole('button', { name: 'Install launcher' }).at(-1)!);

    expect(screen.getByRole('alert')).toHaveTextContent('absolute path beginning with /');
    expect(createLauncher).not.toHaveBeenCalled();
  });

  it('submits an absolute directory and closes before the follow-up refresh', async () => {
    render(<LauncherDialog trigger={<button>Install launcher</button>} />);
    fireEvent.click(screen.getByRole('button', { name: 'Install launcher' }));
    fireEvent.change(screen.getByLabelText('Command name'), { target: { value: 'native-safe' } });
    fireEvent.change(screen.getByLabelText('Install directory'), { target: { value: '/Users/test/bin' } });
    fireEvent.click(screen.getAllByRole('button', { name: 'Install launcher' }).at(-1)!);

    await waitFor(() => expect(createLauncher).toHaveBeenCalledWith(
      { name: 'native-safe', directory: '/Users/test/bin', dangerous: false },
      'revision-2',
    ));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(refreshAfterMutation).toHaveBeenCalledTimes(1);
  });

  it('resets edit state from refreshed props every time it opens', () => {
    const initial: NativeLauncherView = { name: 'native', path: '/tmp/native', claudePath: '/tmp/claude', dangerous: false, verified: true };
    const { rerender } = render(<LauncherDialog launcher={initial} trigger={<button>Edit launcher</button>} />);
    fireEvent.click(screen.getByRole('button', { name: 'Edit launcher' }));
    expect(screen.getByRole('switch', { name: 'Skip permission prompts' })).not.toBeChecked();
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));

    const refreshed = { ...initial, dangerous: true };
    rerender(<LauncherDialog launcher={refreshed} trigger={<button>Edit launcher</button>} />);
    fireEvent.click(screen.getByRole('button', { name: 'Edit launcher' }));

    expect(screen.getByRole('switch', { name: 'Skip permission prompts' })).toBeChecked();
  });
});
