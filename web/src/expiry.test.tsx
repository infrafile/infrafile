import { describe, expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import type { AssetDirectoryEntry, ExpiryEntry, ExpiryReport } from './contracts';
import { summarizeExpiry } from './expiry';
import { ExpirySection } from './ExpirySection';

const today = new Date();
const asOf = [today.getFullYear(), String(today.getMonth() + 1).padStart(2, '0'), String(today.getDate()).padStart(2, '0')].join('-');
const record = (name: string, archived = false, kind = 'Domain'): AssetDirectoryEntry => ({ key: `${kind}/${name}`, kind, name, archived, labels: null });
function entry(name: string, days: number, kind = 'Domain'): ExpiryEntry {
  const date = new Date(asOf + 'T00:00:00Z'); date.setUTCDate(date.getUTCDate() + days);
  return { kind, name, file: `inventory/${name}.yaml`, expires_at: date.toISOString().slice(0, 10), days_left: days };
}
const report = (entries: ExpiryEntry[]): ExpiryReport => ({ version: 'v1', ok: entries.length === 0, as_of: asOf, within_days: 3652500, entries });
const html = (entries: ExpiryEntry[], records = [record('example.net')]) => renderToStaticMarkup(<ExpirySection asset={records[0]} records={records} state={{ status: 'ready', data: report(entries) }} />);

describe('registered expiry presentation', () => {
  it.each([[-2, '已过期 2 天', '已过期'], [0, '今天到期', '30 天内到期'], [30, '剩余 30 天', '30 天内到期'], [31, '剩余 31 天', '未临近']])('shows server calendar day result %s without recomputing it', (days, text, status) => {
    const e = entry('example.net', Number(days));
    const output = html([e]);
    expect(output).toContain(e.expires_at);
    expect(output).toContain(text);
    expect(output).toContain(status);
    expect(output).toContain(asOf);
  });
  it('counts only eligible active records, including Domain, Resource and Server', () => {
    expect(summarizeExpiry(report([entry('example.net', 60)]), [record('example.net'), record('db1', false, 'Server'), record('cdn', false, 'Resource'), record('old', true), record('blog', false, 'App')])).toEqual({ total: 3, registered: 1, missing: 2, due: 0 });
  });
  it('distinguishes all registered but not due from missing dates', () => {
    expect(html([entry('example.net', 60)])).toContain('30 天内没有到期项');
    expect(html([entry('example.net', 60)])).toContain('已登记 1 / 共 1');
    expect(html([entry('example.net', 60)], [record('example.net'), record('missing.example.net')])).toContain('有 1 个未登记到期日，无法判断');
    expect(html([])).toContain('不代表安全/正常');
    expect(html([])).not.toContain('30 天内没有到期项');
  });
  it('does not count archived records or label them as missing', () => {
    expect(html([], [record('old.example.net', true)])).toContain('已归档，不纳入到期检查');
    expect(summarizeExpiry(report([]), [record('old', true)])).toEqual({ total: 0, registered: 0, missing: 0, due: 0 });
  });
  it('shows failure and loading without a successful empty state', () => {
    for (const state of [{ status: 'error' as const, error: Error('读取失败（HTTP 503）') }, { status: 'loading' as const }]) {
      const output = renderToStaticMarkup(<ExpirySection asset={record('example.net')} records={[record('example.net')]} state={state} />);
      expect(output).toContain(state.status === 'error' ? '到期信息读取失败' : '正在读取到期信息');
      expect(output).not.toContain('没有到期项');
      expect(output).not.toContain('已登记');
    }
  });
});
