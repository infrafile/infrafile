import type { AssetCategory } from './contracts';
import { isAssetCategory } from './assetState';
export type WorkbenchPage = 'overview' | 'assets' | 'inspections' | 'changes';
export interface Scope { key: string; days: number; issue: string; category?: AssetCategory }
export interface PageProps {
  scope: Scope;
  onScopeChange: (scope: Scope) => void;
  onUnauthorized: () => void;
}

export function isAssetKey(key: string): boolean {
  return /^(Account|Server|Domain|Resource|App|Repo)\//.test(key);
}

export function readScope(hash: string): Scope {
  const params = new URLSearchParams(hash.replace(/^#/, ''));
  const raw = params.get('days') || '7';
  if (!/^\d+$/.test(raw) || !Number.isSafeInteger(Number(raw))) {
    throw Error('证据有效期须为非负整数天数。');
  }
  const category = params.get('category');
  if (category && !isAssetCategory(category)) throw Error('资产分类无效，请重新选择。');
  return { key: params.get('key') || '', days: Number(raw), issue: params.get('issue') || '', ...(category ? { category: category as AssetCategory } : {}) };
}

export function pageURL(page: WorkbenchPage, scope: Scope): string {
  const params = new URLSearchParams({ days: String(scope.days) });
  if (scope.key && (page === 'changes' || isAssetKey(scope.key))) params.set('key', scope.key);
  if (scope.issue) params.set('issue', scope.issue);
  if (page === 'assets' && scope.category) params.set('category', scope.category);
  return '/' + page + '#' + params.toString();
}
