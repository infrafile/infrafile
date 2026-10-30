import type { AssetDirectoryEntry, ExpiryReport } from './contracts';
import { query } from './api';

export const expiryKinds = ['Domain', 'Resource', 'Server'];
export function loadExpiry(signal: AbortSignal) {
  // Covers every four-digit schema date, while keeping the server's calendar-day result.
  return query<ExpiryReport>('expiry', { within_days: 3652500 }, signal);
}
export function summarizeExpiry(report: ExpiryReport, records: AssetDirectoryEntry[], within = 30) {
  const active = new Set(records.filter(row => !row.archived && expiryKinds.includes(row.kind)).map(row => row.key));
  const entries = report.entries.filter(entry => active.has(entry.kind + '/' + entry.name));
  return { total: active.size, registered: entries.length, missing: active.size - entries.length, due: entries.filter(entry => entry.days_left <= within).length };
}
