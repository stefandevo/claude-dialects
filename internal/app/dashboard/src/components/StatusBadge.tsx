import { AlertTriangle, CircleCheck, CircleStop } from 'lucide-react';
import type { RuntimeState } from '../types';
import { Badge } from './ui/badge';

const stateConfig = {
  running: { label: 'Running', variant: 'success' as const, icon: CircleCheck },
  stopped: { label: 'Stopped', variant: 'secondary' as const, icon: CircleStop },
  degraded: { label: 'Degraded', variant: 'warning' as const, icon: AlertTriangle },
};

export function StatusBadge({ state = 'stopped' }: { state?: RuntimeState }) {
  const config = stateConfig[state];
  const Icon = config.icon;
  return (
    <Badge variant={config.variant}>
      <Icon aria-hidden="true" className="size-3.5" />
      {config.label}
    </Badge>
  );
}
