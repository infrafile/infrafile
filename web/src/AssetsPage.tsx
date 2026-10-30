import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { Badge, Button, SegmentedControl, TextField } from '@radix-ui/themes';
import { ApiError, query } from './api';
import type { AssetDirectoryEntry, AssetSummaries, ExpiryReport, ListResult } from './contracts';
import { assetChanges, assetStatus, categoryLabels, defaultFilters, filterAssets, filterDirectory, selectAsset, type AssetFilters } from './assetState';
import { AssetDetail } from './AssetDetail';
import { ScopeBar } from './ScopeBar';
import { isAssetKey, type PageProps } from './scope';
import { useQuery, type QueryState } from './useQuery';
import { loadExpiry } from './expiry';
import { AutoReadStatus } from './AutoReadStatus';

type DirectoryRows = { mode: 'complete'; summary: AssetSummaries } | { mode: 'directory'; rows: AssetDirectoryEntry[] };
type Directory = DirectoryRows & { expiry: QueryState<ExpiryReport> };
const directoryRows = (value: Directory) => value.mode === 'complete' ? value.summary.rows : value.rows;
const directoryChanges = (previous: Directory, next: Directory) => assetChanges(directoryRows(previous), directoryRows(next)) || (JSON.stringify(previous.expiry) === JSON.stringify(next.expiry) ? 0 : 1);
const kinds = [['', '全部资产'], ['Server', '服务器'], ['Domain', '域名'], ['Resource', '资源'], ['App', '应用'], ['Repo', '代码仓'], ['Account', '账号']];
export function AssetsPage({ scope, onScopeChange, onUnauthorized, renderSidebar }: PageProps & { renderSidebar: (children: ReactNode) => ReactNode }) {
  const [filters, setFilters] = useState<AssetFilters>(() => ({ ...defaultFilters, status: scope.category ?? 'all' })), [refresh, setRefresh] = useState(0);
  const [announcement, setAnnouncement] = useState(''), [detailOpen, setDetailOpen] = useState(!!scope.key);
  const list = useRef<HTMLDivElement>(null), detail = useRef<HTMLElement>(null), cause = useRef<'initial' | 'filter' | 'link'>(scope.key ? 'link' : 'initial');
  const load = useCallback(async (signal: AbortSignal, automatic = false): Promise<Directory> => {
    const selection = filters.kind ? { kind: filters.kind } : {};
    let directory: DirectoryRows;
    try { directory = { mode: 'complete', summary: await query<AssetSummaries>('asset-summaries', { ...selection, max_age_days: scope.days }, signal) }; }
    catch (error) {
      if (automatic || signal.aborted || error instanceof ApiError && error.status === 401) throw error;
      directory = { mode: 'directory', rows: (await query<ListResult>('list', selection, signal)).records };
    }
    let expiry: QueryState<ExpiryReport>;
    try { expiry = { status: 'ready', data: await loadExpiry(signal) }; }
    catch (error) {
      if (automatic || signal.aborted || error instanceof ApiError && error.status === 401) throw error;
      expiry = { status: 'error', error: error instanceof Error ? error : Error('到期信息读取失败。') };
    }
    return { ...directory, expiry };
  }, [filters.kind, scope.days]);
  const state = useQuery(`${filters.kind}/${scope.days}/${refresh}`, load, onUnauthorized, { automatic: true, changes: directoryChanges });
  const data = state.status === 'ready' ? state.data : undefined;
  const rows = useMemo(() => !data ? [] : data.mode === 'complete' ? filterAssets(data.summary.rows, filters) : filterDirectory(data.rows, filters), [data, filters]);
  const summaries = data?.mode === 'complete' ? new Map(data.summary.rows.map(row => [row.key, row])) : undefined;
  useEffect(() => { setFilters(current => ({ ...current, status: scope.category ?? 'all' })); }, [scope.category]);
  useEffect(() => {
    if (!data) return;
    const selected = selectAsset(rows, scope.key, cause.current);
    cause.current = 'link';
    if (selected.changed) {
      onScopeChange({ ...scope, key: selected.key, issue: '' });
      setAnnouncement(selected.key ? '已选择 ' + selected.key : '没有符合筛选条件的资产。');
    }
  }, [data, rows, scope, onScopeChange]);
  const updateFilters = (next: Partial<AssetFilters>) => {
    cause.current = 'filter'; setFilters(current => ({ ...current, ...next }));
    if (next.status) onScopeChange({ ...scope, category: next.status === 'all' ? undefined : next.status });
  };
  const navigate = (key: string, enter = true) => {
    cause.current = 'link'; onScopeChange({ ...scope, key, issue: '' });
    if (enter) enterDetail();
  };
  const outside = !!data && !!scope.key && !rows.some(row => row.key === scope.key);
  const enterDetail = () => { setDetailOpen(true); requestAnimationFrame(() => detail.current?.focus()); };
  return <div className="app-shell">
    {renderSidebar(<div className="kind-nav" aria-label="资产类型"><p className="eyebrow">资产类型</p>{kinds.map(([kind, name]) => <button key={kind} className={filters.kind === kind ? 'active' : ''} aria-pressed={filters.kind === kind} onClick={() => updateFilters({ kind })}><span>{name}</span><span className="kind-code">{kind}</span></button>)}</div>)}
    <main id="workspace" tabIndex={-1} className={'assets-workspace' + (detailOpen ? ' show-detail' : '')}>
      <div className="asset-list"><div className="list-toolbar"><div className="title-line"><h1>资产目录</h1><Badge color="gray">{rows.length}</Badge></div>
        <TextField.Root type="search" aria-label="搜索资产" placeholder="名称、主键或标签" value={filters.search} onChange={event => updateFilters({ search: event.target.value })} />
        <SegmentedControl.Root aria-label="归档状态" size="1" value={filters.archived} onValueChange={archived => updateFilters({ archived: archived as AssetFilters['archived'] })}>
          <SegmentedControl.Item value="active">在用</SegmentedControl.Item><SegmentedControl.Item value="archived">已归档</SegmentedControl.Item><SegmentedControl.Item value="all">全部</SegmentedControl.Item>
        </SegmentedControl.Root>
        <SegmentedControl.Root aria-label="资产状态" size="1" disabled={!summaries} value={filters.status} onValueChange={status => updateFilters({ status: status as AssetFilters['status'] })}>
          {[['all', '全部'], ['risk', '有风险'], ['needs_attention', '需处理'], ['no_checks', '暂无检查'], ['passed', '通过']].map(([value, name]) => <SegmentedControl.Item key={value} value={value}>{name}</SegmentedControl.Item>)}
        </SegmentedControl.Root>
        <p role="status" className="sr-only">{announcement}</p>
        <div className="list-auto-read"><AutoReadStatus state={state.auto} /></div>
      </div>
      {state.status === 'loading' && <p className="empty" role="status">正在读取资产摘要…</p>}
      {state.status === 'error' && <p role="alert" className="error">{state.error.message}</p>}
      {data?.mode === 'directory' && <div className="notice" role="alert"><p>摘要读取失败，仅显示资产目录。状态未读取。</p><p>请缩小资产类型后重新读取，或使用本地 CLI 核对。</p></div>}
      <div className="asset-rows" role="listbox" aria-label="资产" ref={list}>{rows.map((row, index) => {
        const summary = summaries?.get(row.key), status = summary && assetStatus(summary);
        return <button role="option" aria-selected={row.key === scope.key} tabIndex={row.key === scope.key || !rows.some(row => row.key === scope.key) && index === 0 ? 0 : -1} key={row.key}
          onClick={() => navigate(row.key)} onKeyDown={event => {
            if (event.key === 'Enter') { event.preventDefault(); if (row.key !== scope.key) navigate(row.key); else enterDetail(); }
            if (event.key === 'ArrowDown' || event.key === 'ArrowUp' || event.key === 'Home' || event.key === 'End') {
              event.preventDefault(); const next = event.key === 'Home' ? 0 : event.key === 'End' ? rows.length - 1 : Math.max(0, Math.min(rows.length - 1, index + (event.key === 'ArrowDown' ? 1 : -1)));
              navigate(rows[next].key, false); list.current?.querySelectorAll<HTMLButtonElement>('[role=option]')[next]?.focus();
            }
          }}>
          <span className="asset-row-top"><strong>{row.name}</strong>{summary ? <Badge color={status === 'risk' ? 'red' : status === 'passed' ? 'green' : status === 'no_checks' ? 'gray' : 'amber'}>{categoryLabels[status!]}</Badge> : <Badge color="gray">状态未读取</Badge>}</span>
          <span className="asset-key">{row.key}{row.archived ? ' · 已归档' : ''}</span>
          {summary && <><span className="asset-reason">{summary.reason.message}</span><span className="asset-counts">风险 {summary.risk.confirmed} · 未知检查 {summary.coverage.unknown} · 未知发现 {summary.risk.unknown}</span></>}
        </button>;
      })}</div>
      {data && !rows.length && <p className="empty">没有符合筛选条件的资产。</p>}
      <div className="list-footer">{data?.mode === 'complete' ? <>摘要读取时间<br /><time>{data.summary.as_of}</time></> : '资产目录'}<br />↑ ↓ 选择 · Enter 查看详情</div>
      </div>
      <section className="asset-detail" ref={detail} tabIndex={-1} aria-label="资产详情">
        <div className="detail-toolbar"><Button className="back-button" size="1" variant="soft" onClick={() => { setDetailOpen(false); requestAnimationFrame(() => list.current?.querySelector<HTMLButtonElement>('[aria-selected=true]')?.focus({ preventScroll: true })); }}>返回资产列表</Button><ScopeBar scope={scope} onScopeChange={onScopeChange} onRefresh={() => setRefresh(value => value + 1)} /></div>
        <AutoReadStatus state={state.auto} />
        {outside && <div className="notice"><span>此资产不在当前筛选范围内。</span> <Button size="1" variant="soft" onClick={() => { cause.current = 'link'; setFilters({ ...defaultFilters, archived: 'all' }); onScopeChange({ ...scope, category: undefined }); }}>清除筛选</Button></div>}
        {scope.key && isAssetKey(scope.key) ? data ? <AssetDetail key={`${scope.key}/${scope.days}/${refresh}`} assetKey={scope.key} days={scope.days} expiry={data.expiry} summaryReadAt={data.mode === 'complete' ? data.summary.as_of : null} directory={directoryRows(data)} onNavigate={navigate} onUnauthorized={onUnauthorized} /> : <p className="empty">正在读取资产目录…</p> : <p className="empty">{scope.key ? '此目标不是资产，请前往巡检或变更查看。' : '选择资产查看风险与证据。'}</p>}
      </section>
    </main>
  </div>;
}
