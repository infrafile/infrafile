const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const http = require('node:http');
const https = require('node:https');
const crypto = require('node:crypto');
const { spawn, spawnSync, execFileSync } = require('node:child_process');
const { once } = require('node:events');
const { isDeepStrictEqual } = require('node:util');
const { chromium } = require(process.env.INFRAFILE_PLAYWRIGHT_MODULE || 'playwright');
const assert = (condition, message) => {
    if (!condition)
        throw Error(message);
};
const binary = path.resolve(process.env.INFRAFILE_BINARY || path.join(__dirname, '../bin/infrafile'));
const source = path.resolve(__dirname, '..');
const base = fs.mkdtempSync(path.join(process.env.INFRAFILE_BROWSER_OUTPUT_PARENT || os.tmpdir(), 'infrafile-browser-'));
fs.chmodSync(base, 0o700);
const repo = path.join(base, 'ledger');
fs.cpSync(path.join(source, 'examples/homelab'), repo, { recursive: true });
fs.appendFileSync(path.join(repo, 'infrafile.yaml'), '\nstorageVersion: v1\naudit:\n  watch:\n    - {target: Domain/example.net, plugin: cloudflare, instance: home, service: zones}\n');
const serverFile = path.join(repo, 'inventory/servers/web1.yaml');
fs.writeFileSync(serverFile, fs.readFileSync(serverFile, 'utf8').replace('跑博客和 wiki 的前端机', '<img src=x onerror=globalThis.__infrafileInjected=1>'));
execFileSync(binary, ['sync', 'example', '--dir', repo, '--json'], { stdio: ['ignore', 'pipe', 'pipe'] });
const missingBin = path.join(base, 'missing-bin');
fs.mkdirSync(missingBin, { mode: 0o700 });
const failed = spawnSync(binary, ['sync', 'example-python', '--dir', repo, '--json'], { env: { ...process.env, PATH: missingBin }, stdio: ['ignore', 'pipe', 'pipe'] });
assert(failed.status !== 0 && failed.status !== null, 'isolated missing-plugin fixture did not fail');
for (const name of ['_sync.yaml', '.sync-status.yaml']) {
    const file = path.join(repo, 'state', name);
    fs.writeFileSync(file, fs.readFileSync(file, 'utf8').replace(/\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ/g, '2020-01-01T12:00:00Z'));
}
execFileSync(binary, ['findings', 'refresh', repo, '--max-age', '7d', '--json'], { stdio: ['ignore', 'pipe', 'pipe'] });
const historyFixture = path.join(repo, 'state/.findings.json'), cursorBase = 9007199254740993n;
fs.writeFileSync(historyFixture, fs.readFileSync(historyFixture, 'utf8').replace(/"(sequence|last_sequence|pruned_through)"\s*:\s*(\d+)/g, (_, name, value) => '"' + name + '":' + (BigInt(value) + cursorBase)));
const digest = p => crypto.createHash('sha256').update(fs.readFileSync(p)).digest('hex');
function files(root) {
    const result = {};
    function visit(dir) {
        for (const ent of fs.readdirSync(dir, { withFileTypes: true })) {
            const file = path.join(dir, ent.name);
            if (ent.isDirectory())
                visit(file);
            else
                result[path.relative(root, file)] = digest(file);
        }
    }
    visit(root);
    return result;
}
const before = files(repo), children = [], codes = [], servers = [];
const report = { binary_sha256: digest(binary), version: JSON.parse(execFileSync(binary, ['version', '--json'], { encoding: 'utf8' })), checks: {} };
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
async function start(id, extra = []) {
    const pair = path.join(base, id + '.pair');
    const child = spawn(binary, ['serve', '--dir', repo, '--listen', '127.0.0.1:0', '--pair-file', pair, ...extra], { stdio: ['ignore', 'pipe', 'pipe'] });
    const entry = { child, pair, stdout: '', stderr: '', stopped: false };
    children.push(entry);
    child.stdout.on('data', b => entry.stdout += b);
    child.stderr.on('data', b => entry.stderr += b);
    for (let n = 0; n < 200; n++) {
        const match = entry.stderr.match(/工作台：(https?:\/\/[^\s]+)/);
        if (match) {
            entry.origin = match[1];
            return entry;
        }
        assert(child.exitCode === null, 'serve failed before readiness');
        await delay(25);
    }
    throw Error('serve readiness timeout');
}
async function stop(entry) {
    if (entry.stopped)
        return;
    const finished = once(entry.child, 'exit');
    entry.child.kill('SIGTERM');
    const timeout = setTimeout(() => entry.child.kill('SIGKILL'), 6500);
    const [code, signal] = await finished;
    clearTimeout(timeout);
    entry.stopped = true;
    assert(code === 0 && signal === null, 'serve did not stop gracefully');
    assert(!fs.existsSync(entry.pair), 'pair file retained after graceful shutdown');
}
async function pair(page, entry, origin = entry.origin) {
    await page.goto(origin + '/');
    const code = fs.readFileSync(entry.pair, 'utf8').trim();
    codes.push(code);
    assert(code.length >= 26, 'pair code missing');
    await page.getByLabel('配对码', { exact: true }).fill(code);
    const submitted = page.waitForResponse(r => r.url() === origin + '/pair');
    await page.getByRole('button', { name: '连接工作台', exact: true }).click();
    const response = await submitted;
    if (response.status() !== 303) {
        const headers = await response.request().allHeaders();
        console.log(JSON.stringify({ pair_request: { status: response.status(), origin: headers.origin, host: headers.host, fetch_site: headers['sec-fetch-site'], type: headers['content-type'], header_names: Object.keys(headers) } }));
        throw Error('pair response ' + response.status() + ' in ' + path.basename(entry.pair));
    }
    await page.waitForLoadState('domcontentloaded');
    if (!await page.getByRole('navigation', { name: '工作台', exact: true }).isVisible()) {
        const status = await session(page);
        throw Error('pair redirect session ' + status.status + ' in ' + path.basename(entry.pair));
    }
    await page.getByRole('navigation', { name: '工作台', exact: true }).waitFor();
}
async function session(page) { return page.evaluate(async () => { const r = await fetch('/api/session'); return { status: r.status, data: await r.json() }; }); }
async function listen(server) { server.listen(0, '127.0.0.1'); await once(server, 'listening'); return server.address().port; }
async function views(page, entry) {
    const failures = [];
    page.on('pageerror', error => failures.push(error.message));
    await page.goto(entry.origin + '/inspections');
    await page.getByRole('heading', { name: '已保存的问题历史', exact: true }).waitFor();
    assert(await page.getByText('执行失败', { exact: true }).count() > 0, 'failed execution hidden');
    assert(await page.locator('#content').innerText().then(text => text.includes('[stale]')), 'stale execution hidden');
    await page.goto(entry.origin + '/assets');
    await page.getByRole('heading', { name: '资产目录', exact: true }).waitFor();
    await page.getByLabel('搜索资产', { exact: true }).fill('web1');
    const asset = page.locator('.assets').getByRole('link', { name: /^Server\/web1/ });
    await asset.focus();
    await page.keyboard.press('Enter');
    await page.getByRole('heading', { name: '登记期望', exact: true }).waitFor();
    assert(!await page.evaluate(() => globalThis.__infrafileInjected), 'ledger description executed script');
    assert(await page.locator('body').innerText().then(text => text.includes('<img src=x onerror=')), 'ledger description not displayed as text');
    assert(!await page.locator('body').innerText().then(text => text.includes('sops://')), 'secret reference rendered');
    await page.getByRole('link', { name: 'Domain/example.net', exact: true }).first().click();
    await page.getByRole('heading', { name: '最近保存的观测', exact: true }).waitFor();
    await page.getByRole('button', { name: '清除筛选', exact: true }).click();
    await page.getByText('展开观测事实', { exact: true }).first().click();
    await page.screenshot({ path: path.join(base, 'asset-detail.png'), fullPage: true });
    await page.getByRole('link', { name: '查看此资产巡检', exact: true }).click();
    await page.getByRole('heading', { name: '已保存的问题历史', exact: true }).waitFor();
    assert(await page.getByText('未知', { exact: true }).count() > 0, 'coverage unknown hidden');
    await page.screenshot({ path: path.join(base, 'inspection.png'), fullPage: true });
    const change = page.getByRole('link', { name: '查看此问题的变化', exact: true }).first();
    assert(await change.count() > 0, 'saved issue did not expose changes navigation');
    await change.click();
    await page.getByRole('heading', { name: '问题变化时间线', exact: true }).waitFor();
    await page.locator('#status').filter({ hasText: '已读取' }).waitFor();
    assert(await page.locator('.items>li').count() > 0, 'issue timeline missing');
    assert(await page.locator('#content').innerText().then(text => text.includes('已清理至 ' + cursorBase)), 'large history cursor rounded');
    await page.screenshot({ path: path.join(base, 'changes.png'), fullPage: true });
    await page.setViewportSize({ width: 390, height: 844 });
    await page.screenshot({ path: path.join(base, 'changes-narrow.png'), fullPage: true });
    assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'narrow view overflows');
    await page.goto(entry.origin + '/assets#key=Domain%2Fexample.net');
    await page.getByRole('heading', { name: '最近保存的观测', exact: true }).waitFor();
    await page.screenshot({ path: path.join(base, 'asset-narrow.png'), fullPage: true });
    assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'asset view overflows');
    await page.setViewportSize({ width: 1080, height: 800 });
    let releaseOld;
    await page.route('**/api/context', async (route) => {
        if (route.request().postDataJSON().key === 'Server/web1') {
            await new Promise(resolve => releaseOld = resolve);
            try {
                await route.continue();
            }
            catch { }
        }
        else
            await route.continue();
    });
    await page.goto(entry.origin + '/assets#key=Server%2Fweb1');
    for (let n = 0; n < 100 && !releaseOld; n++)
        await delay(10);
    assert(!!releaseOld, 'slow request fixture not observed');
    await page.evaluate(() => location.hash = 'key=Domain%2Fexample.net');
    await page.getByRole('heading', { name: 'Domain/example.net', exact: true }).waitFor();
    releaseOld();
    await page.unroute('**/api/context');
    assert(await page.getByRole('heading', { name: 'Server/web1', exact: true }).count() === 0, 'old response replaced selected asset');
    const context = await page.evaluate(async () => { const csrf = document.querySelector('meta[name="csrf-token"]').content; const r = await fetch('/api/context', { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf }, body: JSON.stringify({ key: 'Domain/example.net', max_age_days: 7 }) }); return r.json(); });
    const cli = JSON.parse(execFileSync(binary, ['inspect', repo, '--key', 'Domain/example.net', '--max-age', '7d', '--json'], { encoding: 'utf8' }));
    delete cli.as_of;
    delete context.inspection.as_of;
    assert(isDeepStrictEqual(cli, context.inspection), 'browser context differs from CLI inspection');
    assert(context.observations.some(e => e.match === 'matched' && e.values.length > 0), 'matched observation facts missing');
    const historyFile = path.join(repo, 'state/.findings.json'), history = fs.readFileSync(historyFile);
    try {
        fs.unlinkSync(historyFile);
        await page.goto(entry.origin + '/changes');
        await page.getByText('尚无问题历史。请通过本地 CLI 显式检查并保存历史，缺少记录不表示没有问题。', { exact: true }).waitFor();
        fs.writeFileSync(historyFile, 'malformed-history');
        await page.goto(entry.origin + '/inspections');
        await page.locator('#error').filter({ hasText: '无法读取此范围' }).waitFor();
        assert(await page.locator('#content').innerText() === '', 'failed query retained old view');
        await page.goto(entry.origin + '/assets#key=Domain%2Fexample.net&days=invalid');
        await page.getByText('证据有效期须为非负整数天数。', { exact: true }).waitFor();
    }
    finally {
        fs.writeFileSync(historyFile, history, { mode: 0o600 });
    }
    assert(failures.length === 0, 'browser script failed');
    return { keyboard_asset_relationship_evidence_issue_changes: true, narrow_layout: true, description_escaped: true, cli_inspection_equal: true, failed_and_stale_visible: true, missing_and_corrupt_history_visible: true, large_cursor_lossless: true, old_request_cancellation: true };
}
async function main() {
    for (const args of [[], ['--listen', '0.0.0.0:0'], ['--pair-file', path.join(repo, 'code')]]) {
        const r = spawnSync(binary, ['serve', '--dir', repo, ...args], { encoding: 'utf8', timeout: 3000 });
        assert(r.status === 2, 'unsafe serve configuration accepted');
    }
    assert(!fs.existsSync(path.join(repo, 'code')), 'pair material written into ledger');
    report.checks.unsafe_startup_refused = true;
    const browser = await chromium.launch({ headless: true, executablePath: process.env.INFRAFILE_BROWSER_EXECUTABLE || undefined });
    try {
        const local = await start('local');
        const first = await browser.newContext({ viewport: { width: 1080, height: 800 } }), p1 = await first.newPage();
        await p1.goto(local.origin + '/');
        await p1.screenshot({ path: path.join(base, 'login.png') });
        const unauth = await session(p1);
        assert(unauth.status === 401, 'unauthenticated API accepted');
        await pair(p1, local);
        await p1.screenshot({ path: path.join(base, 'connected.png') });
        const state1 = await session(p1);
        assert(state1.status === 200, 'first session failed');
        const queries = await p1.evaluate(async (csrf) => {
            const result = {};
            for (const name of ['list', 'show', 'expiry', 'audit', 'inspect', 'validate', 'schema', 'capabilities']) {
                const r = await fetch('/api/' + name, { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf }, body: JSON.stringify(name === 'show' ? { key: 'Server/web1' } : {}) });
                const data = await r.json();
                result[name] = { status: r.status, version: data.version };
                if (name === 'show')
                    result[name].secrets_omitted = !('secrets' in data.record.spec);
            }
            return result;
        }, state1.data.csrf);
        assert(Object.values(queries).every(q => q.status === 200 && q.version === 'v1'), 'native browser query failed');
        assert(queries.show.secrets_omitted, 'secret references visible');
        report.checks.views = await views(p1, local);
        report.checks.eight_native_browser_queries = queries;
        const second = await browser.newContext({ viewport: { width: 390, height: 844 } }), p2 = await second.newPage();
        await pair(p2, local);
        const a = await first.cookies(), b = await second.cookies();
        assert(a.find(c => c.name === 'infrafile_session').value !== b.find(c => c.name === 'infrafile_session').value, 'device sessions shared');
        assert((await session(p1)).status === 200, 'second device invalidated first');
        await p2.screenshot({ path: path.join(base, 'connected-narrow.png') });
        await p2.getByText('会话与退出', { exact: true }).click();
        await p2.getByRole('button', { name: '撤销所有设备的会话', exact: true }).click();
        await p2.getByRole('heading', { name: '连接你的台账', exact: true }).waitFor();
        assert((await session(p1)).status === 401 && (await session(p2)).status === 401, 'revoke-all retained session');
        report.checks.independent_devices_and_revoke_all = true;
        await pair(p1, local);
        await p1.getByText('会话与退出', { exact: true }).click();
        await p1.getByRole('button', { name: '退出并撤销会话', exact: true }).click();
        await p1.getByRole('heading', { name: '连接你的台账', exact: true }).waitFor();
        assert((await session(p1)).status === 401, 'logout did not invalidate session');
        report.checks.logout = true;
        await pair(p1, local);
        await stop(local);
        const fresh = await start('restart');
        await p1.goto(fresh.origin + '/api/session');
        assert(await p1.evaluate(() => JSON.parse(document.body.textContent).error) === 'Unauthorized', 'restart retained session');
        report.checks.restart_revokes = true;
        await stop(fresh);
        const expiring = await start('expiry', ['--session-ttl', '2s']);
        await pair(p2, expiring);
        assert((await session(p2)).status === 200, 'short session not initially valid');
        await delay(2300);
        assert((await session(p2)).status === 401, 'session expiry ignored');
        await p2.goto(expiring.origin + '/');
        await p2.getByRole('heading', { name: '连接你的台账', exact: true }).waitFor();
        report.checks.expiry = true;
        await stop(expiring);
        const key = path.join(base, 'fixture.key'), cert = path.join(base, 'fixture.pem');
        execFileSync('openssl', ['req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-keyout', key, '-out', cert, '-days', '1', '-subj', '/CN=localhost'], { stdio: 'ignore' });
        fs.chmodSync(key, 0o600);
        let backend;
        const proxy = https.createServer({ key: fs.readFileSync(key), cert: fs.readFileSync(cert) }, (req, res) => {
            const headers = { ...req.headers, 'x-forwarded-proto': 'https' };
            for (const k of Object.keys(headers))
                if (k === 'forwarded' || k === 'x-real-ip' || (k.startsWith('x-forwarded-') && k !== 'x-forwarded-proto'))
                    delete headers[k];
            const r = http.request({ hostname: '127.0.0.1', port: new URL(backend).port, method: req.method, path: req.url, headers }, s => { res.writeHead(s.statusCode, s.headers); s.pipe(res); });
            r.on('error', () => { res.writeHead(502); res.end(); });
            req.pipe(r);
        });
        servers.push(proxy);
        const proxyPort = await listen(proxy), external = 'https://127.0.0.1:' + proxyPort;
        const probe = http.createServer();
        const backendPort = await listen(probe);
        await new Promise(resolve => probe.close(resolve));
        const remote = await start('https', ['--listen', '127.0.0.1:' + backendPort, '--external-url', external, '--trusted-proxy', '127.0.0.1']);
        backend = 'http://127.0.0.1:' + backendPort;
        const remoteContext = await browser.newContext({ ignoreHTTPSErrors: true }), rp = await remoteContext.newPage();
        await pair(rp, remote, external);
        const cookies = await remoteContext.cookies();
        assert(cookies.some(c => c.name === 'infrafile_session' && c.secure && c.httpOnly && c.sameSite === 'Strict'), 'HTTPS cookie flags failed');
        assert((await session(rp)).status === 200, 'HTTPS session failed');
        report.checks.https_proxy_and_secure_cookie = true;
        await rp.screenshot({ path: path.join(base, 'https-connected.png') });
        await remoteContext.close();
        await stop(remote);
        await new Promise(resolve => proxy.close(resolve));
        fs.unlinkSync(key);
        fs.unlinkSync(cert);
        await first.close();
        await second.close();
    }
    finally {
        await browser.close();
        for (const child of children)
            if (!child.stopped)
                await stop(child);
        for (const server of servers) {
            server.closeAllConnections();
            server.close();
        }
        for (const name of ['fixture.key', 'fixture.pem']) {
            const p = path.join(base, name);
            if (fs.existsSync(p))
                fs.unlinkSync(p);
        }
    }
    const after = files(repo);
    assert(JSON.stringify(before) === JSON.stringify(after), 'native query modified ledger');
    for (const e of children) {
        assert(e.stdout === '' && e.stderr === '工作台：' + e.origin + '\n', 'unexpected output or pairing material in ordinary logs');
    }
    report.checks.ledger_files_unchanged = Object.keys(before).length;
    report.checks.no_pair_codes_in_logs = true;
    report.checks.pair_files_cleaned = true;
    fs.writeFileSync(path.join(base, 'evidence.json'), JSON.stringify(report, null, 2) + '\n', { mode: 0o600 });
    console.log(JSON.stringify({ evidence: path.join(base, 'evidence.json'), binary_sha256: report.binary_sha256, checks: report.checks }));
}
main().catch(async (e) => {
    for (const c of children)
        if (!c.stopped)
            try {
                await stop(c);
            }
            catch { }
    ;
    const message = codes.reduce((text, code) => text.split(code).join('[redacted]'), e.message);
    fs.writeFileSync(path.join(base, 'failure.json'), JSON.stringify({ error: message }) + '\n', { mode: 0o600 });
    console.error('browser acceptance failed:', message, 'evidence directory:', base);
    process.exitCode = 1;
});
