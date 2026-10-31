import { describe, expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import type { AssetSummaries, AssetSummary, CertificatesReport, ExpiryReport } from './contracts';
import { AssetTotals, CertificateOverview, ExpiryOverview, FreshnessOverview, ServerOverview } from './OverviewSections';

const row = (key: string, category: string, archived = false): AssetSummary => ({ key, kind: key.split('/')[0], name: key.split('/')[1], labels: null, archived, category, ok: false, coverage: { expected: 1, checked: 0, unknown: 1, not_applicable: 0 }, risk: { confirmed: 0, unknown: 0, waived: 0 }, reason: { code: 'unsupported', message: 'saved result' }, execution: [], evidence: [] });
const scope = { key: '', days: 7, issue: '' };
const summaries = (rows: AssetSummary[]): AssetSummaries => ({ version: 'v1', as_of: '2026-10-31T12:00:00Z', max_age_days: 7, rows });
const markup = renderToStaticMarkup;
describe('overview saved results', () => {
  it('counts each active server category once and links directly to its asset filter', () => {
    const report = summaries([row('Server/a', 'risk'), row('App/b', 'no_checks'), row('Repo/c', 'passed'), row('Domain/d', 'future-code'), row('Domain/old', 'risk', true)]);
    const html = markup(<AssetTotals report={report} scope={scope} />);
    expect(html).toContain('在用资产 4');
    for (const category of ['risk', 'needs_attention', 'no_checks', 'passed']) expect(html).toContain(`category=${category}`);
    expect(html).toContain('暂无检查不是已通过');
    expect(html).not.toContain('在用资产 5');
  });
  it('uses backend day values and excludes archived records from missing expiry counts', () => {
    const report: ExpiryReport = { version: 'v1', ok: false, as_of: '2026-10-31', within_days: 3652500, entries: [{ kind: 'Domain', name: 'example.com', file: '', expires_at: '2026-10-29', days_left: -2 }] };
    const rows = [row('Domain/example.com', 'needs_attention'), row('Domain/missing.example.com', 'needs_attention'), row('Domain/old.example.com', 'risk', true)];
    const html = markup(<ExpiryOverview report={report} records={rows} kind="Domain" />);
    expect(html).toContain('已过期 2 天'); expect(html).toContain('有 1 个未登记到期日，无法判断'); expect(html).toContain('已登记 1 / 共 2');
    const empty = markup(<ExpiryOverview report={{ ...report, entries: [] }} records={rows} kind="Domain" />);
    expect(empty).toContain('不代表安全/正常'); expect(empty).not.toContain('30 天内没有到期项');
    const server = markup(<ExpiryOverview report={{ ...report, entries: [] }} records={[row('Server/web', 'no_checks')]} kind="Server" />);
    expect(server).toContain('另有 1 个未登记'); expect(server).not.toContain('role="alert"');
  });
  it('shows hardware strings literally and never derives utilization or zero for missing fields', () => {
    const web = row('Server/web', 'needs_attention'); web.hardware = { cpu: '4 cores', memory: '16 GiB', disk: '<img src=x>' };
    const html = markup(<ServerOverview rows={[web, row('Server/retired', 'risk', true)]} scope={scope} />);
    expect(html).toContain('台账登记值 · 非实时'); expect(html).toContain('16 GiB'); expect(html).toContain('&lt;img src=x&gt;'); expect(html).toContain('未登记'); expect(html).not.toContain('retired'); expect(html).not.toContain('0%');
  });
  it('separates stale certificate history, trust verification, ambiguity and uncollected hosts', () => {
    const cert = { host: 'wiki.example.com', port: 443, apps: ['App/wiki', 'App/blog'], not_after: '2026-11-01T00:00:00Z', days_left: null, verified: false, verification: 'untrusted', observed_at: '2026-10-01T00:00:00Z', stale: true, data_state: 'stale', expiry_state: 'unknown' };
    const report: CertificatesReport = { version: 'v1', as_of: '2026-10-31T00:00:00Z', max_age_days: 7, has_snapshots: true, message: '', groups: [{ app: '', ownership: 'ambiguous', certificates: [cert] }], uncollected: [{ ...cert, host: 'new.example.com', data_state: 'uncollected', not_after: null, observed_at: null }] };
    const html = markup(<CertificateOverview report={report} scope={scope} />);
    for (const text of ['归属有歧义', '数据已过期', '到期状态未知', 'untrusted', '未采集', 'new.example.com']) expect(html).toContain(text);
    expect(html).not.toContain('剩余 0 天'); expect(html).not.toContain('未临近');
    expect(markup(<CertificateOverview report={{ ...report, has_snapshots: false, groups: [] }} scope={scope} />)).toContain('没有 tls 探测快照 · 不代表证书正常');
  });
  it('compares stored observation times to server report time, deduplicates instances, and exposes stale/missing/future times', () => {
    const a = row('Domain/a.example.com', 'needs_attention'), b = row('Domain/b.example.com', 'needs_attention');
    a.execution = [{ plugin: 'dns', instance: 'one', status: 'succeeded', code: 'stale', observed_at: '2026-10-01T12:00:00Z', snapshot_file: '', receipt_file: '' }, { plugin: 'tls', instance: 'missing', status: 'failed', code: 'failed', observed_at: null, snapshot_file: '', receipt_file: '' }];
    b.execution = [a.execution[0], { ...a.execution[0], instance: 'future', observed_at: '2026-11-01T12:00:00Z' }];
    const html = markup(<FreshnessOverview report={summaries([a, b])} />);
    for (const text of ['数据采集时间', '2026-10-01T12:00:00Z', '2026-11-01T12:00:00Z', '最旧观测已超过证据有效期', '1 个实例缺少观测时间', '1 个观测时间在未来', '采集由外部任务执行']) expect(html).toContain(text);
    expect(markup(<FreshnessOverview report={summaries([])} />)).toContain('没有保存的观测时间，不代表正常');
  });
  it.each([['app', 'App/wiki'], ['no_app', '无应用'], ['unassociated', '未关联应用']])('shows certificate ownership %s and fresh expiry without changing trust results', (ownership, title) => {
    const report: CertificatesReport = { version: 'v1', as_of: '2026-10-31T00:00:00Z', max_age_days: 7, has_snapshots: true, message: '', groups: [{ app: ownership === 'app' ? 'App/wiki' : '', ownership, certificates: [{ host: 'wiki.example.com', port: 443, apps: ownership === 'app' ? ['App/wiki'] : [], not_after: '2026-12-31T00:00:00Z', days_left: 61, verified: false, verification: 'untrusted', observed_at: '2026-10-31T00:00:00Z', stale: false, data_state: 'fresh', expiry_state: 'not_due' }] }], uncollected: [] };
    const html = markup(<CertificateOverview report={report} scope={scope} />);
    expect(html).toContain(title); expect(html).toContain('剩余 61 天'); expect(html).toContain('未临近'); expect(html).toContain('untrusted');
    if (ownership === 'app') expect(html).toContain('key=App%2Fwiki');
  });
});
