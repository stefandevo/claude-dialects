import { AlertTriangle, HelpCircle } from 'lucide-react';
import type { PresetDrift } from '../types';
import { Badge } from './ui/badge';

function driftTitle(drift: PresetDrift) {
  const changes = drift.changes?.length ? `\n${drift.changes.join('\n')}` : '';
  const command = drift.command ? `\n\nAdopt with:\n${drift.command}` : '';
  if (drift.state === 'drifted') {
    return `Preset drift detected for ${drift.preset ?? 'this dialect'}.${changes}${command}`;
  }
  return `Preset may be outdated for ${drift.preset ?? 'this dialect'}.${changes}${command}`;
}

export function PresetDriftBadge({ drift }: { drift: PresetDrift }) {
  if (drift.state === 'drifted') {
    return (
      <Badge variant="warning" title={driftTitle(drift)}>
        <AlertTriangle aria-hidden="true" className="size-3.5" />
        Preset drift
      </Badge>
    );
  }
  return (
    <Badge variant="outline" title={driftTitle(drift)}>
      <HelpCircle aria-hidden="true" className="size-3.5" />
      Preset may be outdated
    </Badge>
  );
}
