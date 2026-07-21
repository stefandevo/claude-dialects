import * as Tooltip from '@radix-ui/react-tooltip';
import { Boxes, Command, Gauge, Menu, Moon, Plus, RefreshCw, Sun, TerminalSquare } from 'lucide-react';
import { useEffect, useState } from 'react';
import { NavLink, Outlet } from 'react-router-dom';
import { useDashboard } from '../AppContext';
import { cn } from '../utils';
import { Button } from './ui/button';
import { Dialog, DialogContent, DialogTitle, DialogTrigger } from './ui/dialog';

const navigation = [
  { to: '/', label: 'Overview', icon: Gauge, end: true },
  { to: '/dialects/new', label: 'New dialect', icon: Plus },
  { to: '/launchers', label: 'Native launchers', icon: Command },
  { to: '/runtime', label: 'Cursor runtime', icon: Boxes },
];

function initialTheme() {
  const stored = window.localStorage.getItem('dashboard-theme');
  if (stored === 'dark' || stored === 'light') return stored;
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}

export function Layout() {
  const { bootstrap, refresh, loading, refreshing, reportError } = useDashboard();
  const [theme, setTheme] = useState<'light' | 'dark'>(initialTheme);
  const [mobileOpen, setMobileOpen] = useState(false);

  useEffect(() => {
    document.documentElement.classList.toggle('dark', theme === 'dark');
    window.localStorage.setItem('dashboard-theme', theme);
  }, [theme]);

  const sidebar = (
    <>
      <div className="flex h-20 items-center gap-3 border-b px-5">
        <div className="grid size-10 place-items-center rounded-lg bg-primary text-primary-foreground shadow-xs"><TerminalSquare className="size-5" /></div>
        <div>
          <p className="font-bold tracking-tight">cc-dialect</p>
          <p className="text-xs text-muted-foreground">Local control plane</p>
        </div>
      </div>
      <nav className="flex-1 space-y-1 p-3" aria-label="Primary navigation">
        {navigation.map(({ to, label, icon: Icon, end }) => (
          <NavLink
            key={to}
            to={to}
            end={end}
            onClick={() => setMobileOpen(false)}
            className={({ isActive }) => cn(
              'flex items-center gap-3 rounded-md px-3 py-2.5 text-sm font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground',
              isActive && 'bg-accent text-accent-foreground',
            )}
          >
            <Icon className="size-4" aria-hidden="true" />{label}
          </NavLink>
        ))}
      </nav>
      <div className="border-t p-4">
        <div className="rounded-md bg-muted p-3">
          <p className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Dashboard version</p>
          <p className="mt-1 truncate font-mono text-xs" title={bootstrap?.version}>{bootstrap?.version || 'Loading…'}</p>
        </div>
      </div>
    </>
  );

  function runRefresh() {
    void refresh().catch((caught) => reportError(caught));
  }

  return (
    <Tooltip.Provider delayDuration={250}>
      <div className="min-h-screen lg:grid lg:grid-cols-[16rem_1fr]">
        <aside className="fixed inset-y-0 left-0 z-30 hidden w-64 flex-col border-r bg-card/90 backdrop-blur-xl lg:flex">{sidebar}</aside>
        <div className="lg:col-start-2">
          <header className="sticky top-0 z-20 flex h-16 items-center justify-between border-b bg-background/85 px-4 backdrop-blur-xl sm:px-6 lg:px-8">
            <div className="flex items-center gap-3">
              <Dialog open={mobileOpen} onOpenChange={setMobileOpen}>
                <DialogTrigger asChild>
                  <Button
                    variant="ghost"
                    size="icon"
                    className="lg:hidden"
                    aria-label="Open navigation"
                    aria-expanded={mobileOpen}
                    aria-controls="mobile-navigation"
                  ><Menu /></Button>
                </DialogTrigger>
                <DialogContent
                  id="mobile-navigation"
                  className="left-0 top-0 flex h-dvh w-[min(18rem,85vw)] max-w-none translate-x-0 translate-y-0 flex-col gap-0 rounded-none border-y-0 border-l-0 p-0 lg:hidden"
                >
                  <DialogTitle className="sr-only">Dashboard navigation</DialogTitle>
                  {sidebar}
                </DialogContent>
              </Dialog>
              <div className="hidden items-center gap-2 text-sm text-muted-foreground sm:flex"><span className="size-2 rounded-full bg-success" />Loopback-only dashboard</div>
            </div>
            <div className="flex items-center gap-1">
              <Tooltip.Root>
                <Tooltip.Trigger asChild><Button variant="ghost" size="icon" onClick={runRefresh} disabled={loading || refreshing} aria-label="Refresh dashboard"><RefreshCw className={cn(refreshing && 'animate-spin')} /></Button></Tooltip.Trigger>
                <Tooltip.Portal><Tooltip.Content className="rounded-md bg-foreground px-2.5 py-1.5 text-xs text-background shadow-lg" sideOffset={8}>{refreshing ? 'Refreshing dashboard' : 'Refresh dashboard'}</Tooltip.Content></Tooltip.Portal>
              </Tooltip.Root>
              <Tooltip.Root>
                <Tooltip.Trigger asChild><Button variant="ghost" size="icon" onClick={() => setTheme(theme === 'dark' ? 'light' : 'dark')} aria-label={`Use ${theme === 'dark' ? 'light' : 'dark'} theme`}>{theme === 'dark' ? <Sun /> : <Moon />}</Button></Tooltip.Trigger>
                <Tooltip.Portal><Tooltip.Content className="rounded-md bg-foreground px-2.5 py-1.5 text-xs text-background shadow-lg" sideOffset={8}>Use {theme === 'dark' ? 'light' : 'dark'} theme</Tooltip.Content></Tooltip.Portal>
              </Tooltip.Root>
            </div>
          </header>
          <main className="relative overflow-hidden">
            <div className="surface-grid pointer-events-none absolute inset-x-0 top-0 h-72 opacity-50" aria-hidden="true" />
            <div className="relative mx-auto w-full max-w-[90rem] p-4 sm:p-6 lg:p-8"><Outlet /></div>
          </main>
        </div>
      </div>
    </Tooltip.Provider>
  );
}
