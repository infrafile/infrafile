export type JsonValue = null | boolean | number | string | JsonValue[] | { [key: string]: JsonValue };
export type JsonObject = { [key: string]: JsonValue };
export type LosslessInteger = number | string;
export interface Evidence { file: string; observed_at: string | null; max_age_days: number | null }
export interface Execution {
  plugin: string; instance: string; status: string; code: string;
  attempted_at?: string; observed_at: string | null; snapshot_file: string; receipt_file: string;
}
export interface Coverage { expected: number; checked: number; unknown: number; not_applicable: number }
export interface Risk { confirmed: number; unknown: number; waived: number }
export type AssetCategory = 'risk' | 'needs_attention' | 'no_checks' | 'passed';
export interface AssetSummary {
  key: string; kind: string; name: string; labels: Record<string, string> | null; archived: boolean; ok: boolean;
  coverage: Coverage; risk: Risk; execution: Execution[]; reason: { code: string; message: string }; evidence: Evidence[];
  category: string; hardware?: Partial<Record<'cpu' | 'memory' | 'disk' | 'os', string>>;
}
export interface AssetSummaries { version: string; as_of: string; max_age_days: number | null; rows: AssetSummary[] }
export type AssetDirectoryEntry = Pick<AssetSummary, 'key' | 'kind' | 'name' | 'labels' | 'archived'>;
export interface ListResult { version: string; records: AssetDirectoryEntry[] }
export interface ExpiryEntry { kind: string; name: string; file: string; expires_at: string; days_left: number }
export interface ExpiryReport { version: string; ok: boolean; as_of: string; within_days: number; entries: ExpiryEntry[] }
export interface Check {
  id: string; rule: string; target: string; plugin: string; instance: string; service: string; aspect: string;
  status: string; code: string; message: string; evidence: Evidence[];
  not_applicable?: { check: string; reason: string; owner: string; review_by: string };
}
export interface Waiver { reason: string; until?: string; source: string }
export interface IssueIdentity {
  fingerprint: string; scope_hash: string;
  key: { target: string; rule: string; aspect: string; [key: string]: JsonValue };
  [key: string]: JsonValue;
}
export interface Finding {
  rule: string; target: string; file: string; message: string; evidence: JsonObject;
  waiver: Waiver | null; assessment: string; issues?: IssueIdentity[];
}
export interface IssueState {
  identity: IssueIdentity; lifecycle: string; assessment: string; waiver: Waiver | null;
  first_seen: string; last_checked: string; last_confirmed?: string; recovered_at?: string; occurrences: LosslessInteger;
}
export interface HistoryView {
  checked_at: string; input_hash: string; issues: IssueState[];
  last_sequence: LosslessInteger; pruned_through: LosslessInteger;
}
export interface Inspection {
  version: string; ok: boolean; as_of: string; max_age_days: number | null;
  assets: { key: string; file: string; archived: boolean }[];
  relations: { from: string; to: string; path: string }[];
  execution: Execution[]; coverage: Coverage & { checks: Check[] }; risk: Risk & { findings: Finding[] };
  notices: { code: string; file: string; target: string; message: string; rule?: string }[];
  history?: HistoryView;
}
export interface AssetContext {
  version: string;
  records: { key: string; file: string; selected: boolean; record: JsonObject }[];
  inspection: Inspection;
  observations: { target: string; check: string; plugin: string; instance: string; service: string; file: string; observed_at: string | null; match: string; values: JsonValue[] }[];
  next_actions: JsonValue[];
}
export interface ChangesResult {
  version: string; present: boolean; last_sequence: LosslessInteger; pruned_through: LosslessInteger;
  changes: { sequence: LosslessInteger; identity: IssueIdentity; type: string; at: string }[];
}
export interface ExecutionIndex {
  id: string; target: string; operation: string; status: string; phase: string; updated_at: string;
  actor: string; authorization_ref: string; expires_at: string;
}
export interface ExecutionsResult { version: string; executions: ExecutionIndex[] }
export interface ExecutionResult { version: string; data: JsonObject }
export interface ShowResult { version: string; record: JsonObject }
export interface ChangeResult { version: string; status: string; code?: string; transaction?: string; commit?: string }
export interface ChangeStatus { version: string; enabled: boolean; transaction: ChangeResult }
export interface ChangeRequest { version: 'v1'; records: JsonValue[] }
export interface ChangePreview {
  version: string; ok: boolean; head: string; digest: string; problems: string[];
  files: { key: string; path: string; existed: boolean; before_hash: string; after_hash: string; before: JsonValue; after: JsonValue }[];
}
