import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { StatusBadge } from './StatusBadge';
import type { RuntimeState } from '../types';

describe('StatusBadge', () => {
  // The badge reads its configuration by state, so a state the API can return
  // and this map does not carry throws while rendering the page rather than
  // degrading to an unknown label. `crashed` is returned for a managed bridge
  // whose recorded process is gone.
  it('renders every runtime state the API can return', () => {
    const states: RuntimeState[] = ['running', 'stopped', 'degraded', 'crashed'];
    for (const state of states) {
      const { unmount } = render(<StatusBadge state={state} />);
      expect(screen.getByText(new RegExp(state, 'i'))).toBeInTheDocument();
      unmount();
    }
  });

  it('falls back to stopped when no state is supplied', () => {
    render(<StatusBadge />);
    expect(screen.getByText(/stopped/i)).toBeInTheDocument();
  });
});
