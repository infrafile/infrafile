import { useEffect, useRef, useState } from 'react';
import { ApiError, query } from './api';
import type { OpsRefreshAvailability, OpsRefreshResult } from './ops';

export const refreshMessages: Record<string, string> = {
  ok: '已切换到完整的新代，地址和会话保持。每条证据仍使用原观测时间。',
  busy: '已有读取或任务正在进行；本次未切换，请待其结束。',
  unresolved_execution: '有执行结果尚未核实，请先查询原回执；当前代保持。',
  prepared_plan: '有尚未过期的预检，请先完成操作或等计划过期后再刷新。',
  generator_changed: '生成程序或绑定已变化，需要在本机重新核对并绑定。',
  authority_changed: '权威目录身份已变化，本次不生成；请核对本机来源。',
  receipt_unavailable: '回执记录未通过核对，本次不切换。',
  timeout: '本地生成超时，当前代保持；没有触发远端采集。',
  cancelled: '生成已取消或会话失效，当前代保持。',
  generation_failed: '本地生成失败，当前代保持；请核对台账和保存证据。',
  invalid_generation: '生成结果未通过格式与目录校验，当前代保持。',
  generation_validation_failed: '新代的台账、证据或程序绑定不一致，当前代保持。',
  action_scope_changed: '新代动作范围与现有绑定不一致，当前代保持。',
  source_changed: '生成期间来源再次变化，未切换；请待更新完成后重试。',
  output_limit: '生成输出超过上限，已停止并保留当前代。',
};

export function OpsRefreshPanel({ availability, onUpdated, onUnauthorized }: { availability?: OpsRefreshAvailability; onUpdated: () => void; onUnauthorized: () => void }) {
  const [running, setRunning] = useState(false), [message, setMessage] = useState('');
  const flight = useRef<AbortController | null>(null);
  useEffect(() => () => flight.current?.abort(), []);
  const refresh = async () => {
    if (!availability?.enabled || flight.current) return;
    const controller = new AbortController(); flight.current = controller; setRunning(true); setMessage('');
    const timer = setTimeout(() => controller.abort(), (availability.timeout_seconds + 5) * 1000);
    try {
      const result = await query<OpsRefreshResult>('ops-refresh', {}, controller.signal);
      if (!controller.signal.aborted) {
        setMessage(refreshMessages[result.error_code] ?? '生成未完成，当前代保持。');
        // A refused refresh must not unmount/cancel an existing read or receipt.
        if (result.status === 'PASS' || !['busy', 'prepared_plan', 'unresolved_execution'].includes(result.error_code)) onUpdated();
      }
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) onUnauthorized();
      if (flight.current === controller) setMessage('刷新未得到完整回执，请重新读取保存结果核对当前代；没有自动重试。');
    } finally {
      clearTimeout(timer); if (flight.current === controller) { flight.current = null; setRunning(false); }
    }
  };
  if (!availability && !running && !message) return null;
  return <section className="ops-refresh-panel" aria-label="本地同代刷新"><button type="button" onClick={() => void refresh()} disabled={running || !availability?.enabled}>{running ? '正在生成并核对新代…' : '刷新台账与保存证据'}</button><p className="muted">仅在本机重新生成完整同代包；不发 SSH、监控请求或执行任务。自动重读不会生成新代。</p>{availability && !availability.enabled && <p className="notice">{refreshMessages[availability.reason ?? 'generator_changed'] ?? '生成绑定尚未确认。'}</p>}{message && <p role="status">{message}</p>}</section>;
}
