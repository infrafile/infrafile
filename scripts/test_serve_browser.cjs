const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const http = require('node:http');
const https = require('node:https');
const crypto = require('node:crypto');
const { spawn, spawnSync, execFileSync } = require('node:child_process');
const { once } = require('node:events');
const { chromium } = require(process.env.INFRAFILE_PLAYWRIGHT_MODULE || 'playwright');
const assert = (condition, message) => { if (!condition)
    throw Error(message); };
const binary = path.resolve(process.env.INFRAFILE_BINARY || path.join(__dirname, '../bin/infrafile'));
const source = path.resolve(__dirname, '..');
const base = fs.mkdtempSync(path.join(process.env.INFRAFILE_BROWSER_OUTPUT_PARENT || os.tmpdir(), 'infrafile-browser-'));
fs.chmodSync(base, 0o700);
const repo = path.join(base, 'ledger');
fs.cpSync(path.join(source, 'examples/homelab'), repo, { recursive: true });
const digest = p => crypto.createHash('sha256').update(fs.readFileSync(p)).digest('hex');
function files(root) { const result = {}; function visit(dir) { for (const ent of fs.readdirSync(dir, { withFileTypes: true })) {
    const file = path.join(dir, ent.name);
    if (ent.isDirectory())
        visit(file);
    else
        result[path.relative(root, file)] = digest(file);
} } visit(root); return result; }
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
    if (!await page.getByRole('heading', { name: '已连接工作台', exact: true }).isVisible()) {
        const status = await session(page);
        throw Error('pair redirect session ' + status.status + ' in ' + path.basename(entry.pair));
    }
    await page.getByRole('heading', { name: '已连接工作台', exact: true }).waitFor();
}
async function session(page) { return page.evaluate(async () => { const r = await fetch('/api/session'); return { status: r.status, data: await r.json() }; }); }
async function listen(server) { server.listen(0, '127.0.0.1'); await once(server, 'listening'); return server.address().port; }
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
        report.checks.eight_native_browser_queries = queries;
        const second = await browser.newContext({ viewport: { width: 390, height: 844 } }), p2 = await second.newPage();
        await pair(p2, local);
        const a = await first.cookies(), b = await second.cookies();
        assert(a.find(c => c.name === 'infrafile_session').value !== b.find(c => c.name === 'infrafile_session').value, 'device sessions shared');
        assert((await session(p1)).status === 200, 'second device invalidated first');
        await p2.screenshot({ path: path.join(base, 'connected-narrow.png') });
        await p2.getByRole('button', { name: '撤销所有设备的会话', exact: true }).click();
        await p2.getByRole('heading', { name: '连接你的台账', exact: true }).waitFor();
        assert((await session(p1)).status === 401 && (await session(p2)).status === 401, 'revoke-all retained session');
        report.checks.independent_devices_and_revoke_all = true;
        await pair(p1, local);
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
main().catch(async (e) => { for (const c of children)
    if (!c.stopped)
        try {
            await stop(c);
        }
        catch { } ; const message = codes.reduce((text, code) => text.split(code).join('[redacted]'), e.message); fs.writeFileSync(path.join(base, 'failure.json'), JSON.stringify({ error: message }) + '\n', { mode: 0o600 }); console.error('browser acceptance failed:', message, 'evidence directory:', base); process.exitCode = 1; });
