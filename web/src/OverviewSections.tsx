import type { AssetSummaries, AssetSummary, CertificateEntry, CertificatesReport, ExpiryReport } from './contracts';
import { assetStatus, categoryLabels } from './assetState';
import { summarizeExpiry } from './expiry';
import { pageURL, type Scope } from './scope';

export function AssetTotals({ report, scope }: { report: AssetSummaries; scope: Scope }) {
  const rows = report.rows.filter(row => !row.archived);
  return <><p>在用资产 {rows.length}</p><div className="overview-metrics">{Object.entries(categoryLabels).map(([category, name]) => <a key={category} href={pageURL('assets', { ...scope, key: '', issue: '', category: category as keyof typeof categoryLabels })}>{name} <strong>{rows.filter(row => assetStatus(row) === category).length}</strong></a>)}</div><p className="muted">每个在用资产只计一类。暂无检查不是已通过；这是资产数，不是问题数或健康评分。</p></>;
}

export function ExpiryOverview({ report, records, kind }: { report: ExpiryReport; records: AssetSummary[]; kind: string }) {
  const rows = records.filter(row => row.kind === kind && !row.archived);
  const summary = summarizeExpiry(report, rows);
  const keys = new Set(rows.map(row => row.key));
  const entries = report.entries.filter(entry => keys.has(entry.kind + '/' + entry.name)).sort((a, b) => a.days_left - b.days_left || a.name.localeCompare(b.name));
  return <><p className="evidence">计算日期：{report.as_of} · 登记值，非实时探测。</p>
    {kind === 'Domain' ? <p>已登记 {summary.registered} / 共 {summary.total}</p> : <p>已登记 {summary.registered} 项；另有 {summary.missing} 个未登记。</p>}
    {!summary.total ? <p className="empty">当前类型没有在用资产，不代表检查通过。</p> : kind === 'Domain' && summary.missing > 0 ? <p className="notice">{summary.registered ? `有 ${summary.missing} 个未登记到期日，无法判断` : '均未登记到期日，无法判断；不代表安全/正常。'}</p> : null}
    {summary.registered > 0 && summary.due === 0 && <p>30 天内没有到期项。</p>}
    {!entries.length && kind !== 'Domain' && <p className="muted">尚无登记到期信息，不代表安全/正常。</p>}
    <ul className="items">{entries.map(entry => <li key={entry.kind + '/' + entry.name}><strong>{entry.name}</strong> · <time>{entry.expires_at}</time><p>{entry.days_left < 0 ? `已过期 ${-entry.days_left} 天` : entry.days_left === 0 ? '今天到期' : `剩余 ${entry.days_left} 天`} · {entry.days_left < 0 ? '已过期' : entry.days_left <= 30 ? '30 天内到期' : '未临近'}</p></li>)}</ul>
  </>;
}

