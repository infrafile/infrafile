import { useState } from 'react';
import { opsMatches, opsName, type OpsOperationRow, type OpsReport } from './ops';

const controllerNames: Record<string, string> = { 'systemd-service': 'systemd服务', 'systemd-timer': 'systemd定时器', 'docker-container': '容器', process: '进程身份' };
const boundaryNames: Record<string, string> = { additional_control_not_bound: '仅列明的固定动作已绑定，其他控制未开放', host_control_not_bound: '整机变更未开放', planned_standby: '按计划备用，保持停用', service_control_not_bound: '服务启停与发布未开放', controller_unmapped: '尚无可靠控制对象映射', scheduled_control_not_bound: '定时任务的单次触发或排程变更未开放', stateful_control_not_bound: '有状态组件控制未开放' };
const prerequisiteNames: Record<string, string> = { exact_host_identity: '核对唯一机器身份', dependent_application_impact: '核对同机应用、依赖与可用副本', exact_component_identity: '核对具体组件与部署归属', restricted_execution_identity: '限定执行身份及固定参数', readiness_and_traffic_evidence: '核对就绪、实际接流量和可用副本', reviewed_preflight_and_rollback: '具备具体预检、结果判据与可执行回退', schedule_mutex_and_cost_limits: '核对排程、互斥、幂等与资源上限', roles_replication_and_restore_evidence: '核对角色、复制、持久化和恢复证据' };

export function OpsOperationScope({ row, report }: { row: OpsOperationRow; report: OpsReport }) {
  const reads = report.read_capabilities?.filter(cap => row.read_ids.includes(cap.id)) ?? [], actions = report.action_capabilities?.filter(cap => row.action_ids.includes(cap.id)) ?? [];
  return <section className="ops-operation-scope"><h3>{row.kind === 'server' ? '机器操作范围' : '部署操作范围'} · {opsName(row.host)}</h3>
    <p>{reads.length ? `已绑定${reads.filter(c => c.enabled).length}/${reads.length}项按需读取` : '未绑定现场读取'} · {actions.length ? `已绑定${actions.filter(c => c.enabled).length}/${actions.length}项受控动作` : '未绑定受控动作'}。绑定可用性与业务健康分别判断。</p>
    {row.notice && <p className="notice">{row.notice}</p>}
    {row.controllers.length > 0 && <p className="muted">组件与选择器来自已有判据配置；是否运行另看观测，进程名不等于唯一可控制实例。</p>}
    {row.controllers.length > 0 ? <ul>{row.controllers.map(c => <li key={c.kind + '/' + c.name + '/' + c.component + '/' + c.data_mode + '/' + c.workload_mode}>{controllerNames[c.kind] ?? c.kind}：<strong>{c.name}</strong> · {c.component} · {c.data_mode === 'stateful' ? '有状态' : c.data_mode === 'stateless' ? '无状态' : '数据模式未核实'}{c.workload_mode === 'scheduled' ? ' · 定时任务' : ''}<span className="ops-sub">判据：{c.checks.join('、')}</span></li>)}</ul> : <p className="muted">{row.kind === 'server' ? '整机能力不等于同机所有组件可控制。' : '未把代理、观察机或主运行名称猜成可控制的服务。'}</p>}
    <p>登记应用范围：{row.applications.map(opsName).join('、') || '暂无登记应用'}。</p>
    {row.kind === 'server' && <section aria-label="承载资源与登记使用方"><h4>承载资源与登记使用方</h4><p className="muted">来自本代台账的资源宿主和部署依赖；包括其他机器上的使用方，不代表完整流量依赖、可用副本或已验证的变更影响。多节点资源不据此判断主从或故障切换能力。</p>{row.hosted_resources === undefined ? <p>本版未包含资源使用方关系，影响范围尚未核对。</p> : row.hosted_resources.length === 0 ? <p>未登记此机器承载的资源，不能据此判断没有依赖。</p> : <ul>{row.hosted_resources.map(resource => <li key={resource.key}><strong>{opsName(resource.key)}</strong>{resource.consumers.length === 0 ? <p>未登记部署使用方，不代表闲置。</p> : <ul>{resource.consumers.map(consumer => <li key={consumer.deployment}>{opsName(consumer.application)} · {opsName(consumer.host)}{consumer.host === row.host ? '（同机）' : '（跨机依赖）'}<span className="ops-sub">部署标识：{consumer.deployment}</span></li>)}</ul>}</li>)}</ul>}</section>}
    {!!row.shared_controllers_with?.length && <p className="notice">共用同一控制对象的登记应用：{row.shared_controllers_with.map(opsName).join('、')}。操作该对象会同时涉及这些应用，不能作为独立实例处理。</p>}
    {row.shared_with.length > 0 && <p>共享依赖的其他登记应用：{row.shared_with.map(opsName).join('、')}。共享依赖不等于本次动作已验证的影响范围。</p>}
    <p className="notice">{boundaryNames[row.boundary] ?? '此控制范围尚未核实'}。受控入口仅代表其列明的固定动作；没有通用重启、部署或回滚授权。</p>
    <details><summary>开放上述控制前需核对</summary><ul>{row.prerequisites.map(key => <li key={key}>{prerequisiteNames[key] ?? '此项尚需明确核对'}</li>)}</ul><p className="muted">这些是尚缺的控制条件，不是已经通过的预检或回退承诺；具体动作仍需审阅其计划。</p></details>
  </section>;
}

