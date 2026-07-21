import { AlertTriangle, CheckCircle2, Cpu, KeyRound, LoaderCircle, PackageCheck, RefreshCw, ServerCog } from 'lucide-react';
import { useState } from 'react';
import { useDashboard } from '../AppContext';
import { ErrorState } from '../components/ErrorState';
import { LoadingState } from '../components/LoadingState';
import { PageHeader } from '../components/PageHeader';
import { Badge } from '../components/ui/badge';
import { Button } from '../components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '../components/ui/card';
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from '../components/ui/dialog';
import type { CursorInstallResult } from '../types';
import { describeVersion } from '../utils';

export function RuntimePage() {
  const { cursor, dialects, loading, error, refresh, refreshAfterMutation, api, reportError, notify } = useDashboard();
  const [installing, setInstalling] = useState(false);
  const [result, setResult] = useState<CursorInstallResult>();
  const [open, setOpen] = useState(false);

  if (loading) return <LoadingState label="Inspecting Cursor runtime" />;
  if (error || !cursor) return <ErrorState message={error || 'Cursor runtime status is unavailable.'} onRetry={() => void refresh().catch((caught) => reportError(caught))} />;

  const cursorDialects = dialects.filter((dialect) => dialect.provider.toLowerCase().includes('cursor') || dialect.bridge?.toLowerCase().includes('cursor'));
  const ready = cursor.runtimeCurrent && cursor.apiKeySet && !cursor.nodeError;

  async function install() {
    setInstalling(true);
    let installed: CursorInstallResult;
    try {
      installed = await api.installCursorRuntime();
    } catch (caught) {
      reportError(caught);
      setInstalling(false);
      return;
    }
    setResult(installed);
    notify(`Cursor SDK ${installed.installedVersion} installed.`);
    setOpen(false);
    setInstalling(false);
    await refreshAfterMutation();
  }

  const checks = [
    { title: 'Node.js', value: cursor.nodeError || describeVersion(cursor.nodeVersion), detail: cursor.nodePath || 'Node.js 22.13 or newer is required.', okay: !cursor.nodeError && Boolean(cursor.nodeVersion), icon: Cpu },
    { title: 'Cursor SDK', value: describeVersion(cursor.installedVersion), detail: `Required version: ${cursor.requiredVersion}`, okay: cursor.runtimeCurrent, icon: PackageCheck },
    { title: 'API credential', value: cursor.apiKeySet ? 'CURSOR_API_KEY is set' : 'CURSOR_API_KEY is missing', detail: 'The dashboard never receives or displays the key value.', okay: cursor.apiKeySet, icon: KeyRound },
  ];

  return (
    <div className="space-y-8">
      <PageHeader eyebrow="Provider runtime" title="Cursor SDK bridge" description="Inspect the private Node.js runtime used by Cursor-backed dialects and install the exact SDK version required by this release." actions={<Badge variant={ready ? 'success' : 'warning'}>{ready ? <CheckCircle2 /> : <AlertTriangle />}{ready ? 'Runtime ready' : 'Action needed'}</Badge>} />

      <div className="grid gap-4 lg:grid-cols-3">
        {checks.map(({ title, value, detail, okay, icon: Icon }) => <Card key={title}><CardHeader><div className="flex items-start justify-between gap-3"><div className="rounded-md bg-accent p-2.5 text-accent-foreground"><Icon className="size-5" /></div><Badge variant={okay ? 'success' : 'warning'}>{okay ? 'Ready' : 'Check'}</Badge></div><CardTitle className="pt-3">{title}</CardTitle><CardDescription className="break-all font-mono text-xs">{value}</CardDescription></CardHeader><CardContent><p className="text-sm leading-relaxed text-muted-foreground">{detail}</p></CardContent></Card>)}
      </div>

      <div className="grid gap-6 xl:grid-cols-[minmax(0,1fr)_22rem]">
        <Card><CardHeader><CardTitle>Install or update runtime</CardTitle><CardDescription>The installer verifies Node and npm, writes a private ESM runtime, and installs the pinned SDK with lifecycle scripts disabled.</CardDescription></CardHeader><CardContent className="space-y-5"><div className="rounded-lg border bg-muted/60 p-4"><div className="flex items-center justify-between gap-4"><div><p className="font-medium">Target @cursor/sdk version</p><p className="mt-1 font-mono text-sm text-muted-foreground">{cursor.requiredVersion}</p></div>{cursor.runtimeCurrent && <Badge variant="success">Current</Badge>}</div></div>{cursorDialects.length > 0 && <div className="flex items-start gap-3 rounded-lg border border-warning/30 bg-warning/10 p-4 text-sm"><AlertTriangle className="mt-0.5 size-5 shrink-0 text-warning" /><p className="leading-relaxed">Updating may stop running Cursor-backed dialects so they can restart against the new bridge. Affected names are reported after installation.</p></div>}<Dialog open={open} onOpenChange={setOpen}><DialogTrigger asChild><Button><RefreshCw />{cursor.runtimeInstalled ? 'Reinstall required version' : 'Install Cursor runtime'}</Button></DialogTrigger><DialogContent><DialogHeader><DialogTitle>Install @cursor/sdk {cursor.requiredVersion}?</DialogTitle><DialogDescription>This runs npm locally with scripts, audit, funding output, and development dependencies disabled. Any active Cursor dialect runtimes may be stopped.</DialogDescription></DialogHeader><DialogFooter><DialogClose asChild><Button variant="outline">Cancel</Button></DialogClose><Button onClick={() => void install()} disabled={installing}>{installing ? <LoaderCircle className="animate-spin" /> : <ServerCog />}{installing ? 'Installing…' : 'Install runtime'}</Button></DialogFooter></DialogContent></Dialog></CardContent></Card>

        <aside className="space-y-4"><Card><CardHeader><CardTitle className="text-base">Cursor-backed dialects</CardTitle><CardDescription>{cursorDialects.length ? `${cursorDialects.length} configured dialect${cursorDialects.length === 1 ? '' : 's'} use this runtime.` : 'No configured dialect currently uses the Cursor bridge.'}</CardDescription></CardHeader><CardContent className="space-y-2">{cursorDialects.map((dialect) => <div key={dialect.name} className="flex items-center justify-between rounded-md border px-3 py-2 text-sm"><span className="font-mono">{dialect.name}</span><Badge variant="outline">{dialect.status?.state || 'unknown'}</Badge></div>)}</CardContent></Card>{result && <Card className="border-success/25"><CardHeader><CardTitle className="text-base">Last installation</CardTitle><CardDescription>Installed SDK {result.installedVersion} with Node {result.nodeVersion}.</CardDescription></CardHeader><CardContent>{result.stoppedDialects.length > 0 ? <><p className="text-sm text-muted-foreground">Stopped runtimes:</p><div className="mt-2 flex flex-wrap gap-2">{result.stoppedDialects.map((name) => <Badge key={name} variant="warning">{name}</Badge>)}</div></> : <p className="text-sm text-muted-foreground">No running dialects needed to be stopped.</p>}</CardContent></Card>}</aside>
      </div>
    </div>
  );
}
