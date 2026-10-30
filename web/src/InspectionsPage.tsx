import { useCallback, useState } from 'react';
import { Badge } from '@radix-ui/themes';
import { query } from './api';
import type { Inspection } from './contracts';
import { InspectionSections } from './InspectionSections';
import { pageURL, type PageProps } from './scope';
import { ScopeBar } from './ScopeBar';
import { useQuery } from './useQuery';
import { AutoReadStatus } from './AutoReadStatus';

export function InspectionsPage({ scope, onScopeChange, onUnauthorized }: PageProps) {
  const [refresh, setRefresh] = useState(0);
  const load = useCallback((signal: AbortSignal) => query<Inspection>('inspect', { ...(scope.key ? { key: scope.key } : {}), max_age_days: scope.days }, signal), [scope.key, scope.days]);
  const state = useQuery(`${scope.key}/${scope.days}/${refresh}`, load, onUnauthorized, { automatic: true });
  return <><h1>巡检</h1><p className="muted">当前范围：{scope.key || '全部资产'} {scope.key && <a href={pageURL('inspections', { ...scope, key: '', issue: '' })}>查看全部范围</a>}</p>
    <ScopeBar scope={scope} onScopeChange={onScopeChange} onRefresh={() => setRefresh(value => value + 1)} />
    <AutoReadStatus state={state.auto} />
    <p id="status" role="status" className="muted">{state.status === 'ready' ? '已读取保存的巡检结果' : state.status === 'loading' ? '正在读取巡检…' : ''}</p>
    <div id="error" role="alert">{state.status === 'error' && <p className="error">{state.error.message}</p>}</div>
    <div id="content" className="report">{state.status === 'ready' && <><section><h2>当前检查概况</h2><Badge color={state.data.ok ? 'green' : state.data.risk.confirmed ? 'red' : 'amber'}>{state.data.ok ? '已通过本范围检查' : state.data.risk.confirmed ? '存在已确认风险' : '尚未全部通过'}</Badge><p className="evidence">报告时间：{state.data.as_of}。报告生成成功不等于全部通过；这里只评价所列检查。</p></section><InspectionSections inspection={state.data} scope={scope} /></>}</div>
  </>;
}
