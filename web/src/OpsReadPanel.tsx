import { useEffect, useRef, useState } from 'react';
import { ApiError, query } from './api';
import { opsName, opsStatus, opsTime, type OpsReadCapability, type OpsReadResult, type OpsReadSaved } from './ops';

export const readError: Record<string, string> = {
  metadata_incomplete: '部分组件身份、配置或元数据未核实；请查看各项结果。',
  resource_pressure: '主机资源压力超过预算，本次读取已停止。', pressure_unverified: '无法核对资源压力，本次没有继续读取。',
  ok: '采集完成', timeout: '采集超时，不能判断目标当前状态。', cancelled: '读取已取消，不能据此判断服务状态。',
  busy: '已有读取进行中，本次未开始；请待其结束。', rate_limited: '读取过于频繁，本次未开始；请稍后再试。', source_changed: '台账或证据来源已变化，请重新生成并绑定同代结果。',
  reader_changed: '读取程序或绑定已变化，请重新核对绑定。', binding_invalid: '来源绑定未通过核对，本次未采集。',
  ssh_config_mismatch: '现有 SSH 配置与台账不一致，本次未访问远端。', ssh_authentication_failed: 'SSH 身份验证失败，状态未知。',
  ssh_host_key_failed: 'SSH 主机身份验证失败，状态未知。', ssh_connection_timeout: 'SSH 连接超时，状态未知。',
  ssh_connection_refused: 'SSH 连接被拒绝，状态未知。', ssh_network_unreachable: 'SSH 网络不可达，状态未知。',
  remote_timeout: '远端只读命令超时，当前状态未核实。', local_timeout: '本机 SSH 读取超时，当前状态未核实。',
  output_limit: '输出超过读取上限，已停止读取；结果未知。', invalid_output: '返回内容未通过校验，结果未知。',
  invalid_evidence: '采集内容缺失或格式异常，结果未知。', remote_clock_invalid: '远端观测时间异常，结果未知。',
  provider_failed: '读取程序失败，未获得有效状态。', target_not_supported: '此对象尚未接入按需读取。',
  read_store_unavailable: '本地保存目录未通过核对，本次没有发起远端采集。',
  read_save_failed: '读取已结束，但保存结果未核实；没有自动重新采集。',
  saved_read_missing: '此范围还没有保存结果；没有发起远端采集。',
  saved_read_invalid: '保存结果损坏或未通过核对，当前状态未知。',
  saved_read_unverified: '上次读取只保存了开始意图，没有完整结果；不能据此判断任务是否仍在运行或目标状态。',
  saved_read_source_changed: '来源、程序或证据代已变化。旧样本保留，当前状态未核实。',
  saved_read_stale: '保存的观测已过期；没有自动重新采集。',
};

export function readIsStale(result: OpsReadResult, now: number) {
  const observed = result.observed_at ? Date.parse(result.observed_at) : NaN;
  return !Number.isFinite(observed) || now - observed > result.max_age_seconds * 1000 || observed > now + 300000;
}

export function OpsReadOutput({ result, now = Date.now(), unverified = false }: { result: OpsReadResult; now?: number; unverified?: boolean }) {
  const stale = result.status === 'PASS' && readIsStale(result, now);
  return <div className="ops-read-output" aria-live="polite"><p className={result.status === 'PASS' && !stale && !unverified ? 'ops-read-complete' : 'notice'}><strong>{unverified ? stale ? '保存样本已过期，且来源未通过当前核对；不能代表当前状态。' : '保存样本的来源未通过当前核对，不能代表当前状态。' : stale ? '本次观测已过期，请按需重新采集。' : readError[result.error_code] ?? '采集未成功，状态未知。'}</strong></p><p className="muted">开始：{opsTime(result.started_at)}<br />结束：{opsTime(result.finished_at)} · 耗时 {result.duration_ms} ms<br />远端观测：{opsTime(result.observed_at)}（北京时间）</p>
    {result.sections.map(section => <section key={section.id}><h4>{section.title}</h4><span className={`ops-status ops-${unverified ? 'unknown' : stale ? 'stale' : section.status.toLowerCase()}`}>{unverified ? stale ? '未知 · 已过期' : '未知' : stale ? '已过期' : section.status === 'PASS' ? '已读取' : opsStatus[section.status] ?? '未知'}</span><dl className="ops-read-fields">{section.fields.map((field, index) => <div key={index}><dt>{field.label}</dt><dd>{field.value}</dd></div>)}</dl>{section.lines.length > 0 && <pre className="ops-read-lines" tabIndex={0} aria-label={section.title + '有限输出'}>{section.lines.join('\n')}</pre>}</section>)}
    {result.sections.some(section => section.lines.length > 0) && <p className="muted">样本行保留原UTC时间；这里只显示固定字段，不是业务原始日志。</p>}
  </div>;
}

export function OpsReadChooser({ capabilities, onUnauthorized }: { capabilities: OpsReadCapability[]; onUnauthorized: () => void }) {
  const [selected, setSelected] = useState('');
  if (capabilities.length < 2) return <OpsReadPanel capability={capabilities[0]} onUnauthorized={onUnauthorized} />;
  const capability = capabilities.find(c => c.id === selected);
  return <><label className="ops-read-choice">读取目标与内容<select aria-label="读取目标与内容" value={capability?.id ?? ''} onChange={event => setSelected(event.target.value)}><option value="">请选择本次读取的目标与内容</option>{capabilities.map(c => <option key={c.id} value={c.id}>{opsName(c.target)} · {c.label}</option>)}</select></label>{capability ? <OpsReadPanel key={capability.id + '/' + capability.target} capability={capability} onUnauthorized={onUnauthorized} /> : <p className="muted">此对象有多种读取范围。先选择目标与内容，选择本身不会采集。</p>}</>;
}

