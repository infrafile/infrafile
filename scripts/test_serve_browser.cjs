const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const http = require('node:http');
const https = require('node:https');
const crypto = require('node:crypto');
const { spawn, spawnSync, execFileSync } = require('node:child_process');
const { once } = require('node:events');
const { isDeepStrictEqual } = require('node:util');
const playwright = require(process.env.INFRAFILE_PLAYWRIGHT_MODULE || path.join(__dirname, '../web/node_modules/playwright'));
const engine = process.env.INFRAFILE_BROWSER_ENGINE || 'chromium';
const suite = process.env.INFRAFILE_BROWSER_SUITE || 'all';
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
    const child = spawn(binary, ['serve', '--dir', repo, '--listen', '127.0.0.1:0', '--pair-file', pair, ...extra], { stdio: ['ignore', 'pipe', 'pipe'], env: suite === 'package' ? { ...process.env, PATH: missingBin } : process.env });
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
    try { await page.getByRole('navigation', { name: '工作台', exact: true }).waitFor({ timeout: 5000 }); }
    catch {
        const status = await session(page);
        throw Error('pair redirect session ' + status.status + ' in ' + path.basename(entry.pair));
    }
    await page.getByRole('navigation', { name: '工作台', exact: true }).waitFor();
}
async function session(page) { return page.evaluate(async () => { const r = await fetch('/api/session'); return { status: r.status, data: await r.json() }; }); }
async function listen(server) { server.listen(0, '127.0.0.1'); await once(server, 'listening'); return server.address().port; }
async function cspViews(page, entry) {
    const errors = [], external = [];
    page.on('pageerror', error => errors.push(error.message));
    page.on('request', request => { if (!request.url().startsWith(entry.origin + '/')) external.push(request.url()); });
    await page.addInitScript(() => {
        globalThis.cspViolations = [];
        document.addEventListener('securitypolicyviolation', e => globalThis.cspViolations.push({ directive: e.violatedDirective, blocked: e.blockedURI, source: e.sourceFile, line: e.lineNumber, target: e.target instanceof Element ? e.target.outerHTML : String(e.target), styles: [...document.querySelectorAll('style')].map(style => style.textContent) }));
    });
    await page.route(entry.origin + '/assets', async route => {
        const response = await route.fetch();
        await route.fulfill({ response, body: '<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><link rel="stylesheet" href="/app.css"><script type="module" src="/app.js"></script></head><body><div id="csp-root"></div></body></html>' });
    });
    for (const name of ['app.js', 'app.css']) {
        await page.route(entry.origin + '/' + name, async route => {
            const response = await route.fetch();
            await route.fulfill({ response, body: fs.readFileSync(path.join(source, 'web/.csp', name)) });
        });
    }
    page.setDefaultTimeout(5000);
    await page.goto(entry.origin + '/assets');
    await page.getByRole('heading', { name: '组件兼容性' }).waitFor();
    await page.getByRole('textbox', { name: '搜索资产' }).fill('server');
    await page.getByRole('checkbox', { name: '已审阅' }).check();
    await page.getByRole('radio', { name: '有风险', exact: true }).click();
    assert(await page.getByRole('radio', { name: '有风险', exact: true }).getAttribute('aria-checked') === 'true', 'selection lost');
    await page.getByText('会话菜单', { exact: true }).click();
    await page.getByRole('button', { name: '菜单项目' }).waitFor();
    await page.keyboard.press('Escape');
    await page.getByRole('button', { name: '菜单项目' }).waitFor({ state: 'hidden' });
    await page.getByRole('button', { name: '打开详情' }).click();
    await page.getByRole('dialog').waitFor();
    await page.keyboard.press('Escape');
    await page.getByRole('dialog').waitFor({ state: 'hidden' });
    await page.waitForFunction(() => document.activeElement?.textContent === '打开详情');
    assert(await page.getByRole('button', { name: '打开详情' }).evaluate(el => el === document.activeElement), 'dialog did not restore focus');
    for (const colorScheme of ['dark', 'light']) {
        await page.emulateMedia({ colorScheme });
        await page.getByTestId('appearance').filter({ hasText: colorScheme }).waitFor();
    }
    const violations = await page.evaluate(() => globalThis.cspViolations);
    assert(violations.length === 0, 'CSP violations: ' + JSON.stringify(violations));
    assert(errors.length === 0, 'component scripts failed: ' + errors.join(';'));
    assert(external.length === 0, 'external asset dependency');
    return { engine, components: true, csp: true, keyboard: true, themes: true };
}
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
    const asset = page.getByRole('option', { name: /Server\/web1/ });
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
async function assetViews(page, entry) {
    let summaries = 0, contexts = 0;
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    page.on('request', request => {
        if (request.url().endsWith('/api/asset-summaries')) summaries++;
        if (request.url().endsWith('/api/context')) contexts++;
    });
    await page.addInitScript(() => {
        globalThis.__violations = [];
        document.addEventListener('securitypolicyviolation', event => globalThis.__violations.push({ directive: event.violatedDirective, blocked: event.blockedURI, source: event.sourceFile, line: event.lineNumber }));
    });
    await page.goto(entry.origin + '/assets');
    await page.getByRole('heading', { name: '资产目录', exact: true }).waitFor();
    await page.getByRole('heading', { name: '登记期望', exact: true }).waitFor();
    assert(summaries === 1 && contexts === 1, 'initial assets caused per-asset requests');
    const initialScope = await page.evaluate(() => location.hash);
    await page.getByRole('link', { name: '跳到内容', exact: true }).focus();
    await page.keyboard.press('Enter');
    assert(await page.evaluate(() => document.activeElement.id === 'workspace'), 'skip link did not focus content');
    assert(await page.evaluate(() => location.hash) === initialScope, 'skip link changed asset scope');
    await page.getByLabel('搜索资产', { exact: true }).fill('web1');
    const item = page.getByRole('option', { name: /Server\/web1/ });
    await item.focus(); await page.keyboard.press('Enter');
    await page.getByRole('heading', { name: 'Server/web1', exact: true }).waitFor();
    assert(await page.locator('body').innerText().then(t => t.includes('<img src=x onerror=')), 'description hidden or interpreted');
    assert(!await page.evaluate(() => globalThis.__infrafileInjected), 'ledger HTML executed');
    await page.getByRole('link', { name: 'Domain/example.net', exact: true }).first().click();
    await page.getByText('此资产不在当前筛选范围内。', { exact: true }).waitFor();
    await page.getByRole('heading', { name: 'Domain/example.net', exact: true }).waitFor();
    await page.getByRole('button', { name: '清除筛选', exact: true }).click();
    await page.getByText('展开观测事实', { exact: true }).first().click();
    for (const width of [1440, 1080, 390]) {
        await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
        for (const theme of ['浅色', '深色']) {
            await page.getByRole('radio', { name: theme, exact: true }).click();
            await page.waitForFunction(value => document.documentElement.dataset.theme === value, theme === '深色' ? 'dark' : 'light');
            await page.locator('.asset-detail').evaluate(element => element.scrollTop = 0);
            await page.waitForFunction(dark => {
                const color = getComputedStyle(document.querySelector('.asset-detail')).backgroundColor;
                const red = Number(color.match(/[\d.]+/)[0]);
                return dark ? red < 80 : red > 200;
            }, theme === '深色');
            assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'asset layout overflows');
        }
    }
    await page.getByRole('button', { name: '返回资产列表', exact: true }).click();
    assert(await page.getByRole('option', { name: /Domain\/example.net/ }).getAttribute('aria-selected') === 'true', 'narrow back lost selection');
    const selected = page.getByRole('option', { name: /Domain\/example.net/ });
    await selected.focus(); await page.keyboard.press('Enter');
    await page.locator('.asset-detail').waitFor({ state: 'visible' });
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(resolve)));
    const keyboardFocus = await page.evaluate(() => document.querySelector('.asset-detail').contains(document.activeElement));
    await page.getByRole('button', { name: '返回资产列表', exact: true }).click();
    await selected.click();
    await page.locator('.asset-detail').waitFor({ state: 'visible' });
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(resolve)));
    const pointerFocus = await page.evaluate(() => document.querySelector('.asset-detail').contains(document.activeElement));
    assert(keyboardFocus && pointerFocus, 'narrow entry did not move focus into detail');
    await page.getByRole('button', { name: '返回资产列表', exact: true }).click();
    await page.setViewportSize({ width: 1080, height: 800 });
    await page.getByRole('option', { name: /Domain\/example.net/ }).focus();
    await page.keyboard.press('ArrowDown');
    assert(await page.evaluate(() => document.activeElement.getAttribute('role') === 'option'), 'arrow key moved focus into detail');
    for (const status of [200, 500]) {
        let release, done;
        const finished = new Promise(resolve => done = resolve);
        await page.route('**/api/context', async route => {
            if (route.request().postDataJSON().key !== 'Server/web1') return route.continue();
            const response = await route.fetch();
            await new Promise(resolve => release = resolve);
            try { await route.fulfill({ response, status }); } catch {} finally { done(); }
        });
        await page.goto(entry.origin + '/assets#key=Server%2Fweb1');
        for (let n = 0; n < 200 && !release; n++) await delay(10);
        assert(release, 'delayed request not reached');
        await page.evaluate(() => location.hash = 'key=Domain%2Fexample.net');
        await page.getByRole('heading', { name: 'Domain/example.net', exact: true }).waitFor();
        release(); await finished;
        assert(await page.getByRole('heading', { name: 'Server/web1', exact: true }).count() === 0, 'late success/error replaced selection');
        await page.unroute('**/api/context');
    }
    await page.route('**/api/asset-summaries', route => route.fulfill({ status: 503, body: '{}' }));
    await page.getByRole('button', { name: '重新读取', exact: true }).click();
    await page.getByText('摘要读取失败，仅显示资产目录。状态未读取。', { exact: true }).waitFor();
    assert(await page.getByRole('radio', { name: '有风险', exact: true }).isDisabled(), 'status filter enabled without summaries');
    assert(await page.locator('.asset-list').innerText().then(t => !t.includes('范围内通过')), 'fallback retained old green statuses');
    await page.unroute('**/api/asset-summaries');
    await page.route('**/api/context', route => route.fulfill({ status: 401, body: '{}' }));
    await page.getByRole('button', { name: '重新读取', exact: true }).click();
    await page.getByRole('link', { name: '重新连接工作台', exact: true }).waitFor();
    assert(await page.getByRole('listbox').count() === 0 && await page.locator('pre').count() === 0, '401 left business data visible');
    await page.unroute('**/api/context');
    assert(errors.length === 0, 'asset page errors: ' + errors.join(','));
    const violations = await page.evaluate(() => globalThis.__violations);
    assert(violations.length === 0, 'asset page violated CSP: ' + JSON.stringify(violations));
    // Capture separately: Playwright injects `body {}` for every WebKit screenshot.
    // CSP assertions above cover application interactions without that test-only injection.
    await page.goto(entry.origin + '/assets#key=Domain%2Fexample.net');
    await page.reload();
    await page.getByRole('heading', { name: 'Domain/example.net', exact: true }).waitFor();
    for (const width of [1440, 1080, 390]) {
        await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
        for (const theme of ['浅色', '深色']) {
            await page.getByRole('radio', { name: theme, exact: true }).click();
            await page.waitForFunction(dark => document.querySelector('.radix-themes').classList.contains(dark ? 'dark' : 'light'), theme === '深色');
            await page.screenshot({ path: path.join(base, `assets-${width}-${theme}.png`), caret: 'initial' });
        }
    }
    return { summary_batch: true, keyboard: true, outside_link: true, narrow_return: true, race_success_and_failure: true, fallback: true, unauthorized_clears: true, themes: true, csp: true };
}
async function concurrentAuthViews(page, entry) {
    for (const [first, late] of [['changes', 'executions'], ['executions', 'changes']]) {
        let release;
        await page.route('**/api/' + first, route => route.fulfill({ status: 422, body: '{}' }));
        await page.route('**/api/' + late, async route => {
            await new Promise(resolve => release = resolve);
            await route.fulfill({ status: 401, body: '{}' });
        });
        try {
            await page.goto(entry.origin + '/changes');
            await page.locator('#error').filter({ hasText: 'HTTP 422' }).waitFor();
            for (let n = 0; n < 100 && !release; n++) await delay(10);
            assert(release, 'parallel query not issued'); release();
            await page.getByRole('link', { name: '重新连接工作台', exact: true }).waitFor({ timeout: 5000 });
            assert(await page.getByRole('navigation', { name: '工作台', exact: true }).count() === 0, 'late 401 retained business root');
        } finally {
            release?.();
            await page.unroute('**/api/' + first); await page.unroute('**/api/' + late);
        }
    }
    return { late_401_after_ordinary_failure: true, both_parallel_orders: true };
}
async function editViews(browser) {
    const editRepo = path.join(base, 'edit-ledger');
    execFileSync(binary, ['init', editRepo], { stdio: 'pipe' });
    fs.appendFileSync(path.join(editRepo, 'infrafile.yaml'), 'storageVersion: v1\n');
    const record = { apiVersion: 'infrafile/v1alpha1', kind: 'Server', metadata: { name: 'web' }, spec: { address: '192.0.2.10' } };
    const file = path.join(editRepo, 'inventory/servers/web.yaml');
    fs.writeFileSync(file, JSON.stringify(record));
    const git = args => execFileSync('git', ['-C', editRepo, ...args], { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).trim();
    git(['init', '-b', 'main']);
    for (const [key, value] of Object.entries({ 'user.name': 'Infrafile Browser', 'user.email': 'infrafile@example.com', 'commit.gpgSign': 'false', 'core.hooksPath': path.join(editRepo, '.git/hooks') })) git(['config', key, value]);
    git(['add', '.']); git(['commit', '-m', 'fixture']);
    const original = git(['rev-parse', 'HEAD']);
    let app = await start('edit', ['--dir', editRepo, '--allow-edit']);
    const first = await browser.newContext(), second = await browser.newContext();
    const a = await first.newPage(), b = await second.newPage();
    await pair(a, app); await pair(b, app);
    async function prepare(page, address) {
        const target = app.origin + '/changes#key=Server%2Fweb';
        if (page.url() === target) await page.getByRole('button', { name: '检查事务状态并读取最新台账', exact: true }).click();
        else await page.goto(target);
        const editor = page.getByLabel('完整记录 JSON', { exact: true }); await editor.waitFor();
        const replacement = JSON.parse(await editor.inputValue()); replacement.spec.address = address;
        await editor.fill(JSON.stringify(replacement, null, 2));
        const response = page.waitForResponse(r => r.url() === app.origin + '/api/change/preview');
        await page.getByRole('button', { name: '校验并预览变更', exact: true }).click();
        const result = await response; assert(result.ok(), 'browser preview failed');
        await page.getByText('预览摘要：', { exact: false }).waitFor();
        return { value: await result.json(), replacement };
    }
    async function save(page) {
        await page.getByLabel('我已核对修改前后的完整内容', { exact: true }).check();
        await page.getByRole('button', { name: '确认并提交到 Git', exact: true }).press('Enter');
    }
    const p = await prepare(a, '192.0.2.11'); await prepare(b, '192.0.2.12');
    await a.getByRole('checkbox', { name: '我已核对修改前后的完整内容', exact: true }).check();
    const edited = a.getByLabel('完整记录 JSON', { exact: true });
    await edited.fill(await edited.inputValue() + '\n');
    assert(!await a.getByRole('checkbox', { name: '我已核对修改前后的完整内容', exact: true }).isChecked(), 'changed preview stayed reviewed');
    assert(await a.getByRole('button', { name: '确认并提交到 Git', exact: true }).isDisabled(), 'changed preview retained approval');
    await a.getByRole('button', { name: '校验并预览变更', exact: true }).click();
    await a.getByText('修改后', { exact: true }).waitFor();
    const requestFile = path.join(base, 'edit-request.json');
    fs.writeFileSync(requestFile, JSON.stringify({ version: 'v1', records: [p.replacement] }), { mode: 0o600 });
    const cli = JSON.parse(execFileSync(binary, ['change', 'preview', editRepo, '--request', requestFile, '--json'], { encoding: 'utf8' }));
    assert(cli.files[0].before_hash === p.value.files[0].before_hash && cli.files[0].after_hash === p.value.files[0].after_hash, 'CLI and Web differ in reviewed bytes');
    await a.setViewportSize({ width: 390, height: 844 });
    await a.screenshot({ path: path.join(base, 'edit-preview-narrow.png'), fullPage: true });
    assert(await a.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'edit preview overflows narrow viewport');
    await save(a); await a.getByText('已提交到本地 Git', { exact: true }).waitFor();
    await save(b); await b.getByText('变更被拒绝，请重新预览', { exact: true }).waitFor();
    assert(git(['rev-parse', 'HEAD^']) === original && git(['status', '--porcelain']) === '', 'browser commit included extra files');
    assert(fs.readFileSync(file, 'utf8').includes('192.0.2.11'), 'stale browser replaced the first commit');
    await prepare(a, '192.0.2.13');
    fs.writeFileSync(path.join(editRepo, '.git/hooks/pre-commit'), '#!/bin/sh\nexit 1\n', { mode: 0o700 });
    await save(a); await a.getByText('事务未完成，需要恢复', { exact: true }).waitFor();
    await stop(app); app = await start('edit-restart', ['--dir', editRepo, '--allow-edit']);
    await pair(a, app); await a.goto(app.origin + '/changes');
    await a.getByRole('button', { name: '恢复此事务', exact: true }).click();
    await a.getByText('已恢复原文件', { exact: true }).waitFor();
    await a.getByRole('button', { name: '检查事务状态并读取最新台账', exact: true }).click();
    await a.getByLabel('完整记录 JSON', { exact: true }).waitFor();
    assert(fs.readFileSync(file, 'utf8').includes('192.0.2.11') && git(['status', '--porcelain']) === '', 'browser restart recovery changed committed state');
    await a.screenshot({ path: path.join(base, 'edit-recovered.png'), fullPage: true });
    fs.unlinkSync(path.join(editRepo, '.git/hooks/pre-commit'));
    await prepare(a, '192.0.2.14');
    let applyCalls = 0, statusCalls = 0;
    await a.route('**/api/change/apply', async route => { applyCalls++; await route.fetch(); await route.abort('failed'); });
    await a.route('**/api/change/status', async route => { statusCalls++; await route.continue(); });
    await save(a);
    await a.getByText(/请先检查事务状态，避免盲目重试/).waitFor();
    assert(await a.getByRole('button', { name: '确认并提交到 Git', exact: true }).isDisabled(), 'uncertain submission can be resent');
    await a.getByRole('button', { name: '检查事务状态并读取最新台账', exact: true }).click();
    await a.getByLabel('完整记录 JSON', { exact: true }).waitFor();
    assert(applyCalls === 1 && statusCalls > 0, 'lost reply did not require status read');
    await a.unroute('**/api/change/apply'); await a.unroute('**/api/change/status');
    // An unsafe number in the raw show response must never become a string in an editable record.
    await a.route('**/api/show', async route => { const response = await route.fetch(); await route.fulfill({ response, body: (await response.text()).replace('{', '{"unsafe":9007199254740993,') }); });
    await a.reload();
    await a.getByText(/此数值超出浏览器精度，请通过 CLI 编辑/).waitFor();
    assert(await a.getByLabel('完整记录 JSON', { exact: true }).count() === 0, 'unsafe numeric response became editable');
    await first.close(); await second.close(); await stop(app);
    return { cli_web_byte_diff_equal: true, explicit_review_and_commit: true, stale_second_session_refused: true, hook_failure_restart_recovery: true, narrow_keyboard_flow: true, changed_preview_invalidated: true, lost_reply_status_before_retry: true, unsafe_edit_number_rejected: true };
}
async function executionViews(browser) {
    const executionRepo = path.join(base, 'execution-ledger'), pluginDir = path.join(base, 'plugins');
    execFileSync(binary, ['init', executionRepo], { stdio: 'pipe' }); fs.mkdirSync(pluginDir, { mode: 0o700 });
    fs.writeFileSync(path.join(executionRepo, 'infrafile.yaml'), 'apiVersion: infrafile/v1alpha1\nkind: Config\nstorageVersion: v1\nplugins:\n  browser-control:\n    instances:\n      home: {}\n');
    fs.writeFileSync(path.join(executionRepo, 'inventory/resources/edge.yaml'), JSON.stringify({ apiVersion: 'infrafile/v1alpha1', kind: 'Resource', metadata: { name: 'edge' }, spec: { provider: 'browser-control', type: 'cdn', control: { plugin: 'browser-control', instance: 'home', zone_id: 'example-zone' } } }));
    const fake = `#!${process.execPath}
const fs=require('node:fs'),readline=require('node:readline');
readline.createInterface({input:process.stdin}).on('line',line=>{
 const r=JSON.parse(line);let data={};
 if(r.method==='describe')data={protocol_version:'v1',capabilities:['probe','control']};
 else if(r.method==='probe')data={data:{paused:fs.existsSync('remote-paused')}};
 else if(r.method==='control'){
  const files=fs.readdirSync('history/executions').filter(n=>n.endsWith('.json'));
  if(!files.some(n=>{const r=JSON.parse(fs.readFileSync('history/executions/'+n));return r.data.phase==='write_started'&&r.data.result.status==='uncertain'}))process.exit(8);
  if(r.params.action==='cdn-pause')fs.writeFileSync('remote-paused','yes');else fs.unlinkSync('remote-paused');
  fs.appendFileSync('control.log',r.params.action+'\\n');
  if(process.env.INFRAFILE_BROWSER_CONTROL_CASE==='lost_reply')process.exit(7);
  data={data:{}};
 }
 process.stdout.write(JSON.stringify({jsonrpc:'2.0',id:r.id,result:data})+'\\n');
});
`;
    fs.writeFileSync(path.join(pluginDir, 'infrafile-plugin-browser-control'), fake, { mode: 0o700 });
    const env = { ...process.env, PATH: pluginDir + path.delimiter + process.env.PATH };
    const cli = (args, extra = {}) => execFileSync(binary, args, { cwd: executionRepo, env: { ...env, ...extra }, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] });
    const actor = 'reviewer <img src=x onerror=alert(1)>';
    const first = JSON.parse(cli(['plan', 'cdn-pause', 'Resource/edge', '-o', 'pause.json', '--json']));
    const flags = p => ['--reviewed-digest', p.digest, '--authorization-ref', 'chat:browser-reviewed', '--actor', actor, '--json'];
    const applied = JSON.parse(cli(['apply', 'pause.json', ...flags(first)]));
    assert(applied.status === 'applied', 'native control did not produce an applied receipt');
    const second = JSON.parse(cli(['plan', 'cdn-resume', 'Resource/edge', '-o', 'resume.json', '--json']));
    const failed = spawnSync(binary, ['apply', 'resume.json', ...flags(second)], { cwd: executionRepo, env: { ...env, INFRAFILE_BROWSER_CONTROL_CASE: 'lost_reply' }, encoding: 'utf8' });
    assert(failed.status === 1 && JSON.parse(failed.stdout).status === 'uncertain', 'lost native reply did not remain uncertain');
    const replay = spawnSync(binary, ['apply', 'resume.json', ...flags(second)], { cwd: executionRepo, env, encoding: 'utf8' });
    assert(replay.status === 1 && JSON.parse(replay.stdout).verification === 'matches_expected', 'uncertain retry was not a readonly verification');
    assert(fs.readFileSync(path.join(executionRepo, 'control.log'), 'utf8').trim().split('\n').length === 2, 'retry sent another control request');
    const before = files(executionRepo), app = await start('executions', ['--dir', executionRepo]);
    const context = await browser.newContext({ viewport: { width: 390, height: 844 } }), page = await context.newPage();
    await pair(page, app); await page.goto(app.origin + '/changes#key=Resource%2Fedge');
    await page.getByText('已回读匹配', { exact: true }).waitFor();
    await page.getByText('结果不确定', { exact: true }).waitFor();
    assert(await page.locator('#content img').count() === 0, 'receipt metadata became HTML');
    const uncertain = page.locator('article').filter({ hasText: second.digest });
    await uncertain.getByRole('button', { name: '读取完整计划与回执', exact: true }).press('Enter');
    await uncertain.getByText('审阅计划', { exact: true }).waitFor();
    const plan = JSON.parse(await uncertain.locator('pre').first().textContent());
    assert(isDeepStrictEqual(plan, second), 'browser did not display the reviewed native plan');
    assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'execution cards overflow narrow viewport');
    await page.screenshot({ path: path.join(base, 'execution-receipts-narrow.png'), fullPage: true });
    await page.goto(app.origin + '/changes#key=Resource%2Fabsent');
    await page.getByText('尚无本范围的执行回执。请通过本地 CLI 审阅和执行；保存台账不会执行云操作。', { exact: true }).waitFor();
    assert(isDeepStrictEqual(before, files(executionRepo)), 'browser changed native receipts or fixture data');
    await context.close(); await stop(app);
    return { native_plan_and_reviewed_control: true, write_ahead_receipt: true, lost_reply_uncertain: true, retry_probe_only: true, plan_detail_matches_cli: true, attribution_escaped: true, narrow_keyboard_flow: true, filtered_empty_visible: true, files_unchanged_on_read: true };
}
async function main() {
    for (const args of [[], ['--listen', '0.0.0.0:0'], ['--pair-file', path.join(repo, 'code')]]) {
        const r = spawnSync(binary, ['serve', '--dir', repo, ...args], { encoding: 'utf8', timeout: 3000 });
        assert(r.status === 2, 'unsafe serve configuration accepted');
    }
    assert(!fs.existsSync(path.join(repo, 'code')), 'pair material written into ledger');
    report.checks.unsafe_startup_refused = true;
    assert(['chromium', 'webkit'].includes(engine), 'unsupported browser engine');
    assert(['csp', 'views', 'all', 'package'].includes(suite), 'unsupported browser suite');
    const browser = await playwright[engine].launch({ headless: true, executablePath: process.env.INFRAFILE_BROWSER_EXECUTABLE || undefined });
    try {
        const local = await start('local');
        const first = await browser.newContext({ viewport: { width: 1080, height: 800 } }), p1 = await first.newPage();
        const externalRequests = [];
        if (suite === 'package') await first.route('**/*', route => {
            if (new URL(route.request().url()).origin !== local.origin) { externalRequests.push(route.request().url()); return route.abort(); }
            return route.continue();
        });
        await p1.goto(local.origin + '/');
        await p1.screenshot({ path: path.join(base, 'login.png') });
        const unauth = await session(p1);
        assert(unauth.status === 401, 'unauthenticated API accepted');
        await pair(p1, local);
        if (suite === 'package') {
            for (const [route, title] of [['assets', '登记期望'], ['inspections', '已保存的问题历史'], ['changes', '问题变化时间线']]) {
                await p1.goto(local.origin + '/' + route);
                await p1.getByRole('heading', { name: title, exact: true }).waitFor();
            }
            assert(externalRequests.length === 0, 'package requested external resources');
            assert(isDeepStrictEqual(before, files(repo)), 'package query modified ledger');
            report.checks.package = { three_pages: true, external_network_blocked: true, runtime_path_has_no_node: true };
            fs.writeFileSync(path.join(base, 'evidence.json'), JSON.stringify(report, null, 2));
            console.log(JSON.stringify({ evidence: path.join(base, 'evidence.json'), checks: report.checks }));
            return;
        }
        if (suite === 'csp') {
            report.checks.csp = await cspViews(p1, local);
            console.log(JSON.stringify(report.checks));
            return;
        }
        if (suite === 'views') {
            report.checks.assets = await assetViews(p1, local);
            assert(isDeepStrictEqual(before, files(repo)), 'asset view modified ledger');
            fs.writeFileSync(path.join(base, 'evidence.json'), JSON.stringify(report, null, 2));
            console.log(JSON.stringify({ evidence: path.join(base, 'evidence.json'), checks: report.checks }));
            return;
        }
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
        report.checks.concurrent_auth = await concurrentAuthViews(p1, local);
        report.checks.views = await views(p1, local);
        report.checks.assets = await assetViews(p1, local);
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
        report.checks.inventory_edit = await editViews(browser);
        report.checks.execution_receipts = await executionViews(browser);
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
