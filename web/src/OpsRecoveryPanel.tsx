import { useEffect, useRef, useState } from 'react';
import { ApiError, query } from './api';
import { OpsActionReceipt, type OpsActionRecord } from './OpsActionPanel';
import type { OpsRecoveryState } from './ops';

export function OpsRecoveryPanel({ rows, onUnauthorized }: { rows?: OpsRecoveryState[]; onUnauthorized: () => void }) {
  const [records, setRecords] = useState<Record<string, OpsActionRecord>>({}), [busy, setBusy] = useState(''), [error, setError] = useState('');
  const flight = useRef<AbortController | null>(null);
  useEffect(() => () => { flight.current?.abort(); }, []);
  const read = async (id: string, remote: boolean) => {
    if (flight.current) return;
    const controller = new AbortController(); flight.current = controller; setBusy(id); setError('');
    try {
      const record = await query<OpsActionRecord>(remote ? 'ops-action/reconcile' : 'ops-action/receipt', { id }, controller.signal);
      if (!controller.signal.aborted) setRecords(previous => ({ ...previous, [id]: record }));
    } catch (failure) {
      if (failure instanceof ApiError && failure.status === 401) onUnauthorized();
      if (!controller.signal.aborted) setError('本次回执未核实；没有重发执行，也没有自动重试。');
    } finally { if (flight.current === controller) { flight.current = null; setBusy(''); } }
  };
  if (!rows?.length) return null;
  return <section className="ops-read-panel" aria-label="待核实执行回执"><h2>待核实执行回执</h2><p>保留原提交身份、来源和回执。这里仅查看记录或核实原执行，不创建计划或重新提交；自动重读不会访问远端。</p>{rows.map(row => <article key={row.id || 'unavailable'}>
    <h3>{row.target || '回执存储未核实'}</h3><p className="muted">{row.action_id} · {row.id}</p>{!row.enabled && <p className="notice">原来源或状态程序未通过核对；请保留回执，不能使用当前来源替代。</p>}
    {row.id && <><button type="button" disabled={!!busy} onClick={() => void read(row.id, false)}>查看原执行记录</button> <button type="button" disabled={!!busy || !row.enabled || !!records[row.id] && !['submitting', 'running', 'uncertain'].includes(records[row.id].state)} onClick={() => void read(row.id, true)}>核实原执行回执</button></>}
    {busy === row.id && <p role="status">正在核对原执行回执…</p>}{records[row.id] && <OpsActionReceipt record={records[row.id]} />}
  </article>)}{error && <p role="alert" className="notice">{error}</p>}</section>;
}
