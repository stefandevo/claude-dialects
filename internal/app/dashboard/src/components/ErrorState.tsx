import { AlertCircle, RefreshCw } from 'lucide-react';
import { Button } from './ui/button';

export function ErrorState({ message, onRetry }: { message: string; onRetry?: () => void }) {
  return (
    <div className="flex min-h-72 flex-col items-center justify-center rounded-lg border border-destructive/25 bg-destructive/5 p-8 text-center" role="alert">
      <AlertCircle className="size-8 text-destructive" aria-hidden="true" />
      <h2 className="mt-4 font-semibold">Dashboard data is unavailable</h2>
      <p className="mt-2 max-w-lg text-sm text-muted-foreground">{message}</p>
      {onRetry && <Button className="mt-5" variant="outline" onClick={onRetry}><RefreshCw /> Try again</Button>}
    </div>
  );
}
