import { describe, expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { OpsContent, OpsState, OpsCriteria } from './OpsPage';
import { unverifiedOps, type OpsEvidence, type OpsReport } from './ops';
import { OpsOperationCatalog, OpsOperationScope } from './OpsOperationCatalog';

const saved: OpsEvidence = { status: 'PASS', reason: 'saved', observed_at: '2026-11-04T10:00:00Z', max_age_seconds: 60, source: 'fleet', freshness: 'fresh' };
const missing: OpsEvidence = { ...saved, status: 'UNKNOWN', observed_at: null };
const fixture = (): OpsReport => ({ version: 'infrafile-ops/v1', enabled: true, captured_at: '2026-11-04T10:00:00Z', evaluated_at: '2026-11-04T10:00:01Z', alignment: 'matched', summary: { servers: 1, applications: 1, deployments: 1, resources: 0, expected_monitors: 1, observed_monitors: 1, runtime_identified: 0 }, servers: [{ key: 'Server/host', name: '<img src=x onerror=alert(1)>', role: 'standby', hardware: { cpu: '2C', memory: '' }, snapshot: saved, disks: [], applications: ['App/app'], notice: '等待自然任务验收' }], applications: [{ key: 'App/app', name: 'app', category: 'ops', deployments: ['one'], notice: '' }], deployments: [{ id: 'one', application: 'App/app', host: 'Server/host', kind: 'systemd', selector: '', runtime: missing, readiness: missing, health: missing, resources: [], checks: [], notice: '' }], resources: [], monitors: [{ id: 1, name: 'probe', type: 'push', active: false, interval: 60, targets: [], association_reason: '旧监控已暂停', configuration: saved, state: { ...saved, status: 'PAUSED' } }], collection_errors: ['business_health_not_recomputed_no_remote_probes'] });

describe('Ops saved evidence overview', () => {
  it('keeps missing operation catalogs unknown instead of reporting zero managed objects', () => {
    const html = renderToStaticMarkup(<OpsOperationCatalog report={fixture()} search="" onSelect={() => {}} />);
    expect(html).toContain('本代未包含操作目录'); expect(html).not.toContain('共0台机器'); expect(html).not.toContain('提交单次执行');
  });
  it('lists control gaps and original component checks without granting restart or claiming rollback readiness', () => {
    const row = { id: 'Deployment/one', kind: 'deployment', object: 'App/app', host: 'Server/host', deployment: 'one', controllers: [{ kind: 'docker-container', name: '<script>store</script>', component: 'store', data_mode: 'stateful', workload_mode: 'service', checks: ['store-health'] }], read_ids: [], action_ids: [], applications: ['App/app'], shared_with: ['App/consumer'], shared_controllers_with: ['App/coupled'], notice: '等待自然任务验收', boundary: 'stateful_control_not_bound', prerequisites: ['roles_replication_and_restore_evidence'] };
    const html = renderToStaticMarkup(<OpsOperationScope row={row} report={fixture()} />);
    expect(html).toContain('未绑定受控动作'); expect(html).toContain('不是已经通过的预检或回退承诺'); expect(html).toContain('共享依赖的其他登记应用'); expect(html).toContain('共用同一控制对象的登记应用'); expect(html).toContain('store-health'); expect(html).toContain('&lt;script&gt;'); expect(html).not.toContain('<script>'); expect(html).not.toContain('提交单次执行');
  });
  it('displays current denominators, gaps, static hardware and escaped labels without creating zero utilization', () => {
    const html = renderToStaticMarkup(<OpsContent report={fixture()} />);
    expect(html).toContain('运行对象已定位 0/1');
    expect(html).toContain('运行证据未知 1 项');
    expect(html).toContain('0 启用 / 1 暂停');
    expect(html).toContain('&lt;img');expect(html).not.toContain('<img');
    expect(html).toContain('使用率见选定机器的按需采集');expect(html).not.toContain('0%');
    expect(html).toContain('有待验收或保留事项');
  });
  it('shows cross-host resource consumers, exact deployments and unknown or unregistered relations without control grants', () => {
    const row = { id: 'Server/data', kind: 'server', object: 'Server/data', host: 'Server/data', controllers: [], read_ids: [], action_ids: [], applications: [], shared_with: [], notice: '', boundary: 'host_control_not_bound', prerequisites: [], hosted_resources: [{ key: 'Resource/shared', consumers: [{ application: 'App/<script>consumer</script>', host: 'Server/web', deployment: 'remote-one' }, { application: 'App/local', host: 'Server/data', deployment: 'local-one' }] }, { key: 'Resource/unknown', consumers: [] }] };
    const html = renderToStaticMarkup(<OpsOperationScope row={row} report={fixture()} />);
    expect(html).toContain('承载资源与登记使用方'); expect(html).toContain('（跨机依赖）'); expect(html).toContain('（同机）'); expect(html).toContain('remote-one'); expect(html).toContain('不代表完整流量依赖'); expect(html).toContain('未登记部署使用方，不代表闲置'); expect(html).toContain('&lt;script&gt;'); expect(html).not.toContain('<script>'); expect(html).not.toContain('提交单次执行');
    expect(renderToStaticMarkup(<OpsOperationScope row={{ ...row, hosted_resources: undefined }} report={fixture()} />)).toContain('本版未包含资源使用方关系');
    expect(renderToStaticMarkup(<OpsOperationScope row={{ ...row, hosted_resources: [] }} report={fixture()} />)).toContain('不能据此判断没有依赖');
    const report = { ...fixture(), operations: [row] };
    expect(renderToStaticMarkup(<OpsOperationCatalog report={report} search="consumer" onSelect={() => {}} />)).toContain('查看机器 data 的操作范围');
    expect(renderToStaticMarkup(<OpsOperationCatalog report={report} search="unrelated" onSelect={() => {}} />)).not.toContain('查看机器 data 的操作范围');
  });
  it('labels historical passing evidence as expired and distinguishes future and missing data', () => {
    const old = renderToStaticMarkup(<OpsState value={{ ...saved, status: 'STALE', observed_status: 'PASS', freshness: 'stale' }} />);
    expect(old).toContain('已过期');expect(old).toContain('历史结果：通过');expect(old).not.toContain('ops-pass');
    expect(renderToStaticMarkup(<OpsState value={{ ...saved, status: 'UNKNOWN', freshness: 'future' }} />)).toContain('观测时间在未来');
    expect(renderToStaticMarkup(<OpsState value={missing} />)).toContain('未采集');
  });
  it('never reuses old green state after an automatic read failure; pause and original observations are retained', () => {
    const current = fixture();const old = unverifiedOps(current);
    expect(old.servers[0].snapshot.status).toBe('UNKNOWN');expect(old.monitors[0].state.status).toBe('PAUSED');
    expect(old.monitors[0].configuration.status).toBe('UNKNOWN');
    expect(old.servers[0].snapshot.observed_at).toBe(saved.observed_at);
    expect(current.servers[0].snapshot.status).toBe('PASS');
    const html = renderToStaticMarkup(<OpsContent report={old} />);
    expect(html).toContain('尚未核对当前权威来源');expect(html).not.toContain('快照可用');
  });
  it('shows changed authority as frozen observations instead of current inventory parity', () => {
    const html = renderToStaticMarkup(<OpsContent report={{ ...fixture(), alignment: 'changed' }} />);
    expect(html).toContain('权威来源已有变化');expect(html).not.toContain('台账与证据包来源一致');
  });
  it('separates scheduled criteria from saved observations, escapes labels and offers no execution', () => {
    const current = fixture(), deployment = current.deployments[0];
    deployment.checks = ['local', 'timer'];
    deployment.criteria = [
      { id: 'local', component: '<img src=x>', kind: 'fleet_systemd', layer: 'runtime', workload_mode: 'service', data_mode: 'stateless', selector: 'core.service', observer: 'Server/host', evidence: saved },
      { id: 'timer', component: 'job', kind: 'systemd_timer', layer: 'schedule', workload_mode: 'scheduled', data_mode: 'stateful', selector: 'job.timer', observer: 'Server/host', evidence: { ...missing, source: 'app-policy', reason: 'remote_probe_not_requested' } },
    ];
    const html = renderToStaticMarkup(<OpsCriteria deployment={deployment} />);
    expect(html).toContain('定时任务（无需常驻）');expect(html).toContain('证据层：调度');
    expect(html).toContain('本轮未执行此判据，配置存在不代表检查通过。');
    expect(html).toContain('&lt;img');expect(html).not.toContain('<img');expect(html).not.toContain('<button');
    expect(html).toContain('已保存证据通过');expect(html).toContain('未采集');
    const unverified = unverifiedOps(current);
    expect(unverified.deployments[0].criteria?.[0].evidence.status).toBe('UNKNOWN');
    expect(unverified.deployments[0].criteria?.[0].evidence.observed_at).toBe(saved.observed_at);
    expect(renderToStaticMarkup(<OpsCriteria deployment={unverified.deployments[0]} />)).not.toContain('ops-pass');
  });
  it('does not invent criterion observations for older packages without details', () => {
    const deployment = fixture().deployments[0];deployment.checks = ['legacy'];
    const html = renderToStaticMarkup(<OpsCriteria deployment={deployment} />);
    expect(html).toContain('本代未包含判据详情，不能据此判断通过。');expect(html).not.toContain('ops-pass');
  });
});
