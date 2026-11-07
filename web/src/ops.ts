export type OpsEvidence = { status: string; reason: string; observed_at: string | null; source: string; max_age_seconds: number; observed_status?: string; freshness?: string };
export type OpsServer = { key: string; name: string; role: string; hardware: Record<string, string>; snapshot: OpsEvidence; disks: { mount: string; total: number; available: number; percent: number }[]; applications: string[]; notice: string };
export type OpsProduction = { boundary: string; gates: { id: string; requirements: string[]; observations: { id: string; hosts: string[]; summary: string; reference: string; reference_sha256: string; evidence: OpsEvidence }[] }[] };
export type OpsApplication = { key: string; name: string; category: string; deployments: string[]; notice: string; production?: OpsProduction };
export type OpsCriterion = { id: string; component: string; kind: string; layer: string; workload_mode: string; data_mode: string; selector: string; observer: string; evidence: OpsEvidence };
export type OpsDeployment = { id: string; application: string; host: string; kind: string; selector: string; runtime: OpsEvidence; readiness: OpsEvidence; health: OpsEvidence; resources: string[]; checks: string[]; criteria?: OpsCriterion[]; notice: string };
export type OpsResource = { key: string; name: string; type: string; hosts: string[] };
export type OpsMonitor = { id: number; name: string; type: string; active: boolean | null; interval: number | null; targets: string[]; association_reason: string; configuration: OpsEvidence; state: OpsEvidence };
export type OpsReadCapability = { id: string; label: string; target: string; related_keys: string[]; description: string; timeout_seconds: number; max_age_seconds: number; min_interval_seconds: number; enabled: boolean; unavailable_reason?: string; saved_read?: boolean };
export type OpsReadSaved = { version: string; status: string; error_code: string; binding_matched: boolean; saved_at?: string; label?: string; result?: OpsReadResult };
export type OpsRecoveryState = { id: string; action_id: string; target: string; state: string; enabled: boolean; reason?: string };
export type OpsController = { kind: string; name: string; component: string; data_mode: string; workload_mode: string; checks: string[] };
export type OpsOperationRow = { id: string; kind: string; object: string; host: string; deployment?: string; controllers: OpsController[]; read_ids: string[]; action_ids: string[]; applications: string[]; shared_with: string[]; shared_controllers_with?: string[]; hosted_resources?: { key: string; consumers: { application: string; host: string; deployment: string }[] }[]; notice: string; boundary: string; prerequisites: string[] };
export type OpsReadSection = { id: string; title: string; status: string; fields: { label: string; value: string }[]; lines: string[] };
export type OpsReadResult = { version: string; request_id: string; action_id: string; target: string; status: string; error_code: string; started_at: string; finished_at: string; observed_at: string | null; duration_ms: number; max_age_seconds: number; sections: OpsReadSection[] };
export type OpsRefreshAvailability = { enabled: boolean; reason?: string; timeout_seconds: number };
export type OpsRefreshResult = { version: string; status: string; error_code: string; generation?: string; captured_at?: string };
export type OpsReport = { operations?: OpsOperationRow[]; recoveries?: OpsRecoveryState[]; refresh?: OpsRefreshAvailability; version: string; enabled: boolean; captured_at: string; evaluated_at: string; alignment: string; read_capabilities?: OpsReadCapability[]; action_capabilities?: OpsReadCapability[]; summary: { servers: number; applications: number; deployments: number; resources: number; expected_monitors: number; observed_monitors: number; runtime_identified: number }; servers: OpsServer[]; applications: OpsApplication[]; deployments: OpsDeployment[]; resources: OpsResource[]; monitors: OpsMonitor[]; collection_errors: string[] };

export const opsTime = (value: string | null) => value && Number.isFinite(Date.parse(value)) ? new Intl.DateTimeFormat('zh-CN', { timeZone: 'Asia/Shanghai', dateStyle: 'short', timeStyle: 'medium' }).format(new Date(value)) : '未采集';
export const opsName = (key: string) => key.slice(key.indexOf('/') + 1);
export const opsStatus: Record<string, string> = { PASS: '通过', WARN: '需关注', FAIL: '失败', UNKNOWN: '未知', STALE: '已过期', PAUSED: '已暂停', NA: '不适用' };
export const opsReason: Record<string, string> = {
  runtime_selector_missing: '尚未定位运行对象', fleet_snapshot_missing: '没有主机快照', fleet_snapshot_stale: '主机快照已过期',
  systemd_not_observed: '快照未观察到该服务，不能直接判为停机', container_not_observed: '快照未观察到该容器',
  docker_healthcheck_absent: '没有容器就绪判据', readiness_probe_not_defined: '未定义此运行方式的就绪判据',
  business_health_not_verified: '完整业务健康尚未核实', monitor_not_observed: '没有此监控的保存记录',
  monitor_identity_mismatch: '监控身份与预期不一致', monitor_not_in_policy: '新监控尚未关联',
  monitor_configuration_not_fresh: '监控配置观测缺失或过期', monitor_configuration_stale: '监控配置观测已过期',
  business_health_not_recomputed_no_remote_probes: '完整业务健康尚未核实，运行、按需观测与任务结果分别展示。',
  kuma_snapshot_missing: '没有 Kuma 结果缓存，不能判断监控当前状态。', unexpected_monitors_not_bound: '有新监控尚未关联到台账。',
  remote_probe_not_requested: '本轮未执行此判据，配置存在不代表检查通过。',
  fleet_snapshot_future: '主机快照时间异常，当前状态未知。', systemd_running: '已有快照观察到服务运行。',
  container_running: '已有快照观察到容器运行。', docker_healthy: '容器本地健康检查通过。',
};

export function opsMatches(query: string, ...values: string[]) {
  return values.some(value => value.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase()));
}

export function unverifiedOps(report: OpsReport): OpsReport {
  const unknown = (value: OpsEvidence): OpsEvidence => value.status === 'PAUSED' ? value : { ...value, observed_status: value.observed_status ?? value.status, status: 'UNKNOWN', reason: 'read_failed', freshness: 'unverified' };
  return { ...report, alignment: 'unverified', read_capabilities: report.read_capabilities?.map(cap => ({ ...cap, enabled: false, unavailable_reason: 'binding_invalid' })), action_capabilities: report.action_capabilities?.map(cap => ({ ...cap, enabled: false, unavailable_reason: 'source_changed' })), applications: report.applications.map(row => ({ ...row, production: row.production && { ...row.production, gates: row.production.gates.map(gate => ({ ...gate, observations: gate.observations.map(observation => ({ ...observation, evidence: unknown(observation.evidence) })) })) } })), servers: report.servers.map(row => ({ ...row, snapshot: unknown(row.snapshot) })), deployments: report.deployments.map(row => ({ ...row, runtime: unknown(row.runtime), readiness: unknown(row.readiness), health: unknown(row.health), criteria: row.criteria?.map(c => ({ ...c, evidence: unknown(c.evidence) })) })), monitors: report.monitors.map(row => ({ ...row, state: unknown(row.state), configuration: unknown(row.configuration) })) };
}
