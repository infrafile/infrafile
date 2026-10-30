import { Badge } from '@radix-ui/themes';
import type { AssetDirectoryEntry, ExpiryReport } from './contracts';
import type { QueryState } from './useQuery';
import { expiryKinds, summarizeExpiry } from './expiry';

export function ExpirySection({ asset, records, state }: { asset: AssetDirectoryEntry; records?: AssetDirectoryEntry[]; state: QueryState<ExpiryReport> }) {
  if (!expiryKinds.includes(asset.kind)) return null;
  if (asset.archived) return <section aria-label="登记到期"><h2>登记到期</h2><p className="muted">已归档，不纳入到期检查。</p></section>;
  if (state.status === 'error') return <section aria-label="登记到期"><h2>登记到期</h2><p role="alert" className="error">到期信息读取失败。{state.error.message}</p></section>;
  if (state.status !== 'ready') return <section aria-label="登记到期"><h2>登记到期</h2><p role="status">正在读取到期信息…</p></section>;
  const report = state.data;
  const entry = report.entries.find(entry => entry.kind + '/' + entry.name === asset.key);
  const summary = records && summarizeExpiry(report, records.filter(row => row.kind === asset.kind));
  return <section aria-label="登记到期"><h2>登记到期 <Badge color="gray">登记值</Badge></h2>
    <p className="evidence">按 <time>{report.as_of}</time> 的日历日计算 · 来自台账 spec.expires_at，非实时探测</p>
    {entry ? <p className="expiry-value"><time dateTime={entry.expires_at}>{entry.expires_at}</time>
      <span>{entry.days_left < 0 ? `已过期 ${-entry.days_left} 天` : entry.days_left === 0 ? '今天到期' : `剩余 ${entry.days_left} 天`}</span>
      <Badge color={entry.days_left < 0 ? 'red' : entry.days_left <= 30 ? 'amber' : 'gray'}>{entry.days_left < 0 ? '已过期' : entry.days_left <= 30 ? '30 天内到期' : '未临近'}</Badge>
    </p> : <p className="notice">未登记到期日，无法判断；不代表安全/正常。</p>}
    {summary && summary.total > 0 && <div className={summary.missing ? 'notice' : 'muted'}>
      <p>当前类型（在用）：已登记 {summary.registered} / 共 {summary.total}。</p>
      {summary.registered > 0 && <p>{summary.due === 0 ? '30 天内没有到期项' : `${summary.due} 项已过期或在 30 天内到期`}。</p>}
      {summary.missing > 0 && <p>{summary.registered === 0 ? '均未登记到期日，无法判断，不代表安全/正常。' : `有 ${summary.missing} 个未登记到期日，无法判断。`}</p>}
    </div>}
  </section>;
}
