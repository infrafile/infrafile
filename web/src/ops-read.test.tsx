import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { OpsReadChooser, OpsReadOutput, OpsReadPanel, readIsStale } from './OpsReadPanel';
import { unverifiedOps, type OpsReadCapability, type OpsReadResult, type OpsReport } from './ops';

const result: OpsReadResult = { version: 'infrafile-ops-read/v1', request_id: 'example', action_id: 'selected-read', target: 'Server/example', status: 'PASS', error_code: 'ok', started_at: '2026-11-04T12:00:00Z', finished_at: '2026-11-04T12:00:01Z', observed_at: '2026-11-04T12:00:00Z', duration_ms: 1000, max_age_seconds: 300, sections: [{ id: 'sample', title: '有限样本', status: 'PASS', fields: [{ label: '范围', value: '<script>unsafe</script>' }], lines: ['<selected output>'] }] };
describe('on-demand reads', () => {
  it('never renders a frozen saved sample as current success', () => {
    const html = renderToStaticMarkup(<OpsReadOutput result={result} unverified now={Date.parse('2026-11-04T12:01:00Z')} />);
    expect(html).toContain('不能代表当前状态'); expect(html).toContain('ops-unknown');
    expect(html).not.toContain('ops-read-complete'); expect(html).not.toContain('已读取');
    expect(html).toContain('&lt;script&gt;unsafe&lt;/script&gt;');
  });
  it('shows both old age and unverified origin when they coincide', () => {
    const html = renderToStaticMarkup(<OpsReadOutput result={result} unverified now={Date.parse('2026-11-04T12:06:00Z')} />);
    expect(html).toContain('未知 · 已过期'); expect(html).toContain('不能代表当前状态'); expect(html).not.toContain('ops-read-complete');
  });
  it('keeps saved/read/remote timestamps separate and escapes output', () => {
    const html = renderToStaticMarkup(<OpsReadOutput result={result} now={Date.parse('2026-11-04T12:01:00Z')} />);
    expect(html).toContain('采集完成'); expect(html).toContain('远端观测'); expect(html).toContain('&lt;selected output&gt;'); expect(html).not.toContain('<script>');
  });
  it('ages success without another network request', () => {
    expect(readIsStale(result, Date.parse('2026-11-04T12:06:00Z'))).toBe(true);
    const html = renderToStaticMarkup(<OpsReadOutput result={result} now={Date.parse('2026-11-04T12:06:00Z')} />);
    expect(html).toContain('观测已过期'); expect(html).not.toContain('ops-status ops-pass');
  });
  it('shows failed collection separately from service health', () => {
    const html = renderToStaticMarkup(<OpsReadOutput result={{ ...result, status: 'FAIL', error_code: 'timeout', observed_at: null, sections: [] }} />);
    expect(html).toContain('采集超时'); expect(html).toContain('不能判断目标当前状态'); expect(html).not.toContain('采集完成');
  });
  it('unsupported objects have no collection button', () => {
    const html = renderToStaticMarkup(<OpsReadPanel onUnauthorized={() => {}} />);
    expect(html).toContain('此对象尚未接入'); expect(html).not.toContain('<button');
  });
  it('distinguishes two reading scopes on the same machine without picking one', () => {
    const cap: OpsReadCapability = { id: 'status', label: '读取机器状态', target: 'Server/example', related_keys: [], description: 'Fixed scope', timeout_seconds: 15, max_age_seconds: 300, min_interval_seconds: 10, enabled: true };
    const html = renderToStaticMarkup(<OpsReadChooser capabilities={[cap, { ...cap, id: 'identity', label: '核对服务身份' }]} onUnauthorized={() => {}} />);
    expect(html).toContain('example · 读取机器状态'); expect(html).toContain('example · 核对服务身份');
    expect(html).toContain('此对象有多种读取范围'); expect(html).not.toContain('<button');
  });
  it('requires an explicit machine choice for applications on more than one host', () => {
    const cap: OpsReadCapability = { id: 'one', label: 'Read one', target: 'Server/one', related_keys: [], description: 'Fixed scope', timeout_seconds: 15, max_age_seconds: 300, min_interval_seconds: 10, enabled: true };
    const html = renderToStaticMarkup(<OpsReadChooser capabilities={[cap, { ...cap, id: 'two', target: 'Server/two' }]} onUnauthorized={() => {}} />);
    expect(html).toContain('请选择本次读取的目标与内容');expect(html).toContain('选择本身不会采集');expect(html).not.toContain('<button');
    const single = renderToStaticMarkup(<OpsReadChooser capabilities={[cap]} onUnauthorized={() => {}} />);
    expect(single).toContain('本次目标：one');expect(single).toContain('Read one');expect(single).not.toContain('<select');
  });
});