export function OpsOperationCatalog({ report, search, onSelect }: { report: OpsReport; search: string; onSelect: (row: OpsOperationRow) => void }) {
  const [kind, setKind] = useState('all'), [availability, setAvailability] = useState('all');
  const reads = new Set(report.read_capabilities?.filter(c => c.enabled).map(c => c.id)), actions = new Set(report.action_capabilities?.filter(c => c.enabled).map(c => c.id));
  const rows = report.operations ?? [];
  return <section className="ops-operation-catalog" aria-label="机器与应用操作目录"><p>{report.operations ? `共${rows.filter(r => r.kind === 'server').length}台机器、${rows.filter(r => r.kind === 'deployment').length}项应用部署。` : '本代操作范围尚未核对。'}以下只展示已绑定入口与未开放原因，打开目录不会采集、预检或执行。</p><div className="ops-filters">
    <label>对象类型<select aria-label="操作对象类型" value={kind} onChange={e => setKind(e.target.value)}><option value="all">全部对象</option><option value="server">机器</option><option value="deployment">应用部署</option></select></label>
    <label>入口可用性<select aria-label="操作入口可用性" value={availability} onChange={e => setAvailability(e.target.value)}><option value="all">全部入口</option><option value="read">有按需读取</option><option value="action">有受控动作</option><option value="held">暂无可用入口</option></select></label>
  </div>{!report.operations && <p className="notice">本代未包含操作目录；不能据此推断操作权限。</p>}<div className="overview-table"><table><caption className="ops-caption">读取及执行范围来自本机固定绑定 · 暂无入口不等于故障 · 不是完整流量依赖清单</caption><thead><tr><th scope="col">对象 / 机器</th><th scope="col">登记组件与选择器</th><th scope="col">按需读取</th><th scope="col">受控动作</th><th scope="col">后续控制边界</th><th scope="col">详情</th></tr></thead><tbody>{rows.filter(row => {
    const canRead = row.read_ids.some(id => reads.has(id)), canAct = row.action_ids.some(id => actions.has(id));
    return (kind === 'all' || kind === row.kind) && opsMatches(search, row.object, row.host, ...row.controllers.map(c => c.name), ...(row.hosted_resources ?? []).flatMap(r => [r.key, ...r.consumers.flatMap(c => [c.application, c.host])])) && (availability === 'all' || availability === 'read' && canRead || availability === 'action' && canAct || availability === 'held' && !canRead && !canAct);
  }).map(row => <tr key={row.id}><th scope="row">{opsName(row.object)}<span className="ops-sub">{row.kind === 'server' ? '机器' : '应用部署'} · {opsName(row.host)}</span></th><td>{row.kind === 'server' ? `${row.applications.length}个登记应用` : row.controllers.length ? row.controllers.map(c => c.name).join('、') : '尚未可靠映射'}</td><td>{row.read_ids.some(id => reads.has(id)) ? '已有固定读取' : row.read_ids.length ? '绑定未通过核对' : '未开放'}</td><td>{row.action_ids.some(id => actions.has(id)) ? '已有固定动作' : row.action_ids.length ? '绑定未通过核对' : '未开放'}</td><td>{boundaryNames[row.boundary] ?? '尚未核实'}</td><td><button type="button" onClick={() => onSelect(row)} data-operation-id={row.id} aria-label={row.kind === 'server' ? `查看机器 ${opsName(row.host)} 的操作范围` : `查看 ${opsName(row.object)} 在 ${opsName(row.host)} 的操作范围`}>查看范围</button></td></tr>)}</tbody></table></div></section>;
}
