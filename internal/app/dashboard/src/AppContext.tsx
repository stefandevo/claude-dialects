import * as AlertDialog from '@radix-ui/react-alert-dialog';
import { AlertTriangle, CheckCircle2, X } from 'lucide-react';
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { ApiError, dashboardApi } from './api';
import { Button } from './components/ui/button';
import type {
  BootstrapResponse,
  CursorRuntimeStatus,
  DialectView,
  NativeLauncherView,
} from './types';

interface ToastMessage {
  id: number;
  tone: 'success' | 'warning' | 'error';
  message: string;
}

type RefreshHandler = () => Promise<void> | void;

interface DashboardContextValue {
  bootstrap?: BootstrapResponse;
  presets: DialectView[];
  dialects: DialectView[];
  dialectRevision: string;
  launchers: NativeLauncherView[];
  launcherRevision: string;
  cursor?: CursorRuntimeStatus;
  loading: boolean;
  refreshing: boolean;
  error?: string;
  refresh: () => Promise<void>;
  refreshAfterMutation: () => Promise<void>;
  registerRefreshHandler: (handler: RefreshHandler) => () => void;
  reportError: (error: unknown, onConflict?: RefreshHandler) => void;
  notify: (message: string) => void;
  warn: (message: string) => void;
  api: typeof dashboardApi;
}

const DashboardContext = createContext<DashboardContextValue | null>(null);

function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : 'An unexpected error occurred.';
}

