import { AlertTriangle, CircleCheck, CircleStop, CircleX } from 'lucide-react';
import type { RuntimeState } from '../types';
import { Badge } from './ui/badge';

const stateConfig: Record<RuntimeState, { label: string; variant: 'success' | 'secondary' | 'warning' | 'destructive'; icon: typeof CircleCheck }> = {
  running: { label: 'Running', variant: 'success', icon: CircleCheck },
  stopped: { label: 'Stopped', variant: 'secondary', icon: CircleStop },
  degraded: { label: 'Degraded', variant: 'warning', icon: AlertTriangle },
  // A bridge that died leaving its PID record behind: not stopped, and not
  // something a restart of the proxy alone repairs.
  crashed: { label: 'Crashed', variant: 'destructive', icon: CircleX },
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
