import { ArrowLeft, Info, LoaderCircle, Save, SlidersHorizontal } from 'lucide-react';
import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { useDashboard } from '../AppContext';
import { ErrorState } from '../components/ErrorState';
import { LoadingState } from '../components/LoadingState';
import { PageHeader } from '../components/PageHeader';
import { Button } from '../components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '../components/ui/card';
import { Input } from '../components/ui/input';
import { Label } from '../components/ui/label';
import { Switch } from '../components/ui/switch';
import type { DialectInput, DialectView } from '../types';
import { isValidName } from '../utils';

const emptyInput: DialectInput = {
  name: '', preset: '', model: '', subagentModel: '', opusModel: '', sonnetModel: '', haikuModel: '',
  effortLevel: 'auto', concurrency: 3, port: 0, bridgePort: 0, baseUrl: '', authTokenEnv: '', effort: false, toolSearch: false,
};

function inputFromView(view: DialectView): DialectInput {
  return {
    name: view.name,
    preset: view.preset === 'custom' ? '' : view.preset,
    model: view.model || '',
    subagentModel: view.subagentModel || '',
    opusModel: view.opusModel || '',
    sonnetModel: view.sonnetModel || '',
    haikuModel: view.haikuModel || '',
    effortLevel: view.effortLevel || 'auto',
    concurrency: view.concurrency || 3,
    port: view.port || 0,
    bridgePort: view.bridgePort || 0,
    baseUrl: view.baseUrl || '',
    authTokenEnv: view.authTokenEnv || '',
    effort: view.effort,
    toolSearch: view.toolSearch,
  };
}

function NumberField({ id, label, value, onChange, hint, min, max }: { id: string; label: string; value: number; onChange: (value: number) => void; hint?: string; min?: number; max?: number }) {
  return <div className="grid gap-2"><Label htmlFor={id}>{label}</Label><Input id={id} type="number" min={min} max={max} value={value || ''} placeholder="Automatic" onChange={(event) => onChange(event.target.value === '' ? 0 : Number(event.target.value))} />{hint && <p className="text-xs leading-relaxed text-muted-foreground">{hint}</p>}</div>;
}

