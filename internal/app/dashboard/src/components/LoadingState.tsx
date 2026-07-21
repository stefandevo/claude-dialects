import { LoaderCircle } from 'lucide-react';

export function LoadingState({ label = 'Loading dashboard' }: { label?: string }) {
  return (
    <div className="flex min-h-72 items-center justify-center text-sm text-muted-foreground" role="status">
      <LoaderCircle className="mr-2 size-5 animate-spin" aria-hidden="true" />
      {label}…
    </div>
  );
}
