'use strict';
const content = document.getElementById('content');
const workspace = document.getElementById('workspace');
const statusLine = document.getElementById('status');
const errorBox = document.getElementById('error');
const csrf = document.querySelector('meta[name="csrf-token"]').content;
const page = location.pathname === '/' ? 'assets' : location.pathname.slice(1);
let controller;
let view = { key: '', days: 7, issue: '' };
const filters = { search: '', kind: '', state: '' };
const words = { checked: '已检', unknown: '未知', not_applicable: '不适用', succeeded: '执行成功', failed: '执行失败', running: '执行中', confirmed: '已确认', confirmed_risk: '存在已确认风险', blocked: '尚未全部通过', clear: '已明确通过', open: '未恢复', recovered: '已恢复', out_of_scope: '已退出范围', opened: '首次发现', unconfirmed: '尚未确认', reopened: '再次发生', returned: '重新进入范围', waived: '已豁免', unwaived: '豁免结束', matched: '身份匹配', ambiguous: '身份不唯一', not_observed: '未观测到', incomplete: '集合不完整', identity_mismatch: '身份不匹配', unsupported: '尚不支持' };
const fieldNames = { provider: '厂商', account: '账号引用', dns_account: 'DNS 账号引用', address: '地址', private_address: '内网地址', region: '区域', instance_id: '实例 ID', resource_id: '资源 ID', type: '资源类型', endpoint: '端点', expires_at: '到期日', enabled: '期望启用', origin: '期望回源', deployments: '部署', bindings: '绑定关系', ssh: 'SSH 设置', hardware: '硬件', url: '地址', registrar: '注册商' };
function el(tag, text, className) {
    const node = document.createElement(tag);
    if (text !== undefined)
        node.textContent = text;
    if (className)
        node.className = className;
    return node;
}
function badge(value) {
    const bad = ['failed', 'confirmed', 'confirmed_risk', 'open', 'reopened'].includes(value);
    const warn = ['unknown', 'blocked', 'out_of_scope', 'not_applicable', 'ambiguous', 'not_observed', 'incomplete', 'identity_mismatch', 'unsupported', 'unconfirmed', 'waived'].includes(value);
    return el('span', words[value] || value, 'badge' + (bad ? ' bad' : warn ? ' warn' : ''));
}
function params() { return new URLSearchParams(location.hash.slice(1)); }
function selectedKey() { return view.key; }
function days() { return view.days; }
function readView() {
    const p = params();
    const raw = p.get('days') || '7';
    if (!/^\d+$/.test(raw) || !Number.isSafeInteger(Number(raw)))
        throw Error('证据有效期须为非负整数天数。');
    return { key: p.get('key') || '', days: Number(raw), issue: p.get('issue') || '' };
}
function isAssetKey(key) { return /^(Account|Server|Domain|Resource|App|Repo)\//.test(key); }
function url(targetPage, key = selectedKey(), identity) {
    const p = new URLSearchParams({ days: String(days()) });
    if (targetPage !== 'changes' && key && !isAssetKey(key))
        key = '';
    if (key)
        p.set('key', key);
    if (identity)
        p.set('issue', identity.fingerprint + '/' + identity.scope_hash);
    return '/' + targetPage + '#' + p.toString();
}
function link(text, targetPage, key, identity) { const a = el('a', text); a.href = url(targetPage, key, identity); return a; }
function box(title, parent = content) { const section = el('section'); section.append(el('h2', title)); parent.append(section); return section; }
function jsonDetails(title, value) { const d = el('details'); d.append(el('summary', title), el('pre', JSON.stringify(value, null, 2))); return d; }
function empty(parent, text) { parent.append(el('p', text, 'empty')); }
function timeLabel(value) { return value || '没有已验证的时间'; }
async function api(name, body = {}, signal = controller.signal) {
    const response = await fetch('/api/' + name, { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf }, body: JSON.stringify(body), signal, redirect: 'error' });
    if (response.status === 401)
        throw Error('会话已失效。请重新连接工作台。');
    if (!response.ok)
        throw Error('无法读取此范围（HTTP ' + response.status + '）。请核对台账、历史文件与选择条件后重试。');
    const result = JSON.parse(await response.text(), (key, value, context) => {
        if (typeof value === 'number' && !Number.isSafeInteger(value) && Number.isInteger(value)) {
            if (context?.source)
                return context.source;
            throw Error('浏览器无法无损显示此数值，请使用本地 CLI。');
        }
        return value;
    });
    if (signal.aborted)
        throw new DOMException('Cancelled', 'AbortError');
    return result;
}
function scopeBar(title) {
    content.append(el('h1', title));
    const bar = el('div', undefined, 'actions');
    bar.append(el('span', selectedKey() ? '当前范围：' + selectedKey() : '当前范围：全部资产'));
    if (selectedKey())
        bar.append(link('查看全部范围', page, ''));
    bar.append(link('前往资产选择范围', 'assets', selectedKey()));
    content.append(bar);
    const form = el('form');
    form.className = 'filters';
    const label = el('label', '证据有效期（天）');
    const input = el('input');
    input.type = 'number';
    input.min = '0';
    input.step = '1';
    input.value = String(days());
    input.required = true;
    label.append(input);
    const apply = el('button', '应用有效期');
    apply.type = 'submit';
    const refresh = el('button', '重新读取');
    refresh.type = 'button';
    refresh.addEventListener('click', load);
    form.append(label, apply, refresh);
    form.addEventListener('submit', event => { event.preventDefault(); const value = Number(input.value); if (!Number.isSafeInteger(value) || value < 0) {
        input.reportValidity();
        return;
    } const p = params(); p.set('days', String(value)); location.hash = p.toString(); });
    content.append(form);
}
function metrics(parent, entries) {
    const grid = el('div', undefined, 'metrics');
    for (const [label, value] of entries) {
        const item = el('div', label, 'metric');
        item.append(el('strong', String(value), 'count'));
        grid.append(item);
    }
    parent.append(grid);
}
function checksSection(report, parent = content) {
    const section = box('覆盖与证据', parent);
    metrics(section, [['应检', report.coverage.expected], ['已检', report.coverage.checked], ['未知', report.coverage.unknown], ['不适用', report.coverage.not_applicable]]);
    section.append(el('p', '未知、缺少证据和已豁免均不表示检查通过。证据有效期：' + days() + ' 天。', 'notice'));
    const label = el('label', '检查状态');
    const select = el('select');
    for (const [value, text] of [['', '全部状态'], ['unknown', '仅未知'], ['checked', '仅已检'], ['not_applicable', '仅不适用']]) {
        const option = el('option', text);
        option.value = value;
        select.append(option);
    }
    label.append(select);
    section.append(label);
    const list = el('ul', undefined, 'items');
    section.append(list);
    function render() {
        list.replaceChildren();
        const rows = report.coverage.checks.filter(c => !select.value || c.status === select.value);
        if (!rows.length) {
            empty(list, '此范围没有匹配的检查项。');
            return;
        }
        for (const check of rows) {
            const li = el('li');
            const heading = el('h3', check.rule + ' · ' + check.target);
            li.append(heading, badge(check.status), el('p', check.message), el('p', check.aspect + ' · ' + check.code, 'muted'));
            for (const evidence of check.evidence)
                li.append(el('p', '来源：' + (evidence.file || '无快照') + ' · 观测：' + timeLabel(evidence.observed_at) + ' · TTL：' + (evidence.max_age_days ?? '未设置') + ' 天', 'muted'));
            if (check.not_applicable)
                li.append(el('p', '不适用：' + check.not_applicable.reason + '；负责人 ' + check.not_applicable.owner + '；复核 ' + check.not_applicable.review_by, 'notice'));
            const a = isAssetKey(check.target) ? link('查看资产', 'assets', check.target) : link('查看全部巡检', 'inspections', '');
            li.append(a);
            list.append(li);
        }
    }
    select.addEventListener('change', render);
    render();
}
function historySection(history, parent = content) {
    const section = box('已保存的问题历史', parent);
    if (!history) {
        empty(section, '尚未保存问题历史。缺少历史不表示问题已经恢复；此页面不会自动推进历史。');
        return;
    }
    section.append(el('p', '最近显式检查：' + history.checked_at + '；变化游标 ' + history.last_sequence + '。以下状态与本次检查结果分开保存。', 'muted'));
    const list = el('ul', undefined, 'items');
    section.append(list);
    if (!history.issues.length)
        empty(list, '最近一次显式检查没有保存的问题；仍需核对当前覆盖缺口。');
    for (const state of history.issues) {
        const id = state.identity;
        const li = el('li');
        li.append(el('h3', id.key.rule + ' · ' + id.key.target), badge(state.lifecycle), el('span', ' '), badge(state.assessment), el('p', '首次发现：' + timeLabel(state.first_seen) + '；最近检查：' + timeLabel(state.last_checked) + '；发生 ' + state.occurrences + ' 次'));
        if (state.recovered_at)
            li.append(el('p', '最近恢复：' + state.recovered_at));
        li.append(link('查看此问题的变化', 'changes', id.key.target, id));
        list.append(li);
    }
}
async function assetsPage(signal) {
    scopeBar('资产');
    const data = await api('list', {}, signal);
    const form = el('form', undefined, 'filters');
    form.addEventListener('submit', event => event.preventDefault());
    const searchLabel = el('label', '搜索资产');
    const search = el('input');
    search.type = 'search';
    search.placeholder = '名称、主键或标签';
    search.value = filters.search;
    searchLabel.append(search);
    const kindLabel = el('label', '资产类型');
    const kind = el('select');
    for (const value of ['', ...new Set(data.records.map(r => r.kind))]) {
        const option = el('option', value || '全部类型');
        option.value = value;
        kind.append(option);
    }
    kind.value = filters.kind;
    kindLabel.append(kind);
    const stateLabel = el('label', '登记状态');
    const state = el('select');
    for (const [value, text] of [['', '全部状态'], ['active', '在用'], ['archived', '已归档']]) {
        const option = el('option', text);
        option.value = value;
        state.append(option);
    }
    state.value = filters.state;
    stateLabel.append(state);
    form.append(searchLabel, kindLabel, stateLabel);
    content.append(form);
    const layout = el('div', undefined, 'layout');
    const catalog = box('资产目录', layout);
    const count = el('p', undefined, 'count');
    const list = el('ul', undefined, 'assets');
    catalog.append(count, list);
    const detail = el('div');
    layout.append(detail);
    content.append(layout);
    function renderList() {
        Object.assign(filters, { search: search.value, kind: kind.value, state: state.value });
        list.replaceChildren();
        const needle = search.value.toLowerCase();
        const rows = data.records.filter(r => (!kind.value || r.kind === kind.value) && (!state.value || r.archived === (state.value === 'archived')) && (r.key + ' ' + Object.entries(r.labels || {}).map(([k, v]) => k + '=' + v).join(' ')).toLowerCase().includes(needle));
        count.textContent = '显示 ' + rows.length + ' / ' + data.records.length + ' 条资产';
        for (const row of rows) {
            const li = el('li');
            const a = link(row.key, 'assets', row.key);
            if (row.key === selectedKey())
                a.setAttribute('aria-current', 'true');
            a.append(el('small', (row.archived ? '已归档' : '在用') + ' · ' + Object.entries(row.labels || {}).map(([k, v]) => k + '=' + v).join(' · ')));
            li.append(a);
            list.append(li);
        }
        if (!rows.length)
            empty(list, '没有匹配的资产。请调整名称、类型或状态筛选。');
        if (selectedKey() && !rows.some(r => r.key === selectedKey())) {
            const note = el('li', '当前资产不在筛选结果中。', 'notice');
            const clear = el('button', '清除筛选');
            clear.type = 'button';
            clear.addEventListener('click', () => { search.value = ''; kind.value = ''; state.value = ''; renderList(); });
            note.append(clear);
            list.append(note);
        }
    }
    for (const input of [search, kind, state])
        input.addEventListener(input === search ? 'input' : 'change', renderList);
    renderList();
    if (!selectedKey()) {
        empty(detail, '选择一条资产查看登记期望、直接关系和最近观测。');
        return;
    }
    const [record, context] = await Promise.all([api('show', { key: selectedKey() }, signal), api('context', { key: selectedKey(), max_age_days: days() }, signal)]);
    const section = box(selectedKey(), detail);
    const title = section.querySelector('h2');
    title.tabIndex = -1;
    title.id = 'asset-title';
    title.focus();
    section.append(badge(record.record.metadata.archived ? '已归档' : '在用'));
    if (record.record.metadata.description)
        section.append(el('p', record.record.metadata.description));
    section.append(el('p', '登记文件：' + record.file, 'muted'));
    const actions = el('div', undefined, 'actions');
    actions.append(link('查看此资产巡检', 'inspections', selectedKey()), link('查看此资产变更', 'changes', selectedKey()));
    section.append(actions);
    const desired = box('登记期望', detail);
    const dl = el('dl');
    for (const [key, value] of Object.entries(record.record.spec)) {
        dl.append(el('dt', fieldNames[key] || key));
        const dd = el('dd');
        dd.append(typeof value === 'object' && value !== null ? el('pre', JSON.stringify(value, null, 2)) : el('span', String(value)));
        dl.append(dd);
    }
    desired.append(dl);
    const relations = box('直接关系', detail);
    const edges = [...record.outgoing.map(r => ({ ...r, direction: '引用' })), ...record.incoming.map(r => ({ ...r, direction: '被引用' }))];
    if (!edges.length)
        empty(relations, '没有登记的直接关系。');
    for (const edge of edges) {
        const p = el('p', edge.direction + ' ');
        p.append(link(edge.kind + '/' + edge.name, 'assets', edge.kind + '/' + edge.name), el('span', ' · ' + edge.path, 'muted'));
        relations.append(p);
    }
    const observed = box('最近保存的观测', detail);
    observed.append(el('p', '保存的值可能来自过期或失败前的快照；当前资格以每项检查为准。', 'notice'));
    if (!context.observations.length)
        empty(observed, '没有能与此资产明确关联的快照。请查看下方覆盖缺口。');
    for (const row of context.observations) {
        const check = context.inspection.coverage.checks.find(c => c.id === row.check);
        const item = el('div');
        item.append(el('h3', row.plugin + '/' + row.instance + ' · ' + row.service), badge(row.match));
        if (check)
            item.append(el('span', ' '), badge(check.status), el('p', check.message + ' [' + check.code + ']'));
        item.append(el('p', '来源：' + row.file + ' · 观测：' + timeLabel(row.observed_at), 'muted'));
        if (row.match === 'matched')
            item.append(jsonDetails('展开观测事实', row.values));
        observed.append(item);
    }
    checksSection(context.inspection, detail);
}
async function inspectionsPage(signal) {
    scopeBar('巡检');
    const report = await api('inspect', { ...(selectedKey() ? { key: selectedKey() } : {}), max_age_days: days() }, signal);
    const summary = box('当前检查概况');
    summary.append(badge(report.ok ? '已通过本范围检查' : report.risk.confirmed ? 'confirmed_risk' : 'blocked'), el('p', '报告时间：' + report.as_of + '。报告生成成功不等于全部通过；这里只评价所列检查。', 'muted'));
    const execution = box('最近执行');
    if (!report.execution.length)
        empty(execution, '此范围没有已绑定的执行实例。请核对覆盖缺口。');
    for (const run of report.execution) {
        const p = el('p', run.plugin + '/' + run.instance + ' ');
        p.append(badge(run.status), el('span', ' [' + run.code + ']；尝试 ' + timeLabel(run.attempted_at) + '；观测 ' + timeLabel(run.observed_at)));
        execution.append(p);
    }
    checksSection(report);
    const risk = box('当前风险');
    metrics(risk, [['已确认', report.risk.confirmed], ['历史或未知', report.risk.unknown], ['已豁免', report.risk.waived]]);
    if (!report.risk.findings.length)
        empty(risk, '没有发现项，仍需核对未知覆盖与最近执行。');
    for (const finding of report.risk.findings) {
        const item = el('div');
        item.append(el('h3', finding.rule + ' · ' + finding.target), badge(finding.assessment), el('p', finding.message));
        if (finding.waiver)
            item.append(el('p', '已豁免：' + finding.waiver.reason + '；到期 ' + (finding.waiver.until || '未指定'), 'notice'));
        item.append(jsonDetails('查看风险证据', finding.evidence));
        for (const identity of finding.issues || [])
            item.append(link('查看问题变化', 'changes', finding.target, identity));
        risk.append(item);
    }
    historySection(report.history);
    const notices = box('提示与缺口');
    if (!report.notices?.length)
        empty(notices, '没有额外提示。');
    for (const notice of report.notices || [])
        notices.append(el('p', notice.target + ' · ' + notice.message + ' [' + notice.code + ']', 'notice'));
}
async function changesPage(signal) {
    scopeBar('变更');
    const data = await api('changes', {}, signal);
    const section = box('问题变化时间线');
    section.append(el('p', '只展示已显式保存的变化；浏览不会刷新历史，也不表示云端发生了操作。', 'notice'));
    if (!data.present)
        empty(section, '尚无问题历史。请通过本地 CLI 显式检查并保存历史，缺少记录不表示没有问题。');
    else {
        section.append(el('p', '当前游标 ' + data.last_sequence + '；已清理至 ' + data.pruned_through + '。已清理的变化须查阅独立导出。', 'muted'));
        const issue = view.issue;
        const rows = data.changes.filter(e => (!selectedKey() || e.identity.key.target === selectedKey()) && (!issue || e.identity.fingerprint + '/' + e.identity.scope_hash === issue));
        if (!rows.length)
            empty(section, '此范围没有保留的变化，当前问题是否存在仍以巡检和已保存状态为准。');
        const list = el('ul', undefined, 'items');
        for (const event of rows.toReversed()) {
            const li = el('li');
            li.append(el('h3', '#' + event.sequence + ' · ' + event.identity.key.target), badge(event.type), el('p', event.at + ' · ' + event.identity.key.rule + ' · ' + event.identity.key.aspect), link('查看当前巡检', 'inspections', event.identity.key.target), jsonDetails('问题身份', event.identity));
            list.append(li);
        }
        section.append(list);
    }
    const inventory = box('台账修改');
    inventory.append(el('p', '台账变更预览与保存尚未接入工作台。可使用现有 Git 流程审阅台账修改。'));
    const cloud = box('云计划与执行回执');
    cloud.append(el('p', '受控记录尚未接入。请通过本地 CLI 审阅计划并按适用授权执行；保存台账不表示线上已经变更。'));
}
async function load() {
    controller?.abort();
    controller = new AbortController();
    const signal = controller.signal;
    content.replaceChildren();
    errorBox.hidden = true;
    statusLine.textContent = '正在读取台账…';
    content.setAttribute('aria-busy', 'true');
    try {
        view = readView();
        for (const a of document.querySelectorAll('nav a')) {
            a.href = url(a.dataset.page);
            if (a.dataset.page === page)
                a.setAttribute('aria-current', 'page');
            else
                a.removeAttribute('aria-current');
        }
        await ({ assets: assetsPage, inspections: inspectionsPage, changes: changesPage }[page] || assetsPage)(signal);
        if (!signal.aborted)
            statusLine.textContent = '已读取 · 离线只读视图 · 证据有效期 ' + days() + ' 天';
    }
    catch (error) {
        if (error.name === 'AbortError')
            return;
        if (signal.aborted)
            return;
        content.replaceChildren();
        errorBox.replaceChildren(el('p', error.message));
        errorBox.append(link('重新选择资产', 'assets', ''));
        const reconnect = el('a', '重新连接工作台');
        reconnect.href = '/';
        errorBox.append(el('span', ' · '), reconnect);
        errorBox.hidden = false;
        statusLine.textContent = '读取失败，未将旧内容显示为当前结果。';
    }
    finally {
        if (!signal.aborted)
            content.setAttribute('aria-busy', 'false');
    }
}
document.querySelector('.skip').addEventListener('click', event => { event.preventDefault(); workspace.focus(); });
window.addEventListener('hashchange', load);
load();
