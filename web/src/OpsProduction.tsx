import { opsName, opsStatus, opsTime, type OpsApplication } from './ops';

const titles: Record<string, string> = { readiness: '应用就绪', traffic: '接流量与排空', replicas: '可用副本', recovery: '完整恢复', release: '发布与回退' };

export function OpsProduction({ app, host }: { app: OpsApplication; host?: string }) {
  if (!app.production) return <section aria-label="生产控制证据"><h3>生产控制证据</h3><p className="notice">本代未包含生产准入证据；启停和发布条件未核实。</p></section>;
  return <section className="ops-production" aria-label="生产控制证据"><h3>生产控制证据</h3>
    <p className="muted">以下是分项证据与尚需证明的条件。局部样本通过不代表准入通过；本区不授予操作权限。{host && `当前只显示 ${opsName(host)} 的样本。`}</p>
    {app.production.boundary && <p className="notice">{app.production.boundary}</p>}
    {app.production.gates.map(gate => {
      const rows = gate.observations.filter(row => !host || row.hosts.includes(host));
      return <details key={gate.id} open><summary><strong>{titles[gate.id] ?? gate.id}</strong><span className="ops-sub">准入尚未验证 · 局部样本 {rows.length} 项</span></summary>
        <p className="muted">尚需证明：{gate.requirements.join('；')}</p>
        {!rows.length && <p className="muted">此范围没有已登记的核实样本，状态未知。</p>}
        <ul>{rows.map(row => <li key={row.id}><p>{row.summary}</p><span className={`ops-status ops-${row.evidence.status.toLowerCase()}`}>{row.evidence.status === 'PASS' ? '局部样本通过' : opsStatus[row.evidence.status] ?? '未知'}</span>
          <span className="ops-stamp">观测：{opsTime(row.evidence.observed_at)}</span>
          {row.evidence.freshness === 'future' && <span className="ops-sub">观测时间在未来，当前未知</span>}
          {row.evidence.freshness === 'unverified' && <span className="ops-sub">当前读取失败，证据未复核</span>}
          {row.evidence.status === 'STALE' && <span className="ops-sub">历史局部结果：{opsStatus[row.evidence.observed_status ?? 'UNKNOWN']}；当前不能沿用</span>}
          <p className="muted">范围：{row.hosts.map(opsName).join('、')} · 来源：{row.reference}</p><details><summary>来源摘要</summary><code className="ops-criterion-id">{row.reference_sha256}</code></details>
        </li>)}</ul>
      </details>;
    })}
  </section>;
}
