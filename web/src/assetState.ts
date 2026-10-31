import type { AssetCategory, AssetDirectoryEntry, AssetSummary } from './contracts';

export const categoryLabels: Record<AssetCategory, string> = { risk: '有风险', needs_attention: '需处理', no_checks: '暂无检查', passed: '范围内通过' };
export function isAssetCategory(value: string): value is AssetCategory { return Object.hasOwn(categoryLabels, value); }
export interface AssetFilters { search: string; kind: string; archived: 'active' | 'archived' | 'all'; status: 'all' | AssetCategory }
export const defaultFilters: AssetFilters = { search: '', kind: '', archived: 'active', status: 'all' };
export function assetChanges(previous: AssetDirectoryEntry[], next: AssetDirectoryEntry[]): number {
  const before = new Map(previous.map(row => [row.key, JSON.stringify(row)]));
  const after = new Map(next.map(row => [row.key, JSON.stringify(row)]));
  return [...new Set([...before.keys(), ...after.keys()])].filter(key => before.get(key) !== after.get(key)).length;
}
export function filterDirectory<T extends AssetDirectoryEntry>(rows: T[], filters: AssetFilters): T[] {
  const search = filters.search.trim().toLowerCase();
  return rows.filter(row => (!filters.kind || row.kind === filters.kind)
    && (filters.archived === 'all' || row.archived === (filters.archived === 'archived'))
    && [row.key, row.name, ...Object.entries(row.labels ?? {}).map(([key, value]) => key + '=' + value)].join(' ').toLowerCase().includes(search));
}
export function assetStatus(row: AssetSummary): AssetCategory {
  return isAssetCategory(row.category) ? row.category : 'needs_attention';
}
export function filterAssets(rows: AssetSummary[], filters: AssetFilters): AssetSummary[] {
  const rank = { risk: 0, needs_attention: 1, no_checks: 2, passed: 3 };
  return filterDirectory(rows, filters).filter(row => filters.status === 'all' || assetStatus(row) === filters.status)
    .sort((a, b) => rank[assetStatus(a)] - rank[assetStatus(b)] || (a.key < b.key ? -1 : a.key > b.key ? 1 : 0));
}
export function selectAsset(rows: AssetDirectoryEntry[], selectedKey: string, cause: 'initial' | 'filter' | 'link') {
  const outside = !!selectedKey && !rows.some(row => row.key === selectedKey);
  const key = (!selectedKey || (outside && cause === 'filter')) ? rows[0]?.key ?? '' : selectedKey;
  return { key, outside: key === selectedKey && outside, changed: key !== selectedKey };
}
