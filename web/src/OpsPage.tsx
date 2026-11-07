import { useCallback, useState } from 'react';
import { query } from './api';
import { AutoReadStatus } from './AutoReadStatus';
import { useQuery } from './useQuery';
import type { PageProps } from './scope';
import { opsMatches, opsName, opsReason, opsStatus, opsTime, unverifiedOps, type OpsDeployment, type OpsEvidence, type OpsReport } from './ops';
import './ops.css';
import { OpsReadChooser } from './OpsReadPanel';
import { OpsActionChooser } from './OpsActionPanel';
import { OpsOperationCatalog, OpsOperationScope } from './OpsOperationCatalog';
import { OpsRefreshPanel } from './OpsRefreshPanel';
import { OpsRecoveryPanel } from './OpsRecoveryPanel';
import { OpsProduction } from './OpsProduction';

const categoryNames: Record<string, string> = { business: '业务', ops: '运维', infra: '基础设施' };
const ignoreUnauthorized = () => {};

export function OpsState({ value, pass = '通过' }: { value: OpsEvidence; pass?: string }) {
  return <span className="ops-evidence"><span className={`ops-status ops-${value.status.toLowerCase()}`}>{value.status === 'PASS' ? pass : opsStatus[value.status] ?? '未知'}</span><span className="ops-stamp">{opsTime(value.observed_at)}</span>{value.freshness === 'future' && <span className="ops-stamp">观测时间在未来</span>}{value.status === 'STALE' && value.observed_status && <span className="ops-stamp">历史结果：{opsStatus[value.observed_status] ?? '未知'}</span>}</span>;
}

const criterionNames: Record<string, string> = { fleet_systemd: '服务运行', fleet_container_running: '容器运行', fleet_container_health: '容器健康检查', process_running: '进程检查', http_from_container: '已有容器内HTTP判据', public_https: '公网入口判据', es_cluster_health: 'ES就绪判据', systemd_timer: '定时器调度', backup_meta: '备份完成记录', backup_space: '备份容量保护', kuma_heartbeat: 'Kuma任务心跳', nas_snapshot: 'NAS快照进度', public_observer_log: '已有观察结果', manual: '手工复核' };
const layerNames: Record<string, string> = { runtime: '运行', readiness: '就绪', network_path: '入口与链路', schedule: '调度', backup_completion: '任务完成', capacity: '容量保护', progress: '任务进度', unverified: '手工边界' };
const workloadNames: Record<string, string> = { service: '常驻服务', worker: '后台任务', scheduled: '定时任务（无需常驻）', control: '控制组件' };

export function OpsCriteria({ deployment }: { deployment: OpsDeployment }) {
  const rows = deployment.criteria;
  if (!rows?.length) return <p className="muted">已配置 {deployment.checks.length} 项判据；{deployment.checks.length ? '本代未包含判据详情，不能据此判断通过。' : '尚无匹配判据。'}</p>;
  const groups = new Map<string, typeof rows>();
  for (const row of rows) { const key = row.component + '/' + row.workload_mode; groups.set(key, [...(groups.get(key) ?? []), row]); }
  return <section className="ops-criteria" aria-label="已有判据与观测"><h4>已有判据与观测</h4><p className="muted">已配置 {deployment.checks.length} 项。配置与观测分开；本页不执行这些判据，不汇总为完整业务健康。</p>
    {[...groups].map(([key, checks]) => <details key={key}><summary>{checks[0].component}<span className="ops-sub">{workloadNames[checks[0].workload_mode] ?? checks[0].workload_mode} · {checks.length}项 · 通过 {checks.filter(c => c.evidence.status === 'PASS').length} / 未知 {checks.filter(c => c.evidence.status === 'UNKNOWN').length} / 过期 {checks.filter(c => c.evidence.status === 'STALE').length} / 失败 {checks.filter(c => c.evidence.status === 'FAIL').length}</span></summary><ul>{checks.map(check => <li key={check.id}>
      <strong>{criterionNames[check.kind] ?? check.kind}</strong><span className="ops-sub">证据层：{layerNames[check.layer] ?? check.layer} · {check.data_mode === 'stateful' ? '有状态组件' : '无状态组件'}</span><code className="ops-criterion-id">{check.id}</code>{check.selector && <p className="muted">明确对象：{check.selector}</p>}<OpsState value={check.evidence} pass="已保存证据通过" /><p className="ops-criterion-reason">{opsReason[check.evidence.reason] ?? check.evidence.reason}</p><p className="muted">{check.evidence.source === 'fleet' ? '观测来源：主机快照' : '仅有判据配置，尚无本轮执行证据'} · {opsName(check.observer)}</p>
    </li>)}</ul></details>)}
  </section>;
}

