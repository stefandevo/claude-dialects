import { AlertTriangle, CheckCircle2, Command, Edit3, LoaderCircle, Plus, ShieldCheck } from 'lucide-react';
import { useState, type FormEvent, type ReactNode } from 'react';
import { useDashboard } from '../AppContext';
import { ConfirmDeleteDialog } from '../components/ConfirmDeleteDialog';
import { EmptyState } from '../components/EmptyState';
import { ErrorState } from '../components/ErrorState';
import { LoadingState } from '../components/LoadingState';
import { PageHeader } from '../components/PageHeader';
import { Badge } from '../components/ui/badge';
import { Button } from '../components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '../components/ui/card';
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from '../components/ui/dialog';
import { Input } from '../components/ui/input';
import { Label } from '../components/ui/label';
import { Switch } from '../components/ui/switch';
import type { NativeLauncherView } from '../types';
import { isValidName } from '../utils';

interface LauncherDialogProps {
  launcher?: NativeLauncherView;
  trigger: ReactNode;
}

export function validLauncherDirectory(directory: string) {
  return directory === '' || directory.startsWith('/');
}

export function LauncherDialog({ launcher, trigger }: LauncherDialogProps) {
  const { api, launcherRevision, refresh, refreshAfterMutation, reportError, notify } = useDashboard();
  const [open, setOpen] = useState(false);
  const [name, setName] = useState(launcher?.name || '');
  const [directory, setDirectory] = useState('');
  const [dangerous, setDangerous] = useState(launcher?.dangerous || false);
  const [saving, setSaving] = useState(false);
  const [validation, setValidation] = useState<string>();

  function handleOpenChange(next: boolean) {
    if (next) {
      setName(launcher?.name || '');
      setDirectory('');
      setDangerous(launcher?.dangerous || false);
      setValidation(undefined);
    }
    setOpen(next);
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    setValidation(undefined);
    if (!isValidName(name) || name === 'claude') {
      setValidation('Choose a valid lowercase command name other than claude.');
      return;
    }
    if (!validLauncherDirectory(directory)) {
      setValidation('Install directory must be an absolute path beginning with /, or left blank to use the default.');
      return;
    }

    setSaving(true);
    try {
      if (launcher) {
        await api.updateLauncher(launcher.name, { name: launcher.name, directory, dangerous }, launcherRevision);
      } else {
        await api.createLauncher({ name, directory, dangerous }, launcherRevision || undefined);
      }
    } catch (caught) {
      reportError(caught, async () => {
        handleOpenChange(false);
        await refresh();
      });
      setSaving(false);
      return;
    }
    notify(`${name} launcher ${launcher ? 'updated' : 'installed'}.`);
    handleOpenChange(false);
    setSaving(false);
    await refreshAfterMutation();
  }

  return <Dialog open={open} onOpenChange={handleOpenChange}><DialogTrigger asChild>{trigger}</DialogTrigger><DialogContent><form onSubmit={submit}><DialogHeader><DialogTitle>{launcher ? `Edit ${launcher.name}` : 'Install native launcher'}</DialogTitle><DialogDescription>Write a tracked executable that forwards to the current Claude Code binary with this command name.</DialogDescription></DialogHeader>{validation && <div className="mt-4 rounded-md border border-destructive/25 bg-destructive/5 p-3 text-sm text-destructive" role="alert">{validation}</div>}<div className="mt-5 space-y-5"><div className="grid gap-2"><Label htmlFor={`launcher-name-${launcher?.name || 'new'}`}>Command name</Label><Input id={`launcher-name-${launcher?.name || 'new'}`} value={name} disabled={Boolean(launcher)} onChange={(event) => setName(event.target.value)} placeholder="cc-native" autoComplete="off" /></div><div className="grid gap-2"><Label htmlFor={`launcher-directory-${launcher?.name || 'new'}`}>Install directory</Label><Input id={`launcher-directory-${launcher?.name || 'new'}`} value={directory} onChange={(event) => setDirectory(event.target.value)} placeholder={launcher ? 'Leave blank to keep the current directory' : '/Users/you/bin'} /><p className="text-xs leading-relaxed text-muted-foreground">{launcher ? 'Leave blank to keep the tracked directory. Moving a launcher requires removal and reinstallation.' : 'Leave blank to use the default local bin directory, or enter an absolute path beginning with /.'}</p></div><div className="flex items-start justify-between gap-4 rounded-md border p-4"><div><Label htmlFor={`launcher-dangerous-${launcher?.name || 'new'}`}>Skip permission prompts</Label><p className="mt-1 text-xs leading-relaxed text-muted-foreground">Passes the dangerous permission-bypass flag to every launched session.</p></div><Switch id={`launcher-dangerous-${launcher?.name || 'new'}`} checked={dangerous} onCheckedChange={setDangerous} /></div>{dangerous && <div className="flex items-start gap-3 rounded-md border border-warning/30 bg-warning/10 p-3 text-sm"><AlertTriangle className="mt-0.5 size-4 shrink-0 text-warning" /><p>This launcher grants Claude Code broad tool access without interactive approval. Use only in trusted directories.</p></div>}</div><DialogFooter className="mt-6"><DialogClose asChild><Button type="button" variant="outline">Cancel</Button></DialogClose><Button type="submit" disabled={saving}>{saving ? <LoaderCircle className="animate-spin" /> : <Command />}{saving ? 'Saving…' : launcher ? 'Save launcher' : 'Install launcher'}</Button></DialogFooter></form></DialogContent></Dialog>;
}

