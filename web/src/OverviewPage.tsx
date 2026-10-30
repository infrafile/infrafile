import { useCallback, useRef, useState, type ReactNode } from 'react';
import { ApiError, query, type QueryName } from './api';
import type { AssetSummaries, CertificatesReport, ExpiryReport } from './contracts';
import { AutoReadStatus } from './AutoReadStatus';
import { AssetTotals, CertificateOverview, ExpiryOverview, FreshnessOverview, ServerOverview } from './OverviewSections';
import { ScopeBar } from './ScopeBar';
import type { PageProps } from './scope';
import { useQuery } from './useQuery';

type Saved<T> = { data: T; readAt: string };
type OverviewData = { summary?: Saved<AssetSummaries>; expiry?: Saved<ExpiryReport>; certificates?: Saved<CertificatesReport> };
type BlockName = keyof OverviewData;
type BlockError = { message: string; failures: number };
type Errors = Partial<Record<BlockName, BlockError>>;
const blocks: BlockName[] = ['summary', 'expiry', 'certificates'];
const changedBlocks = (previous: OverviewData, next: OverviewData) => blocks.filter(name => JSON.stringify(previous[name]?.data, (key, value) => key === 'as_of' ? undefined : value) !== JSON.stringify(next[name]?.data, (key, value) => key === 'as_of' ? undefined : value)).length;

function ReadBlock({ title, readAt, error, children }: { title: string; readAt?: string; error?: BlockError; children: ReactNode }) {
  return <section aria-label={title}><h2>{title}</h2>{error && <p className="notice" role="alert">{title}读取失败：{error.message}{readAt && ' 保留上次内容；请核对其读取时间。'}{error.failures >= 3 && ' 下列状态可能已过期。'}</p>}{readAt && <p className="evidence">上次读取时间：<time>{readAt}</time></p>}{children}</section>;
}
export function OverviewPage({ scope, onScopeChange, onUnauthorized }: PageProps) {
  const [refresh, setRefresh] = useState(0), [kind, setKind] = useState('Domain');
  const requestKey = `${scope.days}/${refresh}`;
  const previous = useRef<{ key: string; data: OverviewData } | null>(null);
  const [issues, setIssues] = useState<{ key: string; automatic: boolean; errors: Errors }>({ key: '', automatic: false, errors: {} });
  const load = useCallback(async (signal: AbortSignal, automatic = false): Promise<OverviewData> => {
    const prior = automatic && previous.current?.key === requestKey ? previous.current.data : {};
    const failed: Partial<Record<BlockName, string>> = {};
    async function read<T>(block: BlockName, name: QueryName, args: object, old?: Saved<T>): Promise<Saved<T> | undefined> {
      try { return { data: await query<T>(name, args, signal), readAt: new Date().toISOString() }; }
      catch (error) {
        if (signal.aborted || error instanceof ApiError && error.status === 401) throw error;
        failed[block] = error instanceof Error ? error.message : '无法读取保存结果。';
        return old;
      }
    }
    const data: OverviewData = {
      summary: await read('summary', 'asset-summaries', { max_age_days: scope.days }, prior.summary),
      expiry: await read('expiry', 'expiry', { within_days: 3652500 }, prior.expiry),
      certificates: await read('certificates', 'certificates', { max_age_days: scope.days }, prior.certificates),
    };
    signal.throwIfAborted();
    previous.current = { key: requestKey, data };
    setIssues(current => {
      const errors: Errors = {};
      for (const name of blocks) if (failed[name]) errors[name] = { message: failed[name]!, failures: (current.key === requestKey ? current.errors[name]?.failures ?? 0 : 0) + 1 };
      return { key: requestKey, automatic, errors };
    });
    return data;
  }, [requestKey, scope.days]);
  const state = useQuery(requestKey, load, onUnauthorized, { automatic: true, changes: changedBlocks });
  const data = state.status === 'ready' ? state.data : undefined;
  const errors = issues.key === requestKey ? issues.errors : {};
  const failures = Math.max(0, ...Object.values(errors).map(error => error.failures));
  const readTimes = data ? blocks.flatMap(name => data[name] ? [data[name]!.readAt] : []).sort() : [];
  const auto = { ...state.auto, readAt: readTimes[0] ?? null, error: issues.automatic && failures ? Error('部分读取失败。') : state.auto.error, failures };
  const summary = data?.summary?.data;
  return <><h1>总览</h1><p className="muted">全部在用资产 · 依据台账与已保存观测</p><ScopeBar scope={scope} onScopeChange={onScopeChange} onRefresh={() => setRefresh(value => value + 1)} /><AutoReadStatus state={auto} />
    {state.status === 'loading' && <p role="status">正在读取总览…</p>}{state.status === 'error' && <p className="error" role="alert">{state.error.message}</p>}
    {data && <div className="overview-grid">
      <ReadBlock title="资产状态" readAt={data.summary?.readAt} error={errors.summary}>{summary && <AssetTotals report={summary} scope={scope} />}</ReadBlock>
      <ReadBlock title="登记到期" readAt={data.expiry?.readAt} error={errors.expiry}>
        <label>资产类型 <select aria-label="到期资产类型" value={kind} onChange={event => setKind(event.target.value)}><option value="Domain">域名</option><option value="Resource">资源</option><option value="Server">服务器</option></select></label>
        {data.expiry && summary ? <ExpiryOverview report={data.expiry.data} records={summary.rows} kind={kind} /> : data.expiry && <p className="notice">资产目录未读取，无法核对登记范围与缺失数量。</p>}
      </ReadBlock>
      <ReadBlock title="证书到期与应用" readAt={data.certificates?.readAt} error={errors.certificates}>{data.certificates && <CertificateOverview report={data.certificates.data} scope={scope} />}</ReadBlock>
      <ReadBlock title="数据新鲜度" readAt={data.summary?.readAt} error={errors.summary}>{summary && <FreshnessOverview report={summary} />}</ReadBlock>
      <ReadBlock title="服务器配置" readAt={data.summary?.readAt} error={errors.summary}>{summary && <ServerOverview rows={summary.rows} scope={scope} />}</ReadBlock>
    </div>}
  </>;
}