export function DialectFormPage() {
  const { name } = useParams();
  const editing = Boolean(name);
  const navigate = useNavigate();
  const { api, presets, dialectRevision, refresh, refreshAfterMutation, registerRefreshHandler, reportError, notify } = useDashboard();
  const [snapshot, setSnapshot] = useState<{ input: DialectInput; revision: string }>({ input: emptyInput, revision: '' });
  const [loading, setLoading] = useState(editing);
  const [saving, setSaving] = useState(false);
  const [loadError, setLoadError] = useState<string>();
  const [validation, setValidation] = useState<string>();
  const loadRequest = useRef(0);
  const input = snapshot.input;

  const loadDialect = useCallback(async (background = false) => {
    if (!name) return;
    const request = ++loadRequest.current;
    if (!background) {
      setLoading(true);
      setLoadError(undefined);
    }
    try {
      const result = await api.getDialect(name);
      if (request !== loadRequest.current) return;
      setSnapshot({ input: inputFromView(result.data), revision: result.revision || '' });
      setLoadError(undefined);
    } catch (caught) {
      if (request !== loadRequest.current) return;
      if (background) throw caught;
      setLoadError(caught instanceof Error ? caught.message : 'Unable to load dialect.');
    } finally {
      if (!background && request === loadRequest.current) setLoading(false);
    }
  }, [api, name]);

  useEffect(() => { void loadDialect(); }, [loadDialect]);
  useEffect(() => {
    if (!editing) return;
    return registerRefreshHandler(() => loadDialect(true));
  }, [editing, loadDialect, registerRefreshHandler]);

  function update<K extends keyof DialectInput>(key: K, value: DialectInput[K]) {
    setSnapshot((current) => ({ ...current, input: { ...current.input, [key]: value } }));
  }

  function choosePreset(presetName: string) {
    if (!presetName) {
      setSnapshot((current) => ({ ...current, input: { ...emptyInput, name: current.input.name, port: editing ? current.input.port : 0 } }));
      return;
    }
    const preset = presets.find((item) => item.name === presetName);
    if (!preset) return;
    setSnapshot((current) => ({ ...current, input: { ...inputFromView(preset), name: current.input.name, preset: presetName, port: editing ? current.input.port : 0 } }));
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    setValidation(undefined);
    if (!isValidName(input.name)) {
      setValidation('Use lowercase letters, numbers, hyphens, or underscores; the name cannot begin with a hyphen.');
      return;
    }
    if (!input.preset && !input.model.trim()) {
      setValidation('Choose a preset or provide a model identifier.');
      return;
    }
    if (input.concurrency < 1) {
      setValidation('Concurrency must be at least 1.');
      return;
    }
    for (const [label, port] of [['Proxy', input.port], ['Bridge', input.bridgePort]] as const) {
      if (port !== 0 && (port < 1024 || port > 65535)) {
        setValidation(`${label} port must be between 1024 and 65535.`);
        return;
      }
    }
    if (input.port && input.bridgePort && input.port === input.bridgePort) {
      setValidation('Proxy and bridge ports must differ.');
      return;
    }
    const selectedPreset = presets.find((preset) => preset.name === input.preset);
    const validatesCustomUpstream = !selectedPreset?.bridge || Boolean(input.baseUrl);
    if (validatesCustomUpstream && Boolean(input.baseUrl) !== Boolean(input.authTokenEnv)) {
      setValidation('Custom upstream configuration requires both a base URL and token environment variable.');
      return;
    }
    if (input.baseUrl) {
      let endpoint: URL;
      try {
        endpoint = new URL(input.baseUrl);
      } catch {
        setValidation('Base URL must be a valid absolute HTTP or HTTPS URL.');
        return;
      }
      if (!['http:', 'https:'].includes(endpoint.protocol) || endpoint.username || endpoint.password) {
        setValidation('Base URL must use HTTP or HTTPS and cannot include credentials.');
        return;
      }
      if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(input.authTokenEnv)) {
        setValidation('Token environment variable must use letters, numbers, and underscores and cannot begin with a number.');
        return;
      }
    }

    setSaving(true);
    let result;
    try {
      result = editing && name
        ? await api.updateDialect(name, input, snapshot.revision)
        : await api.createDialect(input, dialectRevision || undefined);
    } catch (caught) {
      reportError(caught);
      setSaving(false);
      return;
    }
    notify(`${result.data.dialect.name} ${editing ? 'updated' : 'created'}.`);
    navigate(`/dialects/${encodeURIComponent(result.data.dialect.name)}`);
    setSaving(false);
    await refreshAfterMutation();
  }

  if (loading) return <LoadingState label="Loading dialect configuration" />;
  if (loadError) return <ErrorState message={loadError} onRetry={() => void refresh().catch((caught) => reportError(caught))} />;

  const portHint = editing ? 'Leave empty to preserve the current proxy port.' : 'Leave empty for automatic allocation.';
  const bridgePortHint = editing ? 'Leave empty to preserve the current managed bridge port.' : 'Only managed bridge presets allocate this automatically.';

  return (
    <form className="space-y-8" onSubmit={submit} noValidate>
      <PageHeader
        eyebrow={editing ? 'Edit dialect' : 'New dialect'}
        title={editing ? `Configure ${name}` : 'Create an isolated dialect'}
        description="Start from a provider preset or define a custom model route. Changes to an existing dialect stop its current runtime before the configuration is replaced."
        actions={<Button variant="outline" asChild><Link to={editing && name ? `/dialects/${encodeURIComponent(name)}` : '/'}><ArrowLeft />Cancel</Link></Button>}
      />

      {validation && <div className="flex items-start gap-3 rounded-lg border border-destructive/25 bg-destructive/5 p-4 text-sm text-destructive" role="alert"><Info className="mt-0.5 size-4 shrink-0" />{validation}</div>}

      <div className="grid gap-6 xl:grid-cols-[minmax(0,1fr)_22rem]">
        <div className="space-y-6">
          <Card>
            <CardHeader><CardTitle>Identity and preset</CardTitle><CardDescription>The name identifies this configuration. Launch it with <code className="font-mono">cc-dialect run &lt;name&gt;</code>, or install an optional shim later.</CardDescription></CardHeader>
            <CardContent className="grid gap-5 md:grid-cols-2">
              <div className="grid gap-2"><Label htmlFor="name">Dialect name</Label><Input id="name" value={input.name} disabled={editing} autoComplete="off" placeholder="my-dialect" onChange={(event) => update('name', event.target.value)} aria-describedby="name-hint" /><p id="name-hint" className="text-xs text-muted-foreground">Lowercase letters, numbers, <code>-</code>, and <code>_</code>.</p></div>
              <div className="grid gap-2"><Label htmlFor="preset">Provider preset</Label><select id="preset" className="h-10 rounded-md border border-input bg-background/80 px-3 text-sm shadow-sm" value={input.preset} onChange={(event) => choosePreset(event.target.value)}><option value="">Custom configuration</option>{presets.map((preset) => <option key={preset.name} value={preset.name}>{preset.name} · {preset.provider}</option>)}</select><p className="text-xs text-muted-foreground">Preset values remain editable below.</p></div>
            </CardContent>
          </Card>

          <Card>
            <CardHeader><CardTitle>Model routing</CardTitle><CardDescription>Override model identifiers by workload tier, or leave tier fields empty to use the primary model.</CardDescription></CardHeader>
            <CardContent className="grid gap-5 md:grid-cols-2">
              <div className="grid gap-2 md:col-span-2"><Label htmlFor="model">Primary model</Label><Input id="model" value={input.model} onChange={(event) => update('model', event.target.value)} placeholder="Provider model identifier" /></div>
              <div className="grid gap-2"><Label htmlFor="subagent-model">Subagent model</Label><Input id="subagent-model" value={input.subagentModel} onChange={(event) => update('subagentModel', event.target.value)} placeholder="Defaults to primary" /></div>
              <div className="grid gap-2"><Label htmlFor="opus-model">Opus alias</Label><Input id="opus-model" value={input.opusModel} onChange={(event) => update('opusModel', event.target.value)} placeholder="Defaults to primary" /></div>
              <div className="grid gap-2"><Label htmlFor="sonnet-model">Sonnet alias</Label><Input id="sonnet-model" value={input.sonnetModel} onChange={(event) => update('sonnetModel', event.target.value)} placeholder="Defaults to primary" /></div>
              <div className="grid gap-2"><Label htmlFor="haiku-model">Haiku alias</Label><Input id="haiku-model" value={input.haikuModel} onChange={(event) => update('haikuModel', event.target.value)} placeholder="Defaults to primary" /></div>
            </CardContent>
          </Card>

          <Card>
            <CardHeader><CardTitle>Endpoint and authentication</CardTitle><CardDescription>Custom upstreams require both fields. Only the environment-variable name is stored; secret values are never returned by the dashboard API.</CardDescription></CardHeader>
            <CardContent className="grid gap-5 md:grid-cols-2">
              <div className="grid gap-2 md:col-span-2"><Label htmlFor="base-url">Base URL</Label><Input id="base-url" type="url" value={input.baseUrl} onChange={(event) => update('baseUrl', event.target.value)} placeholder="https://api.example.com/v1" /></div>
              <div className="grid gap-2"><Label htmlFor="auth-token-env">Token environment variable</Label><Input id="auth-token-env" className="font-mono" value={input.authTokenEnv} onChange={(event) => update('authTokenEnv', event.target.value)} placeholder="PROVIDER_API_KEY" /></div>
              <div className="grid gap-2"><Label htmlFor="effort-level">Effort level</Label><select id="effort-level" className="h-10 rounded-md border border-input bg-background/80 px-3 text-sm shadow-sm" value={input.effortLevel} onChange={(event) => update('effortLevel', event.target.value)}>{['auto', 'low', 'medium', 'high', 'xhigh', 'max'].map((level) => <option key={level} value={level}>{level}</option>)}</select></div>
            </CardContent>
          </Card>

          <details className="group rounded-lg border bg-card shadow-soft" open={editing}>
            <summary className="flex cursor-pointer list-none items-center gap-3 p-5 font-semibold sm:p-6"><SlidersHorizontal className="size-4 text-primary" />Advanced runtime settings<span className="ml-auto text-xs font-normal text-muted-foreground group-open:hidden">Show</span><span className="ml-auto hidden text-xs font-normal text-muted-foreground group-open:inline">Hide</span></summary>
            <div className="grid gap-5 border-t p-5 sm:p-6 md:grid-cols-3">
              <NumberField id="concurrency" label="Concurrency" value={input.concurrency} min={1} onChange={(value) => update('concurrency', value)} hint="Maximum concurrent tool uses." />
              <NumberField id="port" label="Proxy port" value={input.port} min={1024} max={65535} onChange={(value) => update('port', value)} hint={portHint} />
              <NumberField id="bridge-port" label="Bridge port" value={input.bridgePort} min={1024} max={65535} onChange={(value) => update('bridgePort', value)} hint={bridgePortHint} />
            </div>
          </details>
        </div>

        <aside className="space-y-4">
          <Card className="xl:sticky xl:top-24">
            <CardHeader><CardTitle className="text-base">Capabilities</CardTitle><CardDescription>Enable optional request features for this dialect.</CardDescription></CardHeader>
            <CardContent className="space-y-5">
              <div className="flex items-start justify-between gap-4"><div><Label htmlFor="effort">Effort controls</Label><p className="mt-1 text-xs leading-relaxed text-muted-foreground">Forward supported effort settings to the provider.</p></div><Switch id="effort" checked={input.effort} onCheckedChange={(value) => update('effort', value)} /></div>
              <div className="flex items-start justify-between gap-4"><div><Label htmlFor="tool-search">Tool search</Label><p className="mt-1 text-xs leading-relaxed text-muted-foreground">Allow supported tool discovery behavior.</p></div><Switch id="tool-search" checked={input.toolSearch} onCheckedChange={(value) => update('toolSearch', value)} /></div>
              <Button className="w-full" type="submit" disabled={saving}>{saving ? <LoaderCircle className="animate-spin" /> : <Save />}{saving ? 'Saving…' : editing ? 'Save changes' : 'Create dialect'}</Button>
              {editing && <p className="text-xs leading-relaxed text-muted-foreground">Saving replaces the public configuration fields shown in this form while preserving the dialect’s private identity.</p>}
            </CardContent>
          </Card>
        </aside>
      </div>
    </form>
  );
}
