import { ArrowLeft, SearchX } from 'lucide-react';
import { Link } from 'react-router-dom';
import { Button } from '../components/ui/button';

export function NotFoundPage() {
  return (
    <div className="flex min-h-[65vh] flex-col items-center justify-center text-center">
      <SearchX className="size-10 text-muted-foreground" />
      <h1 className="mt-5 text-2xl font-bold">Page not found</h1>
      <p className="mt-2 text-sm text-muted-foreground">The dashboard route you requested does not exist.</p>
      <Button className="mt-6" asChild><Link to="/"><ArrowLeft />Return to overview</Link></Button>
    </div>
  );
}
