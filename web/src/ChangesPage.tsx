import { useCallback, useState } from 'react';
import { Button } from '@radix-ui/themes';
import { ApiError, query } from './api';
import type { ChangeStatus, ChangesResult, ExecutionIndex, ExecutionResult, ExecutionsResult } from './contracts';
import { InventoryEditor } from './InventoryEditor';
import { Raw, StatusBadge } from './InspectionSections';
import { pageURL, type PageProps } from './scope';
import { ScopeBar } from './ScopeBar';
import { useQuery } from './useQuery';

function ExecutionCard({ row, onUnauthorized }: { row: ExecutionIndex; onUnauthorized: () => void }) {
  const [read, setRead] = useState(0);
  const load = useCallback((signal: AbortSignal) => query<ExecutionResult>('execution', { id: row.id }, signal), [row.id]);
  const state = useQuery(read ? `${row.id}/${read}` : null, load, onUnauthorized);
  return <article><h3>{row.target} · {row.operation}</h3><StatusBadge value={row.status} /><p>阶段：<StatusBadge value={row.phase} />；记录时间：{row.updated_at}</p>
    <p>主体说明：{row.actor}；授权来源：{row.authorization_ref}</p><p className="evidence">审阅摘要：{row.id}；计划有效至：{row.expires_at}</p>
    {row.status === 'uncertain' && <p className="notice">执行结果不确定。先通过本地 CLI 回读核实；不要盲目重新发送控制请求。</p>}
    <Button size="1" variant="soft" disabled={state.status === 'loading'} onClick={() => setRead(value => value + 1)}>读取完整计划与回执</Button>
    {state.status === 'error' && <p role="alert" className="error">{state.error.message}</p>}
    {state.status === 'ready' && <><Raw title="审阅计划" value={state.data.data.plan} /><Raw title="写前、写后与回读记录" value={state.data.data} /><p className="evidence">如需核实，使用本地 CLI：infrafile apply &lt;原计划文件&gt; --reviewed-digest {row.id} --authorization-ref &lt;适用授权引用&gt; --actor &lt;主体说明&gt;。已有摘要只回读，不重发写请求。</p></>}
  </article>;
}
export function ChangesPage({ scope, onScopeChange, onUnauthorized }: PageProps) {
  const [refresh, setRefresh] = useState(0);
  const load = useCallback(async (signal: AbortSignal) => {
    const info = await query<ChangeStatus>('change/status', {}, signal);
    if (info.transaction.status === 'recovery_required') return { info };
    const rethrow = (error: unknown): never => {
      // Promise.all stops forwarding errors after its first rejection.
      if (error instanceof ApiError && error.status === 401) onUnauthorized();
      throw error;
    };
    const [changes, executions] = await Promise.all([
      query<ChangesResult>('changes', {}, signal).catch(rethrow),
      !scope.key || scope.key.startsWith('Resource/') ? query<ExecutionsResult>('executions', scope.key ? { key: scope.key } : {}, signal).catch(rethrow) : Promise.resolve(undefined),
    ]);
    return { info, changes, executions };
  }, [scope.key, onUnauthorized]);
  const state = useQuery(`${scope.key}/${scope.days}/${scope.issue}/${refresh}`, load, onUnauthorized);
  const data = state.status === 'ready' ? state.data : undefined;
  const events = data?.changes?.changes.filter(event => (!scope.key || event.identity.key.target === scope.key) && (!scope.issue || event.identity.fingerprint + '/' + event.identity.scope_hash === scope.issue)).toReversed() ?? [];
  return <><h1>变更</h1><p className="muted">当前范围：{scope.key || '全部资产'} {scope.key && <a href={pageURL('changes', { ...scope, key: '', issue: '' })}>查看全部范围</a>}</p>
    <ScopeBar scope={scope} onScopeChange={onScopeChange} onRefresh={() => setRefresh(value => value + 1)} />
    <p id="status" role="status" className="muted">{data ? '已读取保存的变更记录' : state.status === 'loading' ? '正在读取变更…' : ''}</p>
    <div id="error" role="alert">{state.status === 'error' && <p className="error">{state.error.message}</p>}</div>
    <div id="content" className="report">{data && <>
      <InventoryEditor key={`${scope.key}/${scope.days}/${refresh}`} scope={scope} info={data.info} onUnauthorized={onUnauthorized} onRefresh={() => setRefresh(value => value + 1)} />
      <section><h2>问题变化时间线</h2>{!data.changes ? <p>台账有未完成的变更。请先恢复事务，再读取台账和问题历史。</p> : <>
        <p className="muted">只展示已显式保存的变化；浏览不会刷新历史，也不表示云端发生了操作。</p>
        {!data.changes.present ? <p>尚无问题历史。请通过本地 CLI 显式检查并保存历史，缺少记录不表示没有问题。</p> : <>
          <p className="evidence">当前游标 {data.changes.last_sequence}；已清理至 {data.changes.pruned_through}。已清理的变化须查阅独立导出。</p>
          {!events.length && <p>此范围没有保留的变化，当前问题是否存在仍以巡检和已保存状态为准。</p>}
          <ul className="items">{events.map(event => <li key={String(event.sequence)}><h3>#{event.sequence} · {event.identity.key.target}</h3><StatusBadge value={event.type} /><p>{event.at} · {event.identity.key.rule} · {event.identity.key.aspect}</p><a href={pageURL('inspections', { ...scope, key: event.identity.key.target })}>查看当前巡检</a><Raw title="问题身份" value={event.identity} /></li>)}</ul>
        </>}
      </>}</section>
      {data.changes && <section><h2>云计划与执行回执</h2><p className="muted">这里只展示已尝试执行的计划和本地回执。原始未执行计划仍在其文件中审阅；没有回执不表示云端没有变更。执行主体和授权引用是说明，不是审批身份认证。</p>
        {!data.executions ? <p>当前选择不是云控制 Resource；可切换到全部范围查看回执。</p> : !data.executions.executions.length ? <p>尚无本范围的执行回执。请通过本地 CLI 审阅和执行；保存台账不会执行云操作。</p> : data.executions.executions.map(row => <ExecutionCard key={row.id} row={row} onUnauthorized={onUnauthorized} />)}
      </section>}
    </>}</div>
  </>;
}