const certificateStates: Record<string, string> = { stale: '数据已过期', failed: '采集失败', running: '采集未结束', future: '观测时间在未来', unverified: '证据资格未确认', uncollected: '未采集' };
function CertificateRow({ entry }: { entry: CertificateEntry }) {
  const known = entry.data_state === 'fresh' && entry.days_left !== null && ['expired', 'today', 'expiring', 'not_due'].includes(entry.expiry_state);
  return <li><strong>{entry.host}:{entry.port}</strong>
    <p>{known ? `${entry.days_left! < 0 ? `已过期 ${-entry.days_left!} 天` : entry.days_left === 0 ? '今天到期' : `剩余 ${entry.days_left} 天`} · ${entry.expiry_state === 'not_due' ? '未临近' : entry.expiry_state === 'expired' ? '已过期' : '30 天内到期'}` : `到期状态未知 · ${certificateStates[entry.data_state] ?? '数据资格未知'}`}</p>
    {entry.not_after && <p className="evidence">{known ? '到期时间' : '历史到期值'}：<time>{entry.not_after}</time></p>}
    <p className="evidence">信任验证（保存结果）：{entry.verification ?? '未采集'}；采集时间：{entry.observed_at ?? '未采集'}</p>
    {entry.apps.length > 1 && <p className="notice">候选应用：{entry.apps.join('、')}，不能确定唯一归属。</p>}
  </li>;
}
export function CertificateOverview({ report, scope }: { report: CertificatesReport; scope: Scope }) {
  const ownership: Record<string, string> = { no_app: '无应用', unassociated: '未关联应用', ambiguous: '归属有歧义' };
  return <>{!report.has_snapshots && <p className="notice">没有 tls 探测快照 · 不代表证书正常</p>}
    {report.groups.map((group, index) => <article key={group.ownership + '/' + group.app + '/' + index}><h3>{group.ownership === 'app' && group.app ? <a href={pageURL('assets', { ...scope, key: group.app, issue: '' })}>{group.app}</a> : ownership[group.ownership] ?? '归属未确认'}</h3><ul className="items">{group.certificates.map((entry, index) => <CertificateRow key={entry.host + '/' + entry.port + '/' + index} entry={entry} />)}</ul></article>)}
    {report.uncollected.length > 0 && <article><h3>未采集</h3><ul className="items">{report.uncollected.map((entry, index) => <CertificateRow key={entry.host + '/' + entry.port + '/' + index} entry={entry} />)}</ul></article>}
  </>;
}

export function ServerOverview({ rows, scope }: { rows: AssetSummary[]; scope: Scope }) {
  const servers = rows.filter(row => row.kind === 'Server' && !row.archived);
  return <><p className="muted">台账登记值 · 非实时</p>{servers.length ? <div className="overview-table"><table><thead><tr><th scope="col">服务器</th><th scope="col">CPU</th><th scope="col">内存</th><th scope="col">磁盘</th><th scope="col">系统</th></tr></thead><tbody>{servers.map(row => <tr key={row.key}><th scope="row"><a href={pageURL('assets', { ...scope, key: row.key, issue: '' })}>{row.name}</a></th>{(['cpu', 'memory', 'disk', 'os'] as const).map(key => <td key={key}>{row.hardware?.[key] || '未登记'}</td>)}</tr>)}</tbody></table></div> : <p className="empty">没有在用服务器登记，不代表运行正常。</p>}</>;
}

export function FreshnessOverview({ report }: { report: AssetSummaries }) {
  const instances = new Map(report.rows.filter(row => !row.archived).flatMap(row => row.execution).map(entry => [entry.plugin + '/' + entry.instance, entry]));
  const values = [...instances.values()];
  const times = values.map(entry => entry.observed_at).filter((time): time is string => !!time && Number.isFinite(Date.parse(time))).sort((a, b) => Date.parse(a) - Date.parse(b));
  const missing = values.length - times.length;
  const asOf = Date.parse(report.as_of);
  const future = times.filter(time => Date.parse(time) > asOf).length;
  const stale = times.length > 0 && report.max_age_days !== null && asOf - Date.parse(times[0]) > report.max_age_days * 86400000;
  return <><p className="muted">采集由外部任务执行，本页不会触发。这里统计在用资产检查引用的实例，保存时间不代表本轮执行成功。</p>
    {times.length ? <><p>数据采集时间 · 最旧：<time>{times[0]}</time></p><p>数据采集时间 · 最新：<time>{times.at(-1)}</time></p></> : <p className="notice">没有保存的观测时间，不代表正常。</p>}
    {missing > 0 && <p className="notice">{missing} 个实例缺少观测时间，资格未知。</p>}{future > 0 && <p className="notice">{future} 个观测时间在未来，资格未知。</p>}{stale && <p className="notice">最旧观测已超过证据有效期。</p>}{report.max_age_days === null && <p className="notice">证据有效期未设置，不能判断新鲜覆盖。</p>}
  </>;
}
