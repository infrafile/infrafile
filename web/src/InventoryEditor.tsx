import { useCallback, useEffect, useRef, useState } from 'react';
import { Button, Checkbox } from '@radix-ui/themes';
import { ApiError, parseEditJSON, query } from './api';
import type { ChangePreview, ChangeRequest, ChangeResult, ChangeStatus, ShowResult } from './contracts';
import { Raw } from './InspectionSections';
import { isAssetKey, type Scope } from './scope';
import { useQuery } from './useQuery';

const receipts: Record<string, string> = { committed: '已提交到本地 Git', unchanged: '没有需要提交的变化', rejected: '变更被拒绝，请重新预览', recovery_required: '事务未完成，需要恢复', rolled_back: '已恢复原文件', complete: '事务已完成' };
export function InventoryEditor({ scope, info, onUnauthorized, onRefresh }: { scope: Scope; info: ChangeStatus; onUnauthorized: () => void; onRefresh: () => void }) {
  const load = useCallback(async (signal: AbortSignal) => scope.key && isAssetKey(scope.key)
    ? (await query<ShowResult>('show', { key: scope.key }, signal, parseEditJSON)).record
    : { apiVersion: 'infrafile/v1alpha1', kind: 'Server', metadata: { name: 'new-server' }, spec: { address: '192.0.2.10' } }, [scope.key]);
  const state = useQuery(info.enabled && info.transaction.status !== 'recovery_required' ? scope.key : null, load, onUnauthorized);
  const [draft, setDraft] = useState<string | null>(null), [prepared, setPrepared] = useState<{ request: ChangeRequest; preview: ChangePreview }>();
  const [reviewed, setReviewed] = useState(false), [busy, setBusy] = useState(false), [locked, setLocked] = useState(false);
  const [receipt, setReceipt] = useState<ChangeResult>(), [error, setError] = useState('');
  const controller = useRef<AbortController | null>(null), sending = useRef(false);
  useEffect(() => { controller.current = new AbortController(); return () => controller.current?.abort(); }, []);
  const text = draft ?? (state.status === 'ready' ? JSON.stringify(state.data, null, 2) : '');
  const refresh = <Button type="button" variant="soft" onClick={onRefresh}>检查事务状态并读取最新台账</Button>;
  const fail = (error: unknown, uncertain = false) => {
    if (error instanceof ApiError && error.status === 401) onUnauthorized();
    if (!controller.current?.signal.aborted) setError((error instanceof Error ? error.message : '请求失败。') + (uncertain ? ' 请先检查事务状态，避免盲目重试。' : ''));
  };
  async function preview() {
    setPrepared(undefined); setReviewed(false); setError(''); setReceipt(undefined); setBusy(true);
    try {
      const value = parseEditJSON(text), request: ChangeRequest = { version: 'v1', records: Array.isArray(value) ? value : [value] };
      const result = await query<ChangePreview>('change/preview', request, controller.current!.signal);
      if (result.ok) setPrepared({ request, preview: result }); else setError('校验未通过：' + result.problems.join('、'));
    } catch (error) { fail(error); } finally { setBusy(false); }
  }
  async function apply() {
    if (!prepared || !reviewed || sending.current) return;
    sending.current = true; setLocked(true); setBusy(true); setPrepared(undefined); setReviewed(false); setError('');
    try { setReceipt(await query<ChangeResult>('change/apply', { request: prepared.request, digest: prepared.preview.digest }, controller.current!.signal)); }
    catch (error) { fail(error, true); } finally { setBusy(false); }
  }
  async function recover() {
    if (sending.current) return;
    sending.current = true; setLocked(true); setBusy(true); setError('');
    try { setReceipt(await query<ChangeResult>('change/recover', { id: info.transaction.transaction }, controller.current!.signal)); }
    catch (error) { fail(error, true); } finally { setBusy(false); }
  }
  return <section className="inventory-editor"><h2>台账修改</h2>
    {!info.enabled ? <p>当前工作台为只读模式。需要编辑时，在本机以 --allow-edit 启动后重新配对。</p> : info.transaction.status === 'recovery_required' ? <>
      <p className="notice">存在未完成事务：{info.transaction.transaction}</p><p>恢复会核对文件和提交哈希；发现外部修改会停止并保留现场。</p>
      <Button disabled={busy || locked} onClick={recover}>恢复此事务</Button>
    </> : state.status === 'error' ? <p role="alert" className="error">{state.error.message}</p> : state.status !== 'ready' ? <p role="status">正在读取可编辑记录…</p> : <>
      <p>填写完整记录，可同时提交多条记录并调整引用。不会删除记录；停用时填写 metadata.archived。凭据引用由服务端保留，不能在此表单中填写。</p>
      <p className="notice">保存会重新序列化变更文件，原有注释和排版会变化。此处显示字段差异；需要核对原始字节 diff 时请使用 change preview。</p>
      <form onSubmit={event => { event.preventDefault(); if (!busy && !locked) void preview(); }}>
        <label>完整记录 JSON（单条或数组）<textarea aria-label="完整记录 JSON" value={text} rows={16} spellCheck={false} disabled={busy || locked} onChange={event => { setDraft(event.target.value); setPrepared(undefined); setReviewed(false); setError(''); }} /></label>
        <Button type="submit" disabled={busy || locked} variant="soft">校验并预览变更</Button>
        {prepared && <div className="preview"><p className="evidence">基准 HEAD：{prepared.preview.head}</p><p className="evidence">预览摘要：{prepared.preview.digest}</p>{!prepared.preview.files.length && <p>记录内容与台账一致，没有变化。</p>}{prepared.preview.files.map(file => <section key={file.path}><h3>{file.key}</h3><div className="diff-columns"><div><h3>修改前</h3><pre>{JSON.stringify(file.before, null, 2)}</pre></div><div><h3>修改后</h3><pre>{JSON.stringify(file.after, null, 2)}</pre></div></div></section>)}</div>}
        <label className="reviewed"><Checkbox checked={reviewed} disabled={!prepared || busy || locked} onCheckedChange={value => setReviewed(value === true)} /> 我已核对修改前后的完整内容</label>
        <Button type="button" disabled={!prepared || !reviewed || busy || locked} onClick={apply}>确认并提交到 Git</Button>
      </form>
    </>}
    <div role="status" aria-live="polite">{busy && <p>正在处理，请等待回执。</p>}{error && <p className="error">{error}</p>}{receipt && <><p>{receipts[receipt.status] || receipt.status}</p><Raw value={receipt} title="事务回执" /></>}</div>
    {(locked || info.transaction.status === 'recovery_required') && refresh}
  </section>;
}
