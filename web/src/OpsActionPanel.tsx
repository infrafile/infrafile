import { useEffect, useRef, useState } from 'react';
import { ApiError, query } from './api';
import { opsName, opsTime, type OpsReadCapability } from './ops';

export type OpsActionResult = { version: string; id: string; action_id: string; target: string; operation: string; state: string; error_code: string; digest: string; expires_at: string | null; invocation_id: string; identity_kind?: 'systemd_invocation' | 'docker_transaction'; disposition: string; fields: { label: string; value: string }[] };
export type OpsActionRecord = { version: string; id: string; action_id: string; target: string; state: string; error_code: string; created_at: string; updated_at: string; binding: string; plan: OpsActionResult; result: OpsActionResult | null };
export const actionStates: Record<string, string> = { prepared: '预检计划已准备 · 尚未执行', submitting: '已记录提交意图 · 等待执行回执', running: '执行中或正在关联已有执行', completed: '本次执行已完成', failed: '本次任务失败', rejected: '本次请求未通过', uncertain: '执行结果待核实 · 不要重复提交' };
export const actionErrors: Record<string, string> = {
  ok: '', running: '只查询保存的回执，不会重发执行。', submission_pending: '请求已经持久记录，关闭页面也不会撤销已提交的任务。',
  awaiting_invocation: '等待 systemd 执行身份。', source_changed: '台账来源已变化，本次禁止提交。', actor_changed: '固定动作程序或绑定已变化，本次禁止提交。',
  actor_unavailable: '固定动作程序未通过核对；先保留现有回执。', configuration_changed: '远端 unit、脚本或受限身份与计划不一致。',
  busy: '已有操作进行中，本次未开始。', unresolved_execution: '存在未核实的执行；先查询已有回执。', rate_limited: '距离上次提交太近，本次未开始。',
  timer_due_soon: '接近 timer 自然触发时间，本次不提交。可等自然执行结束后重新预检。', timer_window_unknown: '无法确认自然排程窗口，本次不提交。',
  plan_expired: '计划已过期，请重新预检。', plan_mismatch: '计划内容或来源已变化，请重新预检。', host_rebooted: '主机身份周期已变化，请重新预检。',
  unit_not_expected: 'unit 不是已核实的 oneshot，拒绝执行。', timer_not_expected: 'timer 状态或配置不符，拒绝执行。',
  receipt_write_failed: '写入执行回执失败；不要重复提交，先核对已有回执。', receipt_unavailable: '无法核对回执存储；没有自动重试。',
  action_timeout: '等待执行回执超时；任务可能已运行，请只查询本次回执。', transport_timeout: 'SSH 回复超时；任务可能已运行，请只查询回执。',
  transport_failed: 'SSH 回复未确认；不要重发执行，请只查询回执。', submission_unknown: '提交结果未确认，请只查询回执。',
  completion_unverified: '执行可能已经发生，完成证据尚未核实。', invocation_output_missing: '缺少此 InvocationID 的有限输出，不能判为完成。',
  invocation_replaced: '当前执行身份已变化，不能把后续自然执行归给本次请求。', invocation_unknown: '无法可靠关联执行身份。',
  observation_timeout: '观察结果超时，任务是否完成待核实。', task_failed: '固定任务返回失败，原 timer 保留。',
  not_submitted: '远端确认仍是未提交的计划，本次未执行。', remote_receipt_missing: '远端没有对应回执；结果保持待核实。',
  output_limit: '结果超过上限，已停止本地等待；不要重发执行。', invalid_output: '结果格式未通过核对；先查询回执。',
  submission_reply_lost: '提交响应未确认。先查询本次回执，不要重复提交。',
  recovery_origin_mismatch: '返回的计划摘要或执行身份与原回执不一致；不能用其他执行核销本次提交。',
  recovery_unavailable: '原执行来源或状态程序未通过核对；请保留回执。',
  dispatching: '提交意图已保存，正在启动独立协调任务；不要重复提交。',
  dispatch_unverified: '独立协调任务的启动回复未确认；只核实原回执，不补发执行。',
  coordinator_result_missing: '协调任务没有可核实的完成回执；任务结果仍未知。',
  worker_preflight_rejected: '执行端重新预检未通过，本次语法检查未开始。',
  worker_ownership_changed: '协调执行身份未通过复核，保留原回执继续核实。',
  result_unverified: '语法结果、隔离或收尾证据不足；只核实原回执，不重复提交。',
  syntax_failed: '隔离语法检查未通过；收尾已核实，业务服务未被重载或启停。',
  resource_pressure: '主机资源压力超过检查预算，本次预检停止。',
  preflight_unverified: '配置、加载身份或TLS前提未通过核对，不能准备新执行。',
  configuration_unverified: '固定程序、专用账号或授权文件未通过核对。',
  origin_mismatch: '请求与原回执的来源不一致，不能用新计划核销旧提交。',
};
export function planReady(record: OpsActionRecord | null, now: number) {
  return record?.state === 'prepared' && !!record.plan?.expires_at && Date.parse(record.plan.expires_at) > now;
}
export function OpsActionReceipt({ record }: { record: OpsActionRecord }) {
  const result = record.result ?? record.plan;
  return <div className="ops-action-receipt" aria-live="polite"><p className={record.state === 'completed' ? 'ops-read-complete' : 'notice'}><strong>{actionStates[record.state] ?? '执行状态未知'}</strong></p>{record.error_code && <p>{actionErrors[record.error_code] ?? '此结果未得到确认；请保留回执并核对。'}</p>}<p className="muted">回执 {record.id}<br />记录：{opsTime(record.created_at)}<br />更新：{opsTime(record.updated_at)}</p>{result?.invocation_id && <p className="ops-action-identity">{result.identity_kind === 'docker_transaction' ? '事务ID' : 'InvocationID'}：{result.invocation_id}</p>}{result?.disposition === 'joined_existing' && <p className="notice">关联的是已有执行，本次没有重新启动任务。</p>}{result?.disposition === 'submitted' && <p className="muted">已提交固定启动请求并关联上述执行；不宣称能排除同时到来的其他发起者。</p>}<dl className="ops-read-fields">{result?.fields?.map((field, index) => <div key={index}><dt>{field.label}</dt><dd>{field.value}</dd></div>)}</dl></div>;
}

