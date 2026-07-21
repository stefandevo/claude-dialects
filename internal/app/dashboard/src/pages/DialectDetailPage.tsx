import { Activity, ArrowLeft, Braces, Cpu, Edit3, KeyRound, LoaderCircle, Network, Play, RefreshCw, Square, TerminalSquare } from 'lucide-react';
import { useCallback, useEffect, useRef, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { useDashboard } from '../AppContext';
import { ConfirmDeleteDialog } from '../components/ConfirmDeleteDialog';
import { ErrorState } from '../components/ErrorState';
import { LoadingState } from '../components/LoadingState';
import { PageHeader } from '../components/PageHeader';
import { StatusBadge } from '../components/StatusBadge';
import { Badge } from '../components/ui/badge';
import { Button } from '../components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '../components/ui/card';
import type { DialectView, RuntimeStatus } from '../types';

function DetailRow({ label, value, mono = false }: { label: string; value?: string | number; mono?: boolean }) {
  return <div className="flex flex-col gap-1 border-b py-3 last:border-b-0 sm:flex-row sm:items-start sm:justify-between sm:gap-6"><dt className="text-sm text-muted-foreground">{label}</dt><dd className={`break-all text-sm font-medium sm:text-right ${mono ? 'font-mono' : ''}`}>{value || '—'}</dd></div>;
}

function ComponentCard({ label, status }: { label: string; status?: RuntimeStatus['proxy'] }) {
  return <div className="rounded-md border bg-background/65 p-4"><div className="flex items-center justify-between gap-3"><p className="font-medium">{label}</p><StatusBadge state={status?.state} /></div><dl className="mt-4 grid grid-cols-2 gap-3 text-xs"><div><dt className="text-muted-foreground">Port</dt><dd className="mt-1 font-mono">{status?.port || '—'}</dd></div><div><dt className="text-muted-foreground">PID</dt><dd className="mt-1 font-mono">{status?.pid || '—'}</dd></div></dl></div>;
}

export function DialectDetailPage() {
  const { name } = useParams();
  const navigate = useNavigate();
  const { api, refresh, refreshAfterMutation, registerRefreshHandler, reportError, notify } = useDashboard();
  const [snapshot, setSnapshot] = useState<{ dialect?: DialectView; revision: string }>({ revision: '' });
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();
  const [action, setAction] = useState<string>();
  const loadRequest = useRef(0);
  const removed = useRef(false);

  const load = useCallback(async (background = false) => {
    if (!name || removed.current) return;
    const request = ++loadRequest.current;
    if (!background) {
      setLoading(true);
      setError(undefined);
    }
    try {
      const result = await api.getDialect(name);
      if (request !== loadRequest.current || removed.current) return;
      setSnapshot({ dialect: result.data, revision: result.revision || '' });
      setError(undefined);
    } catch (caught) {
      if (request !== loadRequest.current || removed.current) return;
      if (background) throw caught;
      setError(caught instanceof Error ? caught.message : 'Unable to load dialect.');
    } finally {
      if (!background && request === loadRequest.current) setLoading(false);
    }
  }, [api, name]);

  useEffect(() => { void load(); }, [load]);
  useEffect(() => registerRefreshHandler(() => load(true)), [load, registerRefreshHandler]);

  async function runAction(next: 'start' | 'stop' | 'restart') {
    if (!name) return;
    setAction(next);
    let status: RuntimeStatus;
    try {
      status = await api.dialectAction(name, next);
    } catch (caught) {
      reportError(caught);
      setAction(undefined);
      return;
    }
    setSnapshot((current) => current.dialect ? { ...current, dialect: { ...current.dialect, status } } : current);
    notify(`${name} ${next === 'stop' ? 'stopped' : next === 'start' ? 'started' : 'restarted'}.`);
    setAction(undefined);
    await refreshAfterMutation();
  }

  async function remove() {
    if (!name) return;
    setAction('delete');
    try {
      await api.deleteDialect(name, snapshot.revision);
    } catch (caught) {
      reportError(caught);
      setAction(undefined);
      return;
    }
    removed.current = true;
    notify(`${name} deleted.`);
    navigate('/');
    setAction(undefined);
    await refreshAfterMutation();
  }

  if (loading) return <LoadingState label="Loading dialect" />;
  if (error || !snapshot.dialect) return <ErrorState message={error || 'Dialect not found.'} onRetry={() => void refresh().catch((caught) => reportError(caught))} />;

  const dialect = snapshot.dialect;
  const busy = Boolean(action);
  const expectedAuth = dialect.authProviders?.length
    ? dialect.authProviders
    : dialect.authProvider
      ? [dialect.authProvider]
      : [];
  const unauthenticated = new Set(dialect.unauthenticatedProviders ?? []);
  return (
    <div className="space-y-8">
      <PageHeader
        eyebrow={`${dialect.provider} dialect`}
        title={dialect.name}
        description={`Routes requests through ${dialect.model} with an isolated configuration, history, and local proxy runtime.`}
        actions={<><Button variant="outline" asChild><Link to="/"><ArrowLeft />Overview</Link></Button><Button asChild><Link to={`/dialects/${encodeURIComponent(dialect.name)}/edit`}><Edit3 />Edit configuration</Link></Button></>}
      />

      <div className="grid gap-6 xl:grid-cols-[minmax(0,1fr)_22rem]">
        <div className="space-y-6">
          <Card>
            <CardHeader><div className="flex items-start justify-between gap-4"><div><CardTitle>Runtime health</CardTitle><CardDescription className="mt-2">Control the local proxy and managed provider bridge together.</CardDescription></div><StatusBadge state={dialect.status?.state} /></div></CardHeader>
            <CardContent>
              <div className="grid gap-3 sm:grid-cols-2"><ComponentCard label="Proxy" status={dialect.status?.proxy} />{dialect.status?.bridge ? <ComponentCard label="Provider bridge" status={dialect.status.bridge} /> : <div className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">This dialect does not require a managed provider bridge.</div>}</div>
              <div className="mt-5 flex flex-wrap gap-2">
                <Button onClick={() => void runAction('start')} disabled={busy || dialect.status?.state === 'running'}>{action === 'start' ? <LoaderCircle className="animate-spin" /> : <Play />}Start</Button>
                <Button variant="outline" onClick={() => void runAction('stop')} disabled={busy || dialect.status?.state === 'stopped'}>{action === 'stop' ? <LoaderCircle className="animate-spin" /> : <Square />}Stop</Button>
                <Button variant="secondary" onClick={() => void runAction('restart')} disabled={busy}>{action === 'restart' ? <LoaderCircle className="animate-spin" /> : <RefreshCw />}Restart</Button>
              </div>
            </CardContent>
          </Card>

          {expectedAuth.length > 0 && (
            <Card>
              <CardHeader>
                <div className="flex items-start justify-between gap-4">
                  <div>
                    <CardTitle className="flex items-center gap-2"><KeyRound className="size-4 text-primary" />Authentication</CardTitle>
                    <CardDescription className="mt-2">{expectedAuth.length > 1 ? 'This dialect maps tiers across several providers — each needs its own OAuth login.' : 'OAuth login required before this dialect can start.'}</CardDescription>
                  </div>
                  <Badge variant={unauthenticated.size === 0 ? 'success' : 'warning'}>{unauthenticated.size === 0 ? 'Ready' : `${unauthenticated.size} to authenticate`}</Badge>
                </div>
              </CardHeader>
              <CardContent className="space-y-2">
                {expectedAuth.map((provider) => {
                  const pending = unauthenticated.has(provider);
                  return (
                    <div key={provider} className="flex items-center justify-between gap-4 rounded-md border p-3 text-sm">
                      <span className="font-mono">{provider}</span>
                      {pending
                        ? <span className="flex flex-wrap items-center gap-2 text-muted-foreground"><Badge variant="warning">Needs auth</Badge><code className="text-xs">cc-dialect auth {dialect.name} {provider}</code></span>
                        : <Badge variant="success">Authenticated</Badge>}
                    </div>
                  );
                })}
              </CardContent>
            </Card>
          )}

          <div className="grid gap-6 lg:grid-cols-2">
            <Card><CardHeader><CardTitle className="flex items-center gap-2"><Cpu className="size-4 text-primary" />Model mapping</CardTitle></CardHeader><CardContent><dl><DetailRow label="Primary" value={dialect.model} mono /><DetailRow label="Subagent" value={dialect.subagentModel} mono /><DetailRow label="Opus alias" value={dialect.opusModel} mono /><DetailRow label="Sonnet alias" value={dialect.sonnetModel} mono /><DetailRow label="Haiku alias" value={dialect.haikuModel} mono /></dl></CardContent></Card>
            <Card><CardHeader><CardTitle className="flex items-center gap-2"><Network className="size-4 text-primary" />Routing</CardTitle></CardHeader><CardContent><dl><DetailRow label="Preset" value={dialect.preset} /><DetailRow label="Proxy port" value={dialect.port} mono /><DetailRow label="Bridge" value={dialect.bridge} /><DetailRow label="Bridge port" value={dialect.bridgePort} mono /><DetailRow label="Base URL" value={dialect.baseUrl} mono /></dl></CardContent></Card>
          </div>

          <Card><CardHeader><CardTitle className="flex items-center gap-2"><Braces className="size-4 text-primary" />Request behavior</CardTitle></CardHeader><CardContent className="flex flex-wrap gap-2"><Badge variant="outline">Effort {dialect.effort ? 'enabled' : 'disabled'}</Badge><Badge variant="outline">Level {dialect.effortLevel || 'auto'}</Badge><Badge variant="outline">Concurrency {dialect.concurrency}</Badge><Badge variant="outline">Tool search {dialect.toolSearch ? 'enabled' : 'disabled'}</Badge>{dialect.authTokenEnv && <Badge variant="outline">Token via {dialect.authTokenEnv}</Badge>}{dialect.extraEnvKeys?.map((key) => <Badge key={key} variant="secondary">Env: {key}</Badge>)}</CardContent></Card>
        </div>

        <aside className="space-y-4">
          <Card><CardHeader><CardTitle className="flex items-center gap-2 text-base"><TerminalSquare className="size-4 text-primary" />Launch commands</CardTitle><CardDescription>Run the dialect directly, or optionally install a convenience shim.</CardDescription></CardHeader><CardContent className="space-y-3"><div className="rounded-md border bg-muted p-3 font-mono text-sm">$ cc-dialect run {dialect.name}</div><div className="rounded-md border bg-muted/60 p-3 font-mono text-xs text-muted-foreground">$ cc-dialect shim install {dialect.name}</div></CardContent></Card>
          <Card><CardHeader><CardTitle className="flex items-center gap-2 text-base"><Activity className="size-4 text-primary" />Configuration safety</CardTitle><CardDescription>Updates use revision preconditions. If another process changes this configuration, the dashboard will require a reload instead of overwriting it.</CardDescription></CardHeader></Card>
          <Card className="border-destructive/25"><CardHeader><CardTitle className="text-base">Danger zone</CardTitle><CardDescription>Deletion stops this dialect and removes its validated instance state.</CardDescription></CardHeader><CardContent><ConfirmDeleteDialog name={dialect.name} noun="dialect" busy={action === 'delete'} onConfirm={remove} /></CardContent></Card>
        </aside>
      </div>
    </div>
  );
}
