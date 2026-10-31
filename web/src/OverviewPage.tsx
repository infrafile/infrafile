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
  const [partial, setPartial] = useState<{ key: string; data: OverviewData }>({ key: '', data: {} });
  const [issues, setIssues] = useState<{ key: string; automatic: boolean; errors: Errors }>({ key: '', automatic: false, errors: {} });
  const load = useCallback(async (signal: AbortSignal, automatic = false): Promise<OverviewData> => {
    const prior = automatic && previous.current?.key === requestKey ? previous.current.data : {};
    if (!automatic) setPartial({ key: requestKey, data: {} });
    setIssues(current => ({ key: requestKey, automatic, errors: automatic && current.key === requestKey ? current.errors : {} }));
    function status(block: BlockName, message?: string) {
      setIssues(current => {
        const errors = current.key === requestKey ? { ...current.errors } : {};
        if (message) errors[block] = { message, failures: (errors[block]?.failures ?? 0) + 1 };
        else delete errors[block];
        return { key: requestKey, automatic, errors };
      });
    }
    async function read<T>(block: BlockName, name: QueryName, args: object, old?: Saved<T>): Promise<Saved<T> | undefined> {
      try {
        const saved = { data: await query<T>(name, args, signal), readAt: new Date().toISOString() };
        signal.throwIfAborted();
        status(block);
        if (!automatic) setPartial(current => ({ key: requestKey, data: { ...(current.key === requestKey ? current.data : {}), [block]: saved } }));
        return saved;
      }
      catch (error) {
        if (signal.aborted || error instanceof ApiError && error.status === 401) throw error;
        status(block, error instanceof Error ? error.message : '无法读取保存结果。');
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
    return data;
  }, [requestKey, scope.days]);
  const state = useQuery(requestKey, load, onUnauthorized, { automatic: true, changes: changedBlocks });
  const data = state.status === 'ready' ? state.data : state.status === 'loading' && partial.key === requestKey ? partial.data : undefined;
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
        <fieldset className="asset-status"><legend>到期资产类型</legend>{[['Domain', '域名'], ['Resource', '资源'], ['Server', '服务器']].map(([value, name]) => <label key={value}><input type="radio" name="expiry-kind" value={value} checked={kind === value} onChange={() => setKind(value)} />{name}</label>)}</fieldset>
        {data.expiry && summary ? <ExpiryOverview report={data.expiry.data} records={summary.rows} kind={kind} /> : data.expiry && <p className="notice">资产目录未读取，无法核对登记范围与缺失数量。</p>}
      </ReadBlock>
      <ReadBlock title="证书到期与应用" readAt={data.certificates?.readAt} error={errors.certificates}>{data.certificates && <CertificateOverview report={data.certificates.data} scope={scope} />}</ReadBlock>
      <ReadBlock title="数据新鲜度" readAt={data.summary?.readAt} error={errors.summary}>{summary && <FreshnessOverview report={summary} />}</ReadBlock>
      <ReadBlock title="服务器配置" readAt={data.summary?.readAt} error={errors.summary}>{summary && <ServerOverview rows={summary.rows} scope={scope} />}</ReadBlock>
    </div>}
  </>;
}