export function DashboardProvider({ children }: { children: ReactNode }) {
  const [bootstrap, setBootstrap] = useState<BootstrapResponse>();
  const [presets, setPresets] = useState<DialectView[]>([]);
  const [dialectSnapshot, setDialectSnapshot] = useState<{ dialects: DialectView[]; revision: string }>({ dialects: [], revision: '' });
  const [launcherSnapshot, setLauncherSnapshot] = useState<{ launchers: NativeLauncherView[]; revision: string }>({ launchers: [], revision: '' });
  const [cursor, setCursor] = useState<CursorRuntimeStatus>();
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState<string>();
  const [conflictOpen, setConflictOpen] = useState(false);
  const conflictHandler = useRef<RefreshHandler | undefined>(undefined);
  const refreshHandlers = useRef(new Set<RefreshHandler>());
  const refreshPromise = useRef<Promise<void> | null>(null);
  const [toasts, setToasts] = useState<ToastMessage[]>([]);
  const toastID = useRef(0);
  const initialized = useRef(false);

  const pushToast = useCallback((tone: ToastMessage['tone'], message: string, duration: number) => {
    const id = ++toastID.current;
    setToasts((current) => [...current, { id, tone, message }]);
    window.setTimeout(() => setToasts((current) => current.filter((toast) => toast.id !== id)), duration);
  }, []);

  const notify = useCallback((message: string) => pushToast('success', message, 4200), [pushToast]);
  const warn = useCallback((message: string) => pushToast('warning', message, 6500), [pushToast]);
  const pushError = useCallback((message: string) => pushToast('error', message, 6000), [pushToast]);

  const registerRefreshHandler = useCallback((handler: RefreshHandler) => {
    refreshHandlers.current.add(handler);
    return () => refreshHandlers.current.delete(handler);
  }, []);

  const refresh = useCallback(() => {
    if (refreshPromise.current) return refreshPromise.current;

    setRefreshing(true);
    const request = (async () => {
      const bootstrapResult = await dashboardApi.bootstrap();
      const [dialectResult, launcherResult, cursorResult, presetResult] = await Promise.all([
        dashboardApi.listDialects(),
        dashboardApi.listLaunchers(),
        dashboardApi.cursorStatus(),
        dashboardApi.presets(),
      ]);
      await Promise.all(Array.from(refreshHandlers.current, (handler) => handler()));
      setBootstrap(bootstrapResult);
      setDialectSnapshot({
        dialects: dialectResult.data.dialects,
        revision: dialectResult.revision || dialectResult.data.revision,
      });
      setLauncherSnapshot({
        launchers: launcherResult.data.launchers,
        revision: launcherResult.revision || launcherResult.data.revision,
      });
      setCursor(cursorResult);
      setPresets(presetResult);
      setError(undefined);
    })();
    const guarded = request.finally(() => {
      if (refreshPromise.current === guarded) {
        refreshPromise.current = null;
        setRefreshing(false);
      }
    });
    refreshPromise.current = guarded;
    return guarded;
  }, []);

  const refreshAfterMutation = useCallback(async () => {
    try {
      await refresh();
    } catch {
      warn('The change succeeded, but the latest dashboard data could not be reloaded. Use Refresh before making another change.');
    }
  }, [refresh, warn]);

  useEffect(() => {
    if (initialized.current) return;
    initialized.current = true;
    void refresh()
      .catch((caught) => setError(errorMessage(caught)))
      .finally(() => setLoading(false));
  }, [refresh]);

  const reportError = useCallback((caught: unknown, onConflict?: RefreshHandler) => {
    if (caught instanceof ApiError && caught.isConflict) {
      conflictHandler.current = onConflict;
      setConflictOpen(true);
      return;
    }
    pushError(errorMessage(caught));
  }, [pushError]);

  const value = useMemo<DashboardContextValue>(() => ({
    bootstrap,
    presets,
    dialects: dialectSnapshot.dialects,
    dialectRevision: dialectSnapshot.revision,
    launchers: launcherSnapshot.launchers,
    launcherRevision: launcherSnapshot.revision,
    cursor,
    loading,
    refreshing,
    error,
    refresh,
    refreshAfterMutation,
    registerRefreshHandler,
    reportError,
    notify,
    warn,
    api: dashboardApi,
  }), [bootstrap, presets, dialectSnapshot, launcherSnapshot, cursor, loading, refreshing, error, refresh, refreshAfterMutation, registerRefreshHandler, reportError, notify, warn]);

  return (
    <DashboardContext.Provider value={value}>
      {children}
      <div className="pointer-events-none fixed inset-x-4 bottom-4 z-[60] flex flex-col items-end gap-2" aria-live="polite" aria-atomic="true">
        {toasts.map((toast) => (
          <div key={toast.id} className="pointer-events-auto flex w-full max-w-sm items-start gap-3 rounded-lg border bg-popover p-4 text-sm shadow-2xl">
            {toast.tone === 'success' ? <CheckCircle2 className="mt-0.5 size-5 shrink-0 text-success" /> : <AlertTriangle className={`mt-0.5 size-5 shrink-0 ${toast.tone === 'warning' ? 'text-warning' : 'text-destructive'}`} />}
            <p className="flex-1 leading-relaxed">{toast.message}</p>
            <button className="rounded-sm text-muted-foreground hover:text-foreground" onClick={() => setToasts((current) => current.filter((item) => item.id !== toast.id))} aria-label="Dismiss notification"><X className="size-4" /></button>
          </div>
        ))}
      </div>
      <AlertDialog.Root open={conflictOpen} onOpenChange={setConflictOpen}>
        <AlertDialog.Portal>
          <AlertDialog.Overlay className="fixed inset-0 z-50 bg-foreground/45 backdrop-blur-sm" />
          <AlertDialog.Content className="fixed left-1/2 top-1/2 z-50 w-[calc(100%-2rem)] max-w-md -translate-x-1/2 -translate-y-1/2 rounded-lg border bg-background p-6 shadow-2xl">
            <AlertDialog.Title className="text-lg font-semibold">Configuration changed</AlertDialog.Title>
            <AlertDialog.Description className="mt-2 text-sm leading-relaxed text-muted-foreground">
              Another process updated the configuration after this page loaded. Reload the latest values before trying again.
            </AlertDialog.Description>
            <div className="mt-6 flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
              <AlertDialog.Cancel asChild><Button variant="outline">Keep this view</Button></AlertDialog.Cancel>
              <AlertDialog.Action asChild>
                <Button onClick={() => {
                  const handler = conflictHandler.current;
                  conflictHandler.current = undefined;
                  void Promise.resolve(handler ? handler() : refresh()).catch((caught) => pushError(errorMessage(caught)));
                }}>Reload latest values</Button>
              </AlertDialog.Action>
            </div>
          </AlertDialog.Content>
        </AlertDialog.Portal>
      </AlertDialog.Root>
    </DashboardContext.Provider>
  );
}

export function useDashboard() {
  const context = useContext(DashboardContext);
  if (!context) throw new Error('useDashboard must be used inside DashboardProvider.');
  return context;
}
