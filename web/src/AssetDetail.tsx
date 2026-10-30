import { useCallback } from 'react';
import { query } from './api';
import type { AssetContext } from './contracts';
import { InspectionSections, Raw } from './InspectionSections';
import { pageURL } from './scope';
import { useQuery } from './useQuery';

export function AssetDetail({ assetKey, days, onNavigate, onUnauthorized }: { assetKey: string; days: number; onNavigate: (key: string) => void; onUnauthorized: () => void }) {
  const load = useCallback((signal: AbortSignal) => query<AssetContext>('context', { key: assetKey, max_age_days: days }, signal), [assetKey, days]);
  const state = useQuery(assetKey + '/' + days, load, onUnauthorized);
  if (state.status === 'error') return <div role="alert" className="error">{state.error.message} 此资产上下文无法读取时，可使用本地 CLI 查看。</div>;
  if (state.status !== 'ready') return <p role="status" className="empty">正在读取资产详情…</p>;
  const data = state.data, record = data.records.find(record => record.key === assetKey);
  const scope = { key: assetKey, days, issue: '' };
  return <><div className="detail-heading"><p className="eyebrow">资产详情</p><h1 tabIndex={-1} id="asset-title">{assetKey}</h1>
    <p className="evidence">详情读取时间：<time>{data.inspection.as_of}</time></p>
    <div className="actions"><a href={pageURL('inspections', scope)}>查看此资产巡检</a><a href={pageURL('changes', scope)}>查看变更与台账编辑</a></div>
  </div><InspectionSections inspection={data.inspection} scope={scope} context={data}>
    <section><h2>登记期望</h2>{record ? <><p className="evidence">{record.file}</p><Raw value={record.record} title="完整脱敏登记" open /></> : <p>未找到登记。</p>}</section>
    <section><h2>直接关系</h2><ul className="items">{data.inspection.relations.map((relation, index) => {
      const target = relation.from === assetKey ? relation.to : relation.from;
      return <li key={index}><span className="muted">{relation.from === assetKey ? '引用' : '被引用'} · {relation.path}</span><br /><a href={pageURL('assets', { ...scope, key: target })} onClick={event => { event.preventDefault(); onNavigate(target); }}>{target}</a></li>;
    })}</ul>{!data.inspection.relations.length && <p className="empty">没有直接关系。</p>}</section>
  </InspectionSections></>;
}