export function OpsActionChooser({ capabilities, onUnauthorized }: { capabilities: OpsReadCapability[]; onUnauthorized: () => void }) {
  const [selected, setSelected] = useState('');
  if (capabilities.length < 2) return <OpsActionPanel capability={capabilities[0]} onUnauthorized={onUnauthorized} />;
  const capability = capabilities.find(cap => cap.id === selected);
  return <><label className="ops-read-choice">操作目标与动作<select aria-label="操作目标与动作" value={capability?.id ?? ''} onChange={event => setSelected(event.target.value)}><option value="">请选择明确的机器和动作</option>{capabilities.map(cap => <option key={cap.id} value={cap.id}>{opsName(cap.target)} · {cap.label}</option>)}</select></label>
    <p className="muted">选择不会预检或执行。切换查看对象不会取消已提交任务；保存回执仍可重新读取，未知结果先核实。</p>
    {capability ? <OpsActionPanel key={capability.id + '/' + capability.target} capability={capability} onUnauthorized={onUnauthorized} /> : <p className="muted">先选择一个目标；每份计划和回执只属于对应机器及动作。</p>}
  </>;
}

export function OpsActionPanel({ capability, onUnauthorized }: { capability?: OpsReadCapability; onUnauthorized: () => void }) {
  const [record, setRecord] = useState<OpsActionRecord | null>(null), [busy, setBusy] = useState(false), [error, setError] = useState(''), [reviewed, setReviewed] = useState(false), [now, setNow] = useState(Date.now());
  const flight = useRef<AbortController | null>(null);
  const current = useRef(record); current.current = record;
  const request = async (operation: 'plan' | 'apply' | 'latest' | 'receipt' | 'reconcile', body: object) => {
    if (flight.current) return;
    const controller = new AbortController(); flight.current = controller; setBusy(true); setError('');
    try {
      const next = await query<OpsActionRecord>(`ops-action/${operation}`, body, controller.signal);
      if (!controller.signal.aborted) { setRecord(next.id ? next : null); setReviewed(false); setNow(Date.now()); }
    } catch (failure) {
      if (failure instanceof ApiError && failure.status === 401) onUnauthorized();
      if (!controller.signal.aborted) {
        setError(operation === 'apply' ? actionErrors.submission_reply_lost : '无法读取或准备本次回执；没有自动重试执行。');
        if (operation === 'apply') setRecord(previous => previous && { ...previous, state: 'uncertain', error_code: 'submission_reply_lost' });
      }
    } finally { if (flight.current === controller) { flight.current = null; setBusy(false); } }
  };
  useEffect(() => {
    if (capability) void request('latest', { action_id: capability.id, target: capability.target });
    return () => { flight.current?.abort(); flight.current = null; };
  }, [capability?.id, capability?.target]);
  useEffect(() => { const timer = setInterval(() => setNow(Date.now()), 1000); return () => clearInterval(timer); }, []);
  useEffect(() => {
    if (!record?.id || !['submitting', 'running'].includes(record.state)) return;
    const timer = setInterval(() => { const value = current.current; if (value) void request('receipt', { id: value.id }); }, 2000);
    return () => clearInterval(timer);
  }, [record?.id, record?.state]);
  if (!capability) return <section className="ops-read-panel"><h3>受控单次执行</h3><p className="muted">此对象未开放受控执行。</p></section>;
  const unresolved = !!record && ['submitting', 'running', 'uncertain'].includes(record.state);
  return <section className="ops-read-panel ops-action-panel"><h3>受控单次执行</h3><p>{capability.description}</p><p className="muted">动作入口默认关闭；当前只允许此固定目标。预检不启动任务。</p>{!capability.enabled && <p className="notice">{actionErrors[capability.unavailable_reason ?? 'source_changed'] ?? '来源未确认，不能提交动作。'}</p>}<button type="button" disabled={busy || unresolved || !capability.enabled} onClick={() => void request('plan', { action_id: capability.id, target: capability.target })}>预检单次执行</button>
    {record?.state === 'prepared' && <><p>计划有效至：{opsTime(record.plan.expires_at)}（北京时间）</p>{!planReady(record, now) && <p className="notice">计划已过期，请重新预检。</p>}<label className="ops-action-review"><input type="checkbox" checked={reviewed} onChange={event => setReviewed(event.target.checked)} />已核对目标、固定任务、影响和排程变化</label><button type="button" disabled={busy || !reviewed || !capability.enabled || !planReady(record, now)} onClick={() => void request('apply', { id: record.id, digest: record.plan.digest, reviewed: true })}>提交单次执行</button></>}
    {record && <><button type="button" disabled={busy} onClick={() => void request('receipt', { id: record.id })}>读取保存回执</button>{unresolved && <button type="button" disabled={busy} onClick={() => void request('reconcile', { id: record.id })}>只核实执行端回执</button>}<OpsActionReceipt record={record} /></>}
    {busy && <p role="status">正在处理本次计划或读取回执…</p>}{error && <p className="notice" role="alert">{error}</p>}<p className="muted">提交后不提供取消；关闭页面不会撤销已提交任务。超时和断联先查回执，不自动重发。</p>
  </section>;
}
