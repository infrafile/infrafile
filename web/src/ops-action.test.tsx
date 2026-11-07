import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { OpsActionChooser, OpsActionPanel, OpsActionReceipt, planReady, type OpsActionRecord } from './OpsActionPanel';
import { OpsRecoveryPanel } from './OpsRecoveryPanel';

const record: OpsActionRecord = { version: 'infrafile-ops-action/v1', id: 'c'.repeat(32), action_id: 'selected-once', target: 'Server/example', state: 'prepared', error_code: 'ok', created_at: '2026-11-04T12:00:00Z', updated_at: '2026-11-04T12:00:00Z', binding: '', plan: { version: 'infrafile-ops-action/v1', id: 'c'.repeat(32), action_id: 'selected-once', target: 'Server/example', operation: 'plan', state: 'prepared', error_code: 'ok', digest: 'a'.repeat(64), expires_at: '2026-11-04T12:00:30Z', invocation_id: '', disposition: 'submit_once', fields: [{ label: 'Scope', value: '<script>unsafe</script>' }] }, result: null };
describe('fixed on-demand action', () => {
  it('expires a concrete plan instead of enabling blind submission', () => {
    expect(planReady(record, Date.parse('2026-11-04T12:00:10Z'))).toBe(true);
    expect(planReady(record, Date.parse('2026-11-04T12:00:30Z'))).toBe(false);
  });
  it('keeps existing invocation attribution and escapes fields', () => {
    const html = renderToStaticMarkup(<OpsActionReceipt record={{ ...record, state: 'completed', result: { ...record.plan, state: 'completed', invocation_id: 'b'.repeat(32), disposition: 'joined_existing' } }} />);
    expect(html).toContain('本次没有重新启动任务'); expect(html).toContain('InvocationID'); expect(html).toContain('&lt;script&gt;'); expect(html).not.toContain('<script>');
  });
  it('identifies a Docker transaction without claiming a systemd invocation', () => {
    const html = renderToStaticMarkup(<OpsActionReceipt record={{ ...record, state: 'completed', result: { ...record.plan, state: 'completed', invocation_id: record.id, identity_kind: 'docker_transaction' } }} />);
    expect(html).toContain('事务ID'); expect(html).not.toContain('InvocationID');
  });
  it('keeps uncertainty distinct from completion', () => {
    const html = renderToStaticMarkup(<OpsActionReceipt record={{ ...record, state: 'uncertain', error_code: 'transport_timeout' }} />);
    expect(html).toContain('不要重复提交'); expect(html).toContain('任务可能已运行'); expect(html).not.toContain('本次执行已完成');
  });
  it('does not expose an action on unmanaged objects', () => {
    const html = renderToStaticMarkup(<OpsActionPanel onUnauthorized={() => {}} />);
    expect(html).toContain('此对象未开放'); expect(html).not.toContain('<button');
  });
  it('offers only local receipt and status lookup for retained submissions', () => {
    const html = renderToStaticMarkup(<OpsRecoveryPanel rows={[{ id: record.id, action_id: record.action_id, target: record.target, state: 'uncertain', enabled: true }]} onUnauthorized={() => {}} />);
    expect(html).toContain('核实原执行回执'); expect(html).toContain('查看原执行记录'); expect(html).not.toContain('提交单次执行'); expect(html).not.toContain('预检单次执行');
  });
  it('retains unavailable recovery origins without enabling status lookup', () => {
    const html = renderToStaticMarkup(<OpsRecoveryPanel rows={[{ id: record.id, action_id: record.action_id, target: '<script>unknown</script>', state: 'uncertain', enabled: false }]} onUnauthorized={() => {}} />);
    expect(html).toContain('原来源或状态程序未通过核对'); expect(html).toContain('&lt;script&gt;'); expect(html).toMatch(/disabled="">核实原执行回执/); expect(html).not.toContain('<script>');
  });
  it('requires explicit machine/action choice instead of choosing the first match', () => {
    const base = { id: 'one', target: 'Server/one', label: 'Observe once', description: '', related_keys: ['App/shared'], enabled: true, timeout_seconds: 2, max_age_seconds: 60, min_interval_seconds: 10 };
    const html = renderToStaticMarkup(<OpsActionChooser capabilities={[base, { ...base, id: 'two', target: 'Server/two' }]} onUnauthorized={() => {}} />);
    expect(html).toContain('操作目标与动作'); expect(html).toContain('先选择一个目标'); expect(html).not.toContain('预检单次执行'); expect(html).not.toContain('提交单次执行');
  });
});
