import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { PresetDrift } from '../types';
import { PresetDriftBadge } from './PresetDriftBadge';

describe('PresetDriftBadge', () => {
  it('renders a warning badge for confirmed drift', () => {
    const drift: PresetDrift = {
      state: 'drifted',
      preset: 'glm',
      changes: ['model glm-5.2 → glm-5.3'],
      command: 'cc-dialect create cc-glm --preset glm',
    };
    render(<PresetDriftBadge drift={drift} />);
    expect(screen.getByText('Preset drift')).toBeInTheDocument();
  });

  it('renders an outline badge for uncertain drift', () => {
    const drift: PresetDrift = { state: 'uncertain', preset: 'cursor-composer' };
    render(<PresetDriftBadge drift={drift} />);
    expect(screen.getByText('Preset may be outdated')).toBeInTheDocument();
  });
});
