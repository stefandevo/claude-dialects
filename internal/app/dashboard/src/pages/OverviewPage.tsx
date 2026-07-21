import { AlertTriangle, ArrowRight, Boxes, CirclePlay, Command, Plus, RadioTower, ServerCog } from 'lucide-react';
import { Link } from 'react-router-dom';
import { useDashboard } from '../AppContext';
import { EmptyState } from '../components/EmptyState';
import { ErrorState } from '../components/ErrorState';
import { LoadingState } from '../components/LoadingState';
import { PageHeader } from '../components/PageHeader';
import { StatusBadge } from '../components/StatusBadge';
import { Badge } from '../components/ui/badge';
import { Button } from '../components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '../components/ui/card';
import { pluralize } from '../utils';

export function OverviewPage() {
  const { dialects, launchers, cursor, loading, error, refresh, reportError } = useDashboard();

  if (loading) return <LoadingState />;
  if (error) return <ErrorState message={error} onRetry={() => void refresh().catch((caught) => reportError(caught))} />;

  const running = dialects.filter((dialect) => dialect.status?.state === 'running').length;
  const attention = dialects.filter((dialect) => dialect.status?.state === 'degraded').length;
  const cursorReady = Boolean(cursor?.runtimeCurrent && cursor.apiKeySet && !cursor.nodeError);

  const stats = [
    { label: 'Configured dialects', value: dialects.length, detail: pluralize(running, 'currently running'), icon: RadioTower },
    { label: 'Active runtimes', value: running, detail: running === dialects.length && dialects.length > 0 ? 'All configured dialects' : 'Healthy proxy and bridge', icon: CirclePlay },
    { label: 'Needs attention', value: attention, detail: attention ? 'Degraded runtime state' : 'No degraded runtimes', icon: AlertTriangle },
    { label: 'Native launchers', value: launchers.length, detail: launchers.every((launcher) => launcher.verified) ? 'All tracked files verified' : 'Verification issue detected', icon: Command },
  ];

  return (
    <div className="space-y-8">
      <PageHeader
        eyebrow="Operations overview"
        title="Your local model workspace"
        description="Inspect every isolated dialect, manage runtime health, and keep local integrations current from one loopback-only control plane."
        actions={<Button asChild><Link to="/dialects/new"><Plus />Create dialect</Link></Button>}
      />

      <section aria-labelledby="summary-heading">
        <h2 id="summary-heading" className="sr-only">Workspace summary</h2>
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
          {stats.map(({ label, value, detail, icon: Icon }) => (
            <Card key={label} className="bg-card/85 backdrop-blur-sm">
              <CardContent className="pt-5 sm:pt-6">
                <div className="flex items-start justify-between gap-4">
                  <div><p className="text-sm font-medium text-muted-foreground">{label}</p><p className="mt-3 text-3xl font-bold tracking-tight">{value}</p></div>
                  <div className="rounded-md bg-accent p-2.5 text-accent-foreground"><Icon className="size-5" aria-hidden="true" /></div>
                </div>
                <p className="mt-4 text-xs text-muted-foreground">{detail}</p>
              </CardContent>
            </Card>
          ))}
        </div>
      </section>

      <div className="grid gap-6 xl:grid-cols-[minmax(0,1fr)_22rem]">
        <section aria-labelledby="dialects-heading">
          <div className="mb-4 flex items-center justify-between gap-4">
            <div><h2 id="dialects-heading" className="text-xl font-semibold">Dialects</h2><p className="mt-1 text-sm text-muted-foreground">Runtime state and model routing at a glance.</p></div>
            {dialects.length > 0 && <Button variant="ghost" asChild><Link to="/dialects/new">Add another <ArrowRight /></Link></Button>}
          </div>
          {dialects.length === 0 ? (
            <EmptyState icon={RadioTower} title="No dialects configured" description="Create an isolated dialect backed by a preset or your own model endpoint." action={<Button asChild><Link to="/dialects/new"><Plus />Create your first dialect</Link></Button>} />
          ) : (
            <div className="grid gap-4 md:grid-cols-2">
              {dialects.map((dialect) => (
                <Link key={dialect.name} to={`/dialects/${encodeURIComponent(dialect.name)}`} className="group rounded-lg focus-visible:ring-offset-4">
                  <Card className="h-full bg-card/85 transition-colors group-hover:border-primary/45">
                    <CardHeader className="pb-4">
                      <div className="flex items-start justify-between gap-3">
                        <div className="min-w-0"><CardTitle className="truncate font-mono text-base">{dialect.name}</CardTitle><CardDescription className="mt-2 truncate">{dialect.provider} · {dialect.model}</CardDescription></div>
                        <StatusBadge state={dialect.status?.state} />
                      </div>
                    </CardHeader>
                    <CardContent className="flex items-center justify-between border-t pt-4 text-xs text-muted-foreground">
                      <div className="flex flex-wrap gap-2"><Badge variant="outline">Port {dialect.port}</Badge>{dialect.bridge && <Badge variant="outline">{dialect.bridge}</Badge>}</div>
                      <ArrowRight className="size-4 transition-transform group-hover:translate-x-0.5" aria-hidden="true" />
                    </CardContent>
                  </Card>
                </Link>
              ))}
            </div>
          )}
        </section>

        <aside className="space-y-4" aria-label="Integration readiness">
          <Card className="bg-card/85">
            <CardHeader><div className="mb-2 flex items-center justify-between"><div className="rounded-md bg-accent p-2 text-accent-foreground"><Boxes className="size-4" /></div><Badge variant={cursorReady ? 'success' : 'warning'}>{cursorReady ? 'Ready' : 'Action needed'}</Badge></div><CardTitle>Cursor runtime</CardTitle><CardDescription>Official SDK bridge and credential readiness.</CardDescription></CardHeader>
            <CardContent className="space-y-3 text-sm">
              <div className="flex justify-between gap-4"><span className="text-muted-foreground">Required SDK</span><span className="font-mono">{cursor?.requiredVersion}</span></div>
              <div className="flex justify-between gap-4"><span className="text-muted-foreground">Installed SDK</span><span className="font-mono">{cursor?.installedVersion || 'Not installed'}</span></div>
              <div className="flex justify-between gap-4"><span className="text-muted-foreground">API key</span><span>{cursor?.apiKeySet ? 'Available' : 'Missing'}</span></div>
              <Button className="mt-2 w-full" variant="outline" asChild><Link to="/runtime"><ServerCog />Manage runtime</Link></Button>
            </CardContent>
          </Card>
          <Card className="border-primary/20 bg-primary/[0.04]">
            <CardHeader><CardTitle className="text-base">Local by design</CardTitle><CardDescription>The dashboard listens only on a loopback IP. Configuration mutations also require same-origin and per-session CSRF validation.</CardDescription></CardHeader>
          </Card>
        </aside>
      </div>
    </div>
  );
}