type Selection = { kind: 'server' | 'app' | 'resource' | 'monitor' | 'deployment'; id: string };
export function OpsContent({ report, onUnauthorized = ignoreUnauthorized }: { report: OpsReport; onUnauthorized?: () => void }) {
  const [tab, setTab] = useState('servers'), [search, setSearch] = useState(''), [filter, setFilter] = useState('all');
  const [selected, setSelected] = useState<Selection | null>(null);
  const statusMatch = (status: string) => filter === 'all' || status === filter;
  const selectKey = (key: string) => setSelected({ kind: key.startsWith('Server/') ? 'server' : key.startsWith('App/') ? 'app' : 'resource', id: key });
  const server = report.servers.find(row => selected?.kind === 'server' && row.key === selected.id);
  const selectedDeployment = report.deployments.find(row => selected?.kind === 'deployment' && row.id === selected.id);
  const app = report.applications.find(row => selectedDeployment ? row.key === selectedDeployment.application : selected?.kind === 'app' && row.key === selected.id);
  const resource = report.resources.find(row => selected?.kind === 'resource' && row.key === selected.id);
  const monitor = report.monitors.find(row => selected?.kind === 'monitor' && String(row.id) === selected.id);
  const detailKey = server?.key ?? app?.key ?? resource?.key;
  const related = detailKey ? report.monitors.filter(row => row.targets.includes(detailKey) || app && row.targets.some(key => report.deployments.some(dep => dep.application === app.key && dep.resources.includes(key)))) : [];
  const instances = report.deployments.filter(dep => selectedDeployment ? dep.id === selectedDeployment.id : server ? dep.host === server.key : app ? dep.application === app.key : resource ? dep.resources.includes(resource.key) : false);
  const operationRows = report.operations?.filter(row => server ? row.kind === 'server' && row.object === server.key : app ? row.kind === 'deployment' && row.object === app.key && (!selectedDeployment || row.deployment === selectedDeployment.id) : false) ?? [];
  const scopeKey = selectedDeployment?.id ?? detailKey;
  const inScope = (cap: { id: string; target: string; related_keys: string[] }) => selectedDeployment ? cap.target === selectedDeployment.host && cap.related_keys.includes(selectedDeployment.application) : cap.target === detailKey || !!detailKey?.startsWith('App/') && cap.related_keys.includes(detailKey);
  const active = report.monitors.filter(row => row.active === true).length;
  const paused = report.monitors.filter(row => row.active === false).length;
  const unconfirmed = report.monitors.filter(row => row.active === null).length;
  const unknown = report.deployments.filter(row => row.runtime.status === 'UNKNOWN').length;
  const choose = (kind: Selection['kind'], id: string, name: string) => <button type="button" className="ops-view" aria-label={`查看 ${name}`} onClick={() => setSelected({ kind, id })}>查看</button>;
  const appState = (key: string) => {
    const rows = report.deployments.filter(d => d.application === key);
    return rows.some(d => d.runtime.status === 'FAIL') ? 'FAIL' : rows.some(d => d.runtime.status === 'STALE') ? 'STALE' : rows.some(d => d.runtime.status === 'UNKNOWN') || !rows.length ? 'UNKNOWN' : 'PASS';
  };
  const references = (keys: string[]) => keys.length ? <span className="ops-links">{keys.map(key => <button type="button" className="ops-link" key={key} onClick={() => selectKey(key)}>{opsName(key)}</button>)}</span> : <span className="muted">未关联</span>;
  const monitors = (rows: typeof report.monitors) => rows.length ? <ul className="ops-monitor-list">{rows.map(row => <li key={row.id}><button type="button" className="ops-link" onClick={() => setSelected({ kind: 'monitor', id: String(row.id) })}>#{row.id} {row.name}</button><OpsState value={row.state} /></li>)}</ul> : <p className="muted">没有明确关联的 Kuma 检查，不代表此对象健康。</p>;
  return <>
    <div className="ops-source" role="status"><strong>{report.alignment === 'matched' ? '台账与证据包来源一致' : report.alignment === 'changed' ? '权威来源已有变化 · 以下是冻结观测' : '尚未核对当前权威来源'}</strong><span>证据包生成：{opsTime(report.captured_at)} · 时效判定：{opsTime(report.evaluated_at)}（北京时间）</span><span>每条记录保留自己的观测时间；来源一致不代表所有服务健康。</span></div>
    <div className="ops-totals" aria-label="运维范围">{[['机器', report.summary.servers], ['应用', report.summary.applications], ['部署', report.summary.deployments], ['监控', report.monitors.length]].map(([name, count]) => <div className="ops-total" key={name}><span>{name}</span><strong>{count}</strong></div>)}</div>
    <p className="ops-coverage">运行对象已定位 {report.summary.runtime_identified}/{report.summary.deployments} · 运行证据未知 {unknown} 项 · Kuma 保存配置：{active} 启用 / {paused} 暂停{unconfirmed > 0 && ` / ${unconfirmed} 未确认`}<span>运行、就绪、业务健康分别判断。</span></p>
    {report.collection_errors.map(code => <p key={code} className="muted ops-note">{opsReason[code] ?? '部分来源尚未确认，请查看对应证据。'}</p>)}
    <div className={`ops-workspace${selected ? ' ops-with-detail' : ''}`}>
      <section className="ops-directory" aria-label="机器与应用目录">
        <div className="ops-tabs" role="group" aria-label="查看范围">{[['servers', '机器'], ['apps', '应用'], ['monitors', '监控'], ['operations', '操作目录']].map(([value, name]) => <button type="button" aria-pressed={tab === value} key={value} onClick={() => { setTab(value); setFilter('all'); }}>{name}</button>)}</div>
        <div className="ops-filters"><label>搜索<input type="search" value={search} onChange={e => setSearch(e.target.value)} placeholder="名称、角色或部署机器" /></label>{tab !== 'operations' && <label>状态<select aria-label="状态" value={filter} onChange={e => setFilter(e.target.value)}>{[['all', '全部状态'], ['UNKNOWN', '未知'], ['STALE', '已过期'], ['FAIL', '失败'], ['PAUSED', '已暂停'], ['PASS', '有有效证据']].map(([value, title]) => <option key={value} value={value}>{title}</option>)}</select></label>}</div>
        {tab === 'servers' && <div className="overview-table"><table><caption className="ops-caption">主机快照与登记规格 · 使用率见选定机器的按需采集</caption><thead><tr><th scope="col">机器</th><th scope="col">登记规格</th><th scope="col">快照</th><th scope="col">应用</th><th scope="col">详情</th></tr></thead><tbody>{report.servers.filter(row => opsMatches(search, row.name, row.role, ...row.applications) && statusMatch(row.snapshot.status)).map(row => <tr key={row.key} className={selected?.id === row.key ? 'ops-selected' : ''}><th scope="row">{row.name}<span className="ops-sub">{row.role || '职责未登记'}</span>{row.notice && <span className="ops-hold">有待验收或保留事项</span>}</th><td>{row.hardware.cpu || '未登记'}<span className="ops-sub">{row.hardware.memory || '内存未登记'}</span></td><td><OpsState value={row.snapshot} pass="快照可用" /></td><td>{row.applications.length}</td><td>{choose('server', row.key, row.name)}</td></tr>)}</tbody></table></div>}
        {tab === 'apps' && <div className="overview-table"><table><caption className="ops-caption">全部在用应用 · 运行证据不等于完整业务健康</caption><thead><tr><th scope="col">应用</th><th scope="col">部署</th><th scope="col">运行证据</th><th scope="col">业务健康</th><th scope="col">生产控制</th><th scope="col">详情</th></tr></thead><tbody>{report.applications.filter(row => opsMatches(search, row.name, ...report.deployments.filter(d => d.application === row.key).map(d => d.host)) && statusMatch(appState(row.key))).map(row => <tr key={row.key} className={selected?.id === row.key ? 'ops-selected' : ''}><th scope="row">{row.name}<span className="ops-sub">{categoryNames[row.category] ?? row.category}</span></th><td>{row.deployments.length}</td><td><span className={`ops-status ops-${appState(row.key).toLowerCase()}`}>{appState(row.key) === 'PASS' ? '有运行证据' : opsStatus[appState(row.key)]}</span><span className="ops-sub">{report.deployments.filter(d => d.application === row.key && d.runtime.status === 'PASS').length}/{row.deployments.length} 项有效</span></td><td><span className="ops-status ops-unknown">完整健康未验</span></td><td><span className="ops-status ops-unknown">{row.production ? '五类准入未验' : '准入证据未包含'}</span></td><td>{choose('app', row.key, row.name)}</td></tr>)}</tbody></table></div>}
        {tab === 'monitors' && <div className="overview-table"><table><caption className="ops-caption">检查结果与启用状态独立展示 · 暂停不代表宕机</caption><thead><tr><th scope="col">监控</th><th scope="col">保存配置</th><th scope="col">结果</th><th scope="col">关联</th><th scope="col">详情</th></tr></thead><tbody>{report.monitors.filter(row => opsMatches(search, row.name, String(row.id)) && statusMatch(row.state.status)).map(row => <tr key={row.id}><th scope="row">{row.name}<span className="ops-sub">#{row.id} · {row.type}</span></th><td>{row.active === null ? '未确认' : row.active ? '启用' : '暂停'}{row.configuration.status !== 'PASS' && <span className="ops-sub">配置观测{row.configuration.status === 'STALE' ? '已过期' : '未确认'}</span>}</td><td><OpsState value={row.state} /></td><td>{row.targets.length ? `${row.targets.length} 个对象` : '未关联现役对象'}</td><td>{choose('monitor', String(row.id), row.name)}</td></tr>)}</tbody></table></div>}
        {tab === 'operations' && <OpsOperationCatalog report={report} search={search} onSelect={row => setSelected({ kind: row.kind === 'server' ? 'server' : 'deployment', id: row.kind === 'server' ? row.object : row.deployment! })} />}
        <p className="muted ops-note">筛选只改变显示范围。没有符合项时，可清空搜索或选择全部状态。</p>
      </section>
      {selected && <aside className="ops-detail" aria-label="对象详情"><div className="ops-detail-head"><h2>{selectedDeployment ? `${opsName(selectedDeployment.application)} · ${opsName(selectedDeployment.host)}` : server?.name ?? app?.name ?? resource?.name ?? monitor?.name ?? '对象未在此代结果中'}</h2><button type="button" onClick={() => setSelected(null)}>关闭详情</button></div>
        {(server?.notice || app?.notice) && <p className="notice">{server?.notice || app?.notice}</p>}
        {detailKey && <p><a href={`/assets#key=${encodeURIComponent(detailKey)}`}>查看同代台账记录</a></p>}
        {detailKey && <OpsReadChooser key={'read/' + scopeKey} capabilities={report.read_capabilities?.filter(inScope) ?? []} onUnauthorized={onUnauthorized} />}
        {detailKey && <OpsActionChooser key={'action/' + scopeKey} capabilities={report.action_capabilities?.filter(inScope) ?? []} onUnauthorized={onUnauthorized} />}
        {operationRows.map(row => <OpsOperationScope key={row.id} row={row} report={report} />)}
        {app && <OpsProduction app={app} host={selectedDeployment?.host} />}
        {server && <><h3>主机证据</h3><OpsState value={server.snapshot} pass="快照可用" /><p className="muted">此处为保存快照与登记规格；CPU/内存使用率见按需采集，短时样本不代表持续负载。</p><p>{server.hardware.os || '系统未登记'}</p>{server.disks.map(disk => <p key={disk.mount}>{disk.mount} 已用 {disk.percent}% · 可用 {(disk.available / 1024 ** 3).toFixed(1)} GiB<span className="ops-sub">磁盘观测：{opsTime(server.snapshot.observed_at)}</span></p>)}<h3>部署应用</h3>{references(server.applications)}</>}
        {resource && <><p>资源类型：{resource.type}</p><h3>所在机器</h3>{references(resource.hosts)}</>}
        {instances.map(dep => <article className="ops-instance" key={dep.id}><h3>{opsName(dep.application)} · {opsName(dep.host)}</h3><p className="muted">{dep.kind} · {dep.selector || (dep.criteria?.some(c => c.workload_mode === 'scheduled') ? '调度与任务结果分别查看' : dep.kind === 'nginx-proxy' ? '兼容代理，以入口判据为准' : '主运行映射尚未定位，已有判据见下方')}</p>{dep.notice && <p className="notice">{dep.notice}</p>}<dl className="ops-layers"><dt>运行</dt><dd><OpsState value={dep.runtime} pass="运行证据通过" />{dep.runtime.status !== 'PASS' && <span className="ops-sub">{opsReason[dep.runtime.reason] ?? '请核对观测时间与运行对象。'}</span>}</dd><dt>就绪</dt><dd><OpsState value={dep.readiness} /></dd><dt>业务健康</dt><dd><OpsState value={dep.health} /><span className="ops-sub">{opsReason[dep.health.reason] ?? '尚未核实'}</span></dd></dl><OpsCriteria deployment={dep} /><h4>数据与组件依赖</h4>{references(dep.resources)}</article>)}
        {detailKey && <><h3>关联监控</h3>{monitors(related)}</>}
        {monitor && <><p>{monitor.type} · {monitor.interval === null ? '窗口未知' : `检查窗口/间隔 ${monitor.interval} 秒`}</p><h3>保存的检查结果</h3><OpsState value={monitor.state} /><h3>配置观测时效</h3><OpsState value={monitor.configuration} pass="配置记录可用" />{monitor.association_reason && <p className="notice">{monitor.association_reason}</p>}<h3>明确关联</h3>{references(monitor.targets)}</>}
        <p className="ops-readonly">保存观测与按需读取 · 受控执行仅限已显式绑定的单个任务</p>
      </aside>}
    </div>
  </>;
}

export function OpsPage({ onUnauthorized }: Pick<PageProps, 'onUnauthorized'>) {
  const [revision, setRevision] = useState(0);
  const load = useCallback((signal: AbortSignal) => query<OpsReport>('ops-overview', {}, signal), []);
  // Stable rows/selection do not reorder; apply age changes immediately instead of retaining old green badges.
  const state = useQuery(`ops/${revision}`, load, onUnauthorized, { automatic: true, changes: () => 0 });
  const updated = useCallback(() => setRevision(value => value + 1), []);
  return <><header className="ops-heading"><p className="eyebrow">{state.status === 'ready' && state.data.action_capabilities?.length ? '运维总览' : '只读运维'}</p><h1>所有机器与应用</h1><p className="muted">依据当前台账与已保存运行证据。重新读取不会触发远端采集或自然任务验收。</p><button type="button" onClick={() => setRevision(value => value + 1)}>重新读取保存结果</button></header><AutoReadStatus state={state.auto} />
    <OpsRefreshPanel availability={state.status === 'ready' ? state.data.refresh : undefined} onUpdated={updated} onUnauthorized={onUnauthorized} />
    <OpsRecoveryPanel rows={state.status === 'ready' ? state.data.recoveries : undefined} onUnauthorized={onUnauthorized} />
    {state.status === 'loading' && <p role="status">正在核对台账与证据包…</p>}
    {state.status === 'error' && <p className="error" role="alert">无法读取已绑定的运维结果。请核对证据包、来源与生成代，旧结果不作为当前证据。</p>}
    {state.status === 'ready' && (state.data.enabled ? <><div className={state.auto.error ? 'ops-read-failed' : ''}>{state.auto.error && <p className="notice" role="alert">本次重新读取失败，以下为上次结果；当前时效未核实。</p>}<OpsContent key={state.data.captured_at} report={state.auto.error ? unverifiedOps(state.data) : state.data} onUnauthorized={onUnauthorized} /></div></> : <p className="notice">当前工作台未绑定运维证据包。现有资产、巡检与变更页面保持可用。</p>)}
  </>;
}
