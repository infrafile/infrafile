import type { AssetDirectoryEntry, AssetSummary } from './contracts';

export interface AssetFilters { search: string; kind: string; archived: 'active' | 'archived' | 'all'; status: 'all' | 'risk' | 'pending' | 'passed' }
export const defaultFilters: AssetFilters = { search: '', kind: '', archived: 'active', status: 'all' };
export function filterDirectory<T extends AssetDirectoryEntry>(rows: T[], filters: AssetFilters): T[] {
  const search = filters.search.trim().toLowerCase();
  return rows.filter(row => (!filters.kind || row.kind === filters.kind)
    && (filters.archived === 'all' || row.archived === (filters.archived === 'archived'))
    && [row.key, row.name, ...Object.entries(row.labels ?? {}).flat()].join(' ').toLowerCase().includes(search));
}
export function assetStatus(row: AssetSummary): 'risk' | 'pending' | 'passed' {
  return row.risk.confirmed > 0 ? 'risk' : row.ok ? 'passed' : 'pending';
}
export function filterAssets(rows: AssetSummary[], filters: AssetFilters): AssetSummary[] {
  const rank = { risk: 0, pending: 1, passed: 2 };
  return filterDirectory(rows, filters).filter(row => filters.status === 'all' || assetStatus(row) === filters.status)
    .sort((a, b) => rank[assetStatus(a)] - rank[assetStatus(b)] || (a.key < b.key ? -1 : a.key > b.key ? 1 : 0));
}
export function selectAsset(rows: AssetDirectoryEntry[], selectedKey: string, cause: 'initial' | 'filter' | 'link') {
  const outside = !!selectedKey && !rows.some(row => row.key === selectedKey);
  const key = (!selectedKey || (outside && cause === 'filter')) ? rows[0]?.key ?? '' : selectedKey;
  return { key, outside: key === selectedKey && outside, changed: key !== selectedKey };
}