export function LaunchersPage() {
  const { launchers, launcherRevision, loading, error, refresh, refreshAfterMutation, api, reportError, notify } = useDashboard();
  const [deleting, setDeleting] = useState<string>();

  if (loading) return <LoadingState label="Loading native launchers" />;
  if (error) return <ErrorState message={error} onRetry={() => void refresh().catch((caught) => reportError(caught))} />;

  async function remove(name: string) {
    setDeleting(name);
    try {
      await api.deleteLauncher(name, launcherRevision);
    } catch (caught) {
      reportError(caught);
      setDeleting(undefined);
      return;
    }
    notify(`${name} launcher removed.`);
    setDeleting(undefined);
    void refreshAfterMutation();
  }

  return <div className="space-y-8"><PageHeader eyebrow="Terminal integration" title="Native launchers" description="Install and verify tracked command wrappers that launch the current Claude Code binary with optional permission behavior." actions={<LauncherDialog trigger={<Button><Plus />Install launcher</Button>} />} />{launchers.length === 0 ? <EmptyState icon={Command} title="No native launchers installed" description="Create a local executable in the default local bin directory or another absolute directory on your PATH." action={<LauncherDialog trigger={<Button><Plus />Install your first launcher</Button>} />} /> : <div className="grid gap-4 lg:grid-cols-2">{launchers.map((launcher) => <Card key={launcher.name}><CardHeader><div className="flex items-start justify-between gap-4"><div className="min-w-0"><CardTitle className="truncate font-mono">{launcher.name}</CardTitle><CardDescription className="mt-2 break-all font-mono text-xs">{launcher.path}</CardDescription></div><Badge variant={launcher.verified ? 'success' : 'destructive'}>{launcher.verified ? <CheckCircle2 /> : <AlertTriangle />}{launcher.verified ? 'Verified' : 'Modified'}</Badge></div></CardHeader><CardContent className="space-y-5"><dl className="space-y-3 text-sm"><div className="flex flex-col gap-1 sm:flex-row sm:justify-between sm:gap-4"><dt className="text-muted-foreground">Claude Code binary</dt><dd className="break-all font-mono text-xs sm:text-right">{launcher.claudePath}</dd></div><div className="flex justify-between gap-4"><dt className="text-muted-foreground">Permission mode</dt><dd className="font-medium">{launcher.dangerous ? 'Bypass prompts' : 'Standard approvals'}</dd></div></dl>{!launcher.verified && <div className="flex items-start gap-3 rounded-md border border-destructive/25 bg-destructive/5 p-3 text-sm"><AlertTriangle className="mt-0.5 size-4 shrink-0 text-destructive" /><p>The tracked file is missing or its content changed outside cc-dialect. Removal and updates will be blocked until the file is restored.</p></div>}<div className="flex flex-wrap gap-2 border-t pt-4"><LauncherDialog launcher={launcher} trigger={<Button variant="outline"><Edit3 />Edit</Button>} /><ConfirmDeleteDialog name={launcher.name} noun="launcher" busy={deleting === launcher.name} onConfirm={() => remove(launcher.name)} /></div></CardContent></Card>)}</div>}<Card className="border-primary/20 bg-primary/[0.04]"><CardHeader><CardTitle className="flex items-center gap-2 text-base"><ShieldCheck className="size-4 text-primary" />Integrity protection</CardTitle><CardDescription>Each launcher is recorded with its absolute path, Claude Code target, permission mode, and SHA-256 content digest. Externally modified files are never silently overwritten or removed.</CardDescription></CardHeader></Card></div>;
}