export function OpsReadPanel({ capability, onUnauthorized }: { capability?: OpsReadCapability; onUnauthorized: () => void }) {
  const [result, setResult] = useState<OpsReadResult | null>(null), [message, setMessage] = useState(''), [running, setRunning] = useState(false), [now, setNow] = useState(Date.now());
  const flight = useRef<AbortController | null>(null);
  const savedFlight = useRef<AbortController | null>(null);
  const [saved, setSaved] = useState<OpsReadSaved | null>(null), [loadingSaved, setLoadingSaved] = useState(false);
  const readSaved = async () => {
    if (!capability?.saved_read || flight.current || savedFlight.current) return;
    const controller = new AbortController(); savedFlight.current = controller;
    setSaved(null); setResult(null); setMessage(''); setLoadingSaved(true);
    try {
      const data = await query<OpsReadSaved>('ops-read/latest', { action_id: capability.id, target: capability.target }, controller.signal);
      if (savedFlight.current === controller && !controller.signal.aborted) { setSaved(data); setNow(Date.now()); }
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) onUnauthorized();
      if (savedFlight.current === controller && !controller.signal.aborted) setMessage('无法读取本地保存结果；没有发起远端采集。');
    } finally {
      if (savedFlight.current === controller) { savedFlight.current = null; setLoadingSaved(false); }
    }
  };
  useEffect(() => {
    flight.current?.abort(); flight.current = null; savedFlight.current?.abort(); savedFlight.current = null;
    setRunning(false); setResult(null); setSaved(null); setMessage(''); setLoadingSaved(false);
    if (capability?.saved_read) void readSaved();
    return () => { flight.current?.abort(); flight.current = null; savedFlight.current?.abort(); savedFlight.current = null; };
  }, [capability?.id, capability?.target, capability?.enabled, capability?.saved_read]);
  useEffect(() => { const timer = setInterval(() => setNow(Date.now()), 1000); return () => clearInterval(timer); }, []);
  const collect = async () => {
    if (!capability?.enabled || flight.current || loadingSaved) return;
    savedFlight.current?.abort(); savedFlight.current = null; setLoadingSaved(false); setSaved(null);
    const controller = new AbortController(); flight.current = controller;
    setResult(null); setMessage(''); setRunning(true);
    let timedOut = false;
    const timer = setTimeout(() => { timedOut = true; controller.abort(); }, (capability.timeout_seconds + 3) * 1000);
    try {
      const data = await query<OpsReadResult>('ops-read', { action_id: capability.id, target: capability.target }, controller.signal);
      if (flight.current === controller && !controller.signal.aborted) { setResult(data); setNow(Date.now()); }
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) onUnauthorized();
      if (flight.current === controller) setMessage(timedOut ? readError.timeout : controller.signal.aborted ? readError.cancelled : '读取失败，当前状态未知；没有保留上次成功作为当前结果。');
    } finally {
      clearTimeout(timer);
      if (flight.current === controller) { flight.current = null; setRunning(false); }
    }
  };
  if (!capability) return <section className="ops-read-panel"><h3>按需只读采集</h3><p className="muted">此对象尚未接入按需读取，仅展示保存的观测。</p></section>;
  return <section className="ops-read-panel"><h3>按需只读采集</h3><p><strong>本次目标：{opsName(capability.target)}</strong></p><p>{capability.description}</p><p className="muted">最长 {capability.timeout_seconds} 秒 · 间隔至少 {capability.min_interval_seconds} 秒 · 全局最多一项读取 · 打开列表不会采集</p><button type="button" disabled={running || loadingSaved || !capability.enabled} onClick={() => void collect()}>{running ? '正在只读采集…' : capability.label}</button>{running && <button type="button" onClick={() => flight.current?.abort()}>取消读取</button>}
    {capability.saved_read && <><button type="button" disabled={running || loadingSaved} onClick={() => void readSaved()}>{loadingSaved ? '正在读取保存结果…' : '读取保存的采集结果'}</button><p className="muted">保存结果只读本机文件，不发SSH；原观测时间保留，过期或来源变化时不会显示为当前通过。</p></>}
    {!capability.enabled && <p className="notice">{readError[capability.unavailable_reason ?? 'binding_invalid'] ?? '本次绑定未确认，不能采集。'}</p>}
    {running && <p role="status">正在读取固定状态和已有观察样本；未执行新的观察任务。</p>}{message && <p className="notice" role="alert">{message}</p>}{result && <OpsReadOutput result={result} now={now} />}
    {saved && <div className="ops-read-saved"><p><strong>上次保存的采集结果{saved.label ? ' · ' + saved.label : ''}</strong></p><p className="muted">这里只读取本地保存记录。保存：{opsTime(saved.saved_at ?? null)}</p>{saved.error_code !== 'ok' && <p className="notice">{readError[saved.error_code] ?? '保存结果未通过核对，当前状态未知。'}</p>}{saved.result && <OpsReadOutput result={saved.result} now={now} unverified={!saved.binding_matched} />}</div>}
  </section>;
}
