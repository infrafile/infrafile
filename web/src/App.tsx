import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react';
import { SegmentedControl, Theme } from '@radix-ui/themes';
import { AssetsPage } from './AssetsPage';
import { InspectionsPage } from './InspectionsPage';
import { ChangesPage } from './ChangesPage';
import { OverviewPage } from './OverviewPage';
import { pageURL, readScope, type Scope, type WorkbenchPage } from './scope';

type Appearance = 'system' | 'light' | 'dark';
function SessionControls() {
  const host = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const controls = document.getElementById('session-controls');
    if (!controls || !host.current) return;
    const parent = controls.parentElement!;
    controls.hidden = false; host.current.append(controls);
    return () => { controls.hidden = true; parent.append(controls); };
  }, []);
  return <div className="session-host" ref={host} onKeyDown={event => {
    const details = host.current?.querySelector('details');
    if (event.key === 'Escape' && details?.open) { details.open = false; details.querySelector('summary')?.focus(); }
  }} />;
}
function currentScope(): { scope: Scope; error: string } {
  try { return { scope: readScope(location.hash), error: '' }; }
  catch (error) { return { scope: { key: '', days: 7, issue: '' }, error: (error as Error).message }; }
}
export function App() {
  const [selection, setSelection] = useState(currentScope), [expired, setExpired] = useState(false);
  const [appearance, setAppearance] = useState<Appearance>(() => {
    try { const value = localStorage.getItem('infrafile-appearance'); return value === 'light' || value === 'dark' ? value : 'system'; } catch { return 'system'; }
  });
  const [systemDark, setSystemDark] = useState(() => matchMedia('(prefers-color-scheme: dark)').matches);
  const page: WorkbenchPage = location.pathname === '/inspections' ? 'inspections' : location.pathname === '/changes' ? 'changes' : location.pathname === '/assets' || location.pathname === '/' && !!selection.scope.key ? 'assets' : 'overview';
  const onScopeChange = useCallback((scope: Scope) => { history.replaceState(null, '', pageURL(page, scope)); setSelection({ scope, error: '' }); }, [page]);
  const onUnauthorized = useCallback(() => setExpired(true), []);
  useEffect(() => {
    const update = () => setSelection(currentScope());
    addEventListener('hashchange', update); addEventListener('popstate', update);
    return () => { removeEventListener('hashchange', update); removeEventListener('popstate', update); };
  }, []);
  useEffect(() => {
    const media = matchMedia('(prefers-color-scheme: dark)'), update = () => setSystemDark(media.matches);
    media.addEventListener('change', update); return () => media.removeEventListener('change', update);
  }, []);
  const theme = appearance === 'system' ? systemDark ? 'dark' : 'light' : appearance;
  useEffect(() => { document.documentElement.dataset.theme = theme; }, [theme]);
  const sidebar = (children: ReactNode) => <aside className="sidebar"><div className="brand">infrafile<span>本地工作台</span></div>
    <p className="repo-name" title={document.getElementById('root')?.dataset.repo}>{document.getElementById('root')?.dataset.repo}</p>
    <nav aria-label="工作台">{([['overview', '总览'], ['assets', '资产'], ['inspections', '巡检'], ['changes', '变更']] as const).map(([target, name]) => <a key={target} href={pageURL(target, selection.scope)} aria-current={target === page ? 'page' : undefined}><span className="nav-symbol" aria-hidden="true">{target === 'overview' ? '▤' : target === 'assets' ? '▦' : target === 'inspections' ? '☑' : '↺'}</span>{name}</a>)}</nav>
    {children}<div className="sidebar-bottom"><p className="eyebrow">外观</p><SegmentedControl.Root size="1" aria-label="外观" value={appearance} onValueChange={value => { setAppearance(value as Appearance); try { localStorage.setItem('infrafile-appearance', value); } catch { /* Appearance remains usable without storage. */ } }}>
      <SegmentedControl.Item value="system">系统</SegmentedControl.Item><SegmentedControl.Item value="light">浅色</SegmentedControl.Item><SegmentedControl.Item value="dark">深色</SegmentedControl.Item>
    </SegmentedControl.Root><SessionControls /></div></aside>;
  return <Theme appearance={theme} accentColor="blue" grayColor="slate" radius="medium" scaling="100%">
    <a className="skip" href="#workspace" onClick={event => { event.preventDefault(); document.getElementById('workspace')?.focus(); }}>跳到内容</a>
    {expired ? <main className="disconnected" role="alert"><h1>会话已失效</h1><p>请重新连接工作台。</p><a href="/">重新连接工作台</a></main> : selection.error ? <div className="app-shell">{sidebar(null)}<main id="workspace" role="alert" className="page"><h1>无法读取范围</h1><p>{selection.error}</p><a href={'/' + page}>重置范围</a></main></div> : page === 'assets' ? <AssetsPage scope={selection.scope} onScopeChange={onScopeChange} onUnauthorized={onUnauthorized} renderSidebar={sidebar} /> : <div className="app-shell">{sidebar(null)}<main id="workspace" className="page" tabIndex={-1}>{page === 'overview' ? <OverviewPage scope={selection.scope} onScopeChange={onScopeChange} onUnauthorized={onUnauthorized} /> : page === 'inspections' ? <InspectionsPage scope={selection.scope} onScopeChange={onScopeChange} onUnauthorized={onUnauthorized} /> : <ChangesPage scope={selection.scope} onScopeChange={onScopeChange} onUnauthorized={onUnauthorized} />}</main></div>}
  </Theme>;
}
