import { describe, expect, it } from 'vitest';
import type { AssetSummary } from './contracts';
import { filterAssets, filterDirectory, selectAsset, type AssetFilters } from './assetState';

const filters: AssetFilters = { search: '', kind: '', archived: 'active', status: 'all' };
const asset = (key: string, patch: Partial<AssetSummary> = {}): AssetSummary => ({
  key, kind: key.split('/')[0], name: key.split('/')[1], labels: null, archived: false, ok: false,
  coverage: { expected: 1, checked: 0, unknown: 1, not_applicable: 0 }, risk: { confirmed: 0, unknown: 0, waived: 0 },
  execution: [], reason: { code: 'unknown', message: '待核实' }, evidence: [], ...patch,
});
describe('asset selection', () => {
  const rows = [asset('Server/z', { ok: true }), asset('Domain/old', { archived: true }), asset('Server/b'), asset('Server/a'), asset('Domain/risk', { risk: { confirmed: 1, unknown: 2, waived: 0 } })];
  it('defaults to active assets, ordered risk, pending, passed and stable key', () => {
    expect(filterAssets(rows, filters).map(r => r.key)).toEqual(['Domain/risk', 'Server/a', 'Server/b', 'Server/z']);
  });
  it('combines kind, archive, status and case insensitive label/name/key search', () => {
    const labeled = asset('Server/Web', { labels: { env: 'Production' } });
    expect(filterAssets([...rows, labeled], { ...filters, search: 'PROD', kind: 'Server', status: 'pending' })).toEqual([labeled]);
    expect(filterAssets(rows, { ...filters, search: 'domain/', archived: 'archived' }).map(r => r.key)).toEqual(['Domain/old']);
    expect(filterAssets(rows, { ...filters, status: 'passed' }).map(r => r.key)).toEqual(['Server/z']);
  });
  it('keeps explicit outside links but changes selection when the user filters it out', () => {
    const filtered = filterAssets(rows, filters);
    expect(selectAsset(filtered, 'Server/outside', 'link')).toEqual({ key: 'Server/outside', outside: true, changed: false });
    expect(selectAsset(filtered, 'Server/outside', 'filter')).toEqual({ key: 'Domain/risk', outside: false, changed: true });
    expect(selectAsset(filtered, '', 'initial').key).toBe('Domain/risk');
    expect(selectAsset(filtered, 'Server/b', 'filter').key).toBe('Server/b');
    expect(selectAsset([], 'Server/a', 'filter').key).toBe('');
    expect(selectAsset([], 'Domain/old', 'link').outside).toBe(true);
  });
  it('preserves key=value label search in summaries and directory fallback', () => {
    const labeled = asset('Server/web', { labels: { env: 'production' } });
    const selection = { ...filters, search: 'ENV=production' };
    expect(filterAssets([labeled], selection)).toEqual([labeled]);
    expect(filterDirectory([labeled], selection)).toEqual([labeled]);
  });
});
