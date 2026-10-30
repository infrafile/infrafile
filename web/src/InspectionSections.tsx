import { useState, type ReactNode } from 'react';
import { Badge, SegmentedControl } from '@radix-ui/themes';
import type { AssetContext, Inspection } from './contracts';
import { isAssetKey, pageURL, type Scope } from './scope';

const labels: Record<string, string> = { unknown: '未知', checked: '已检', not_applicable: '不适用', confirmed: '已确认', waived: '已豁免', failed: '执行失败', stale: '证据过期', success: '执行成功', open: '未恢复', recovered: '已恢复', missing: '缺少证据', pending: '待核实' };
export function StatusBadge({ value }: { value: string }) {
  return <Badge color={['confirmed', 'failed'].includes(value) ? 'red' : ['unknown', 'stale', 'missing', 'pending'].includes(value) ? 'amber' : value === 'checked' || value === 'success' ? 'green' : 'gray'}>{labels[value] ?? value}</Badge>;
}
export function Raw({ value, title = '展开原始数据', open = false }: { value: unknown; title?: string; open?: boolean }) {
  return <details className="raw" open={open}><summary>{title}</summary><pre>{JSON.stringify(value, null, 2)}</pre></details>;
}
export function InspectionSections({ inspection: report, scope, context, children }: { inspection: Inspection; scope: Scope; context?: AssetContext; children?: ReactNode }) {
  const [status, setStatus] = useState('all');
  return <>
    <section aria-labelledby="issues-title"><h2 id="issues-title">风险与待核实</h2>
      <p className="counts"><span>确认风险 <strong>{report.risk.confirmed}</strong></span><span>未知检查 <strong>{report.coverage.unknown}</strong></span><span>未知发现 <strong>{report.risk.unknown}</strong></span><span>已豁免 <strong>{report.risk.waived}</strong></span></p>
      <p className="muted">未知检查与未知发现可能指向同一问题，分别计数。已豁免不表示检查通过。</p>
      <ul className="items">{report.risk.findings.map((finding, index) => <li key={index}>
        <h3>{finding.rule} · {finding.target}</h3><StatusBadge value={finding.assessment} /> {finding.waiver && <StatusBadge value="waived" />}
        <p>{finding.message}</p><p className="muted">来源：{finding.file || '无文件'}</p>
        {finding.waiver && <p>豁免：{finding.waiver.reason} · {finding.waiver.until || '未设置到期'} · {finding.waiver.source}</p>}
        <Raw value={finding.evidence} title="展开问题证据" />
      </li>)}</ul>
      {!report.risk.findings.length && <p className="empty">当前范围没有保存的风险发现，仍需核对覆盖缺口。</p>}
    </section>
    <section><h2>覆盖与证据</h2><p className="counts"><span>应检 <strong>{report.coverage.expected}</strong></span><span>已检 <strong>{report.coverage.checked}</strong></span><span>未知 <strong>{report.coverage.unknown}</strong></span><span>不适用 <strong>{report.coverage.not_applicable}</strong></span></p>
      <p className="muted">证据有效期：{report.max_age_days === null ? '不限制' : report.max_age_days + ' 天'}。仅展示已保存证据；读取不会发起巡检。</p>
      <SegmentedControl.Root aria-label="检查状态" value={status} onValueChange={setStatus} size="1">
        {[['all', '全部状态'], ['unknown', '仅未知'], ['checked', '仅已检'], ['not_applicable', '仅不适用']].map(([value, text]) => <SegmentedControl.Item key={value} value={value}>{text}</SegmentedControl.Item>)}
      </SegmentedControl.Root>
      {context && <h3>最近保存的观测</h3>}
      <ul className="items">{report.coverage.checks.filter(check => status === 'all' || check.status === status).map(check => <li key={check.id}>
        <h3>{check.rule} · {check.target}</h3><StatusBadge value={check.status} /><p>{check.message}</p>
        <p className="muted">{check.aspect} · {check.code} · {check.plugin}/{check.instance}/{check.service}</p>
        {check.evidence.map((evidence, index) => <p className="evidence" key={index}>来源：{evidence.file || '无快照'} · 观测：<time>{evidence.observed_at || '未保存时间'}</time> · TTL：{evidence.max_age_days === null ? '不限制' : evidence.max_age_days + ' 天'}</p>)}
        {context?.observations.filter(observation => observation.check === check.id).map((observation, index) => <div key={index}>
          <p className="evidence">匹配：{observation.match} · {observation.file} · <time>{observation.observed_at || '未保存时间'}</time></p>
          {observation.match === 'matched' ? <Raw value={observation.values} title="展开观测事实" /> : <p>没有可归属于此资产的匹配观测事实。</p>}
        </div>)}
        {check.not_applicable && <p>不适用：{check.not_applicable.reason}；负责人 {check.not_applicable.owner}；复核 {check.not_applicable.review_by}</p>}
        <a href={pageURL(isAssetKey(check.target) ? 'assets' : 'inspections', { ...scope, key: isAssetKey(check.target) ? check.target : '' })}>{isAssetKey(check.target) ? '查看资产' : '查看全部巡检'}</a>
      </li>)}</ul>
      {!report.coverage.checks.some(check => status === 'all' || check.status === status) && <p className="empty">此范围没有匹配的检查项。</p>}
    </section>
    <section><h2>执行资格</h2><ul className="items">{report.execution.map((execution, index) => <li key={index}>
      <h3>{execution.plugin}/{execution.instance}</h3><StatusBadge value={execution.status} /> <span>[{execution.code}]</span>
      <p>最近尝试：<time>{execution.attempted_at || '未记录'}</time> · 保存观测：<time>{execution.observed_at || '未记录'}</time></p>
      <p className="evidence">快照：{execution.snapshot_file || '无'} · 回执：{execution.receipt_file || '无'}</p>
    </li>)}</ul>{!report.execution.length && <p className="empty">此范围没有执行记录。</p>}</section>
    {children}
    <section><h2>已保存的问题历史</h2>{!report.history ? <p>尚未保存问题历史。缺少历史不表示问题已经恢复；此页面不会自动推进历史。</p> : <>
      <p className="evidence">最近显式检查：<time>{report.history.checked_at}</time>；变化游标 {report.history.last_sequence}。历史与本次检查结果分开保存。</p>
      <ul className="items">{report.history.issues.map((state, index) => <li key={index}>
        <h3>{state.identity.key.rule} · {state.identity.key.target}</h3><StatusBadge value={state.lifecycle} /> <StatusBadge value={state.assessment} />
        <p>首次发现：{state.first_seen}；最近检查：{state.last_checked}；发生 {state.occurrences} 次</p>
        {state.last_confirmed && <p>最近确认：{state.last_confirmed}</p>}{state.recovered_at && <p>最近恢复：{state.recovered_at}</p>}
        {state.waiver && <p>豁免：{state.waiver.reason} · {state.waiver.until || '未设置到期'}</p>}
        <a href={pageURL('changes', { ...scope, key: state.identity.key.target, issue: state.identity.fingerprint + '/' + state.identity.scope_hash })}>查看此问题的变化</a>
      </li>)}</ul>{!report.history.issues.length && <p>最近一次显式检查没有保存的问题；仍需核对当前覆盖缺口。</p>}
    </>}</section>
    <section><h2>说明与原始结果</h2>{report.notices.map((notice, index) => <p key={index}>{notice.target} · {notice.code}：{notice.message} {notice.file}</p>)}<Raw value={context ?? report} /></section>
  </>;
}
