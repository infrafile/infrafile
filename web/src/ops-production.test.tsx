import { describe, expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { OpsProduction } from './OpsProduction';
import { unverifiedOps, type OpsApplication, type OpsReport } from './ops';

function app(): OpsApplication {
  return { key: 'App/demo', name: 'demo', category: 'ops', deployments: ['one'], notice: '', production: { boundary: '单例，不试停', gates: ['readiness', 'traffic', 'replicas', 'recovery', 'release'].map(id => ({ id, requirements: ['尚需完整证据'], observations: id !== 'readiness' ? [] : [{ id: 'sample', hosts: ['Server/one'], summary: '<script>partial only</script>', reference: 'reviewed', reference_sha256: 'a'.repeat(64), evidence: { status: 'STALE', observed_status: 'PASS', observed_at: '2026-11-05T07:08:07Z', source: 'reviewed-observation', max_age_seconds: 300, reason: 'partial_observation_not_admission', freshness: 'stale' } }] })) } };
}

describe('production admission evidence', () => {
  it('keeps all five requirements unverified, escapes observations and offers no execution', () => {
    const html = renderToStaticMarkup(<OpsProduction app={app()} />);
    expect(html.match(/准入尚未验证/g)).toHaveLength(5);
    expect(html).toContain('历史局部结果：通过'); expect(html).toContain('当前不能沿用');
    expect(html).toContain('已过期'); expect(html).toContain('&lt;script&gt;');
    expect(html).not.toContain('<script>'); expect(html).not.toContain('<button'); expect(html).not.toContain('ops-pass');
  });
  it('never transfers one host sample to a different deployment and distinguishes missing older packages', () => {
    const html = renderToStaticMarkup(<OpsProduction app={app()} host="Server/two" />);
    expect(html).toContain('当前只显示 two 的样本'); expect(html).not.toContain('partial only');
    expect(html).toContain('此范围没有已登记的核实样本');
    expect(renderToStaticMarkup(<OpsProduction app={{ ...app(), production: undefined }} />)).toContain('本代未包含生产准入证据');
  });
  it('downgrades facts after read failure without changing original time or requirements', () => {
    const original = app(); original.production!.gates[0].observations[0].evidence.status = 'PASS';
    const report = { applications: [original], servers: [], deployments: [], monitors: [] } as unknown as OpsReport;
    const failed = unverifiedOps(report).applications[0];
    expect(failed.production!.gates[0].observations[0].evidence.status).toBe('UNKNOWN');
    expect(failed.production!.gates[0].observations[0].evidence.observed_at).toBe('2026-11-05T07:08:07Z');
    const html = renderToStaticMarkup(<OpsProduction app={failed} />);
    expect(html).toContain('当前读取失败，证据未复核'); expect(html).not.toContain('ops-pass');
    expect(html.match(/准入尚未验证/g)).toHaveLength(5);
    expect(original.production!.gates[0].observations[0].evidence.status).toBe('PASS');
  });
});
