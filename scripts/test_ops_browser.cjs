// Local read-only acceptance. An optional private M1 package is copied before fault injection.
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const crypto = require('node:crypto');
const { spawn } = require('node:child_process');
const { once } = require('node:events');
const playwright = require('../web/node_modules/playwright');
const assert = (ok, text) => { if (!ok) throw Error(text); };
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
const binary = path.resolve(process.env.INFRAFILE_BINARY || 'bin/infrafile');
const input = process.env.INFRAFILE_OPS_PACKAGE;
assert(input, 'set INFRAFILE_OPS_PACKAGE to a generated fixture package');
const base = fs.mkdtempSync(path.join(os.tmpdir(), 'infrafile-ops-browser-'));fs.chmodSync(base, 0o700);
const repo = path.join(base, 'candidate');fs.cpSync(path.join(input, 'bundle/candidate'), repo, { recursive: true });
const modelPath = path.join(base, 'ops.json');fs.copyFileSync(path.join(input, 'ops.json'), modelPath);
const original = fs.readFileSync(modelPath);const model = JSON.parse(original);
const authority = path.join(base, 'authority');fs.mkdirSync(authority);
for (const name of Object.keys(model.authority_files)) {
  const frozen = path.join(input, 'bundle/source', name);
  const source = fs.existsSync(frozen) ? frozen : path.join(process.env.INFRAFILE_OPS_AUTHORITY, name);
  const target = path.join(authority, name);fs.mkdirSync(path.dirname(target), { recursive: true });fs.copyFileSync(source, target);
}
const pair = path.join(base, 'pair');
const child = spawn(binary, ['serve', '--dir', repo, '--ops-overview', modelPath, '--ops-authority', authority, '--listen', '127.0.0.1:0', '--pair-file', pair], { stdio: ['ignore', 'pipe', 'pipe'] });
let stderr = '';child.stderr.on('data', data => stderr += data);
let browser;
const report = { binary_sha256: crypto.createHash('sha256').update(fs.readFileSync(binary)).digest('hex'), checks: {}, expected: model.summary };
(async () => {
  try {
    let origin;
    for (let i = 0; i < 150; i++) { origin = stderr.match(/工作台：(http:\/\/[^\s]+)/)?.[1];if (origin) break;assert(child.exitCode === null, 'candidate process failed');await sleep(40); }
    assert(origin, 'candidate readiness timeout');
    browser = await playwright[process.env.INFRAFILE_BROWSER_ENGINE || 'chromium'].launch({ headless: true });
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
    const page = await context.newPage();await page.clock.install();const errors = [];page.on('pageerror', error => errors.push(error.message));
    await page.goto(origin);await page.getByLabel('配对码', { exact: true }).fill(fs.readFileSync(pair, 'utf8').trim());
    await page.getByRole('button', { name: '连接工作台', exact: true }).click();
    await page.getByRole('heading', { name: '所有机器与应用', exact: true }).waitFor();
    await page.getByText('台账与证据包来源一致', { exact: true }).waitFor();
    assert(await page.locator('.ops-directory tbody tr').count() === model.summary.servers, 'server denominator lost');
    assert(await page.getByRole('button', { name: /重启|启停|部署应用|立即备份/ }).count() === 0, 'write operation exposed');
    await page.screenshot({ path: path.join(base, 'overview.png'), fullPage: true });
    const guarded = model.servers.find(row => row.notice) || model.servers[0];
    await page.getByLabel('搜索', { exact: true }).fill(guarded.name);
    assert(await page.locator('.ops-directory tbody tr').count() === 1, 'server filter failed');
    await page.getByRole('button', { name: `查看 ${guarded.name}`, exact: true }).click();
    if (guarded.notice) await page.getByText(guarded.notice, { exact: true }).first().waitFor();
    await page.getByRole('button', { name: '关闭详情', exact: true }).click();
    await page.getByLabel('搜索', { exact: true }).fill('');
    await page.getByRole('button', { name: '应用', exact: true }).click();
    assert(await page.locator('.ops-directory tbody tr').count() === model.summary.applications, 'application denominator lost');
    assert(await page.getByText('完整健康未验', { exact: true }).count() === model.summary.applications, 'runtime green became app health');
    await page.getByLabel('状态', { exact: true }).selectOption('UNKNOWN');
    assert(await page.locator('.ops-directory tbody tr').count() > 0, 'unknown filter disappeared');
    await page.getByRole('button', { name: '监控', exact: true }).click();
    assert(await page.locator('.ops-directory tbody tr').count() === model.monitors.length, 'monitor denominator lost');
    await page.getByLabel('状态', { exact: true }).selectOption('PAUSED');
    assert(await page.locator('.ops-directory tbody tr').count() === model.monitors.filter(m => m.state.status === 'PAUSED').length, 'paused monitor reported as outage');
    report.checks.counts_filters_pending_acceptance_and_pause = true;
    await page.getByLabel('状态', { exact: true }).selectOption('all');
    const selectedMonitor = model.monitors.find(row => row.targets.length) || model.monitors[0];
    await page.getByRole('button', { name: `查看 ${selectedMonitor.name}`, exact: true }).click();
    await page.getByRole('heading', { name: '配置观测时效', exact: true }).waitFor();
    await page.screenshot({ path: path.join(base, 'monitors.png'), fullPage: true });
    // A failed auto-read must not keep passing badges in the visible result.
    await page.route('**/api/ops-overview', route => route.fulfill({ status: 422, contentType: 'application/json', body: '{}' }));
    await page.clock.fastForward(61000);
    await page.getByText('本次重新读取失败，以下为上次结果；当前时效未核实。', { exact: true }).waitFor();
    assert(await page.locator('.ops-status.ops-pass').count() === 0, 'failed reread retained green');
    assert(await page.getByRole('heading', { name: selectedMonitor.name, exact: true }).count() === 1, 'automatic read discarded selection');
    await page.unroute('**/api/ops-overview');await page.clock.resume();
    await page.getByRole('button', { name: '重新读取保存结果', exact: true }).click();
    await page.getByText('台账与证据包来源一致', { exact: true }).waitFor();
    const binding = Object.keys(model.authority_files)[0];fs.appendFileSync(path.join(authority, binding), '\n');
    await page.getByRole('button', { name: '重新读取保存结果', exact: true }).click();
    await page.getByText('权威来源已有变化 · 以下是冻结观测', { exact: true }).waitFor();
    report.checks.auto_failure_masks_green_selection_retained_source_change_visible = true;
    await page.getByRole('button', { name: '机器', exact: true }).click();
    await page.screenshot({ path: path.join(base, 'desktop.png'), fullPage: true });
    await page.setViewportSize({ width: 390, height: 844 });
    await page.screenshot({ path: path.join(base, 'mobile.png'), fullPage: true });
    assert(await page.locator('.ops-directory tbody tr').count() === model.summary.servers, 'mobile list lost items');
    const expired = await context.newPage();await expired.goto(origin + '/ops');
    await expired.route('**/api/ops-overview', route => route.fulfill({ status: 401, contentType: 'application/json', body: '{}' }));
    await expired.getByRole('button', { name: '重新读取保存结果', exact: true }).click();
    await expired.getByRole('heading', { name: '会话已失效', exact: true }).waitFor();
    assert(await expired.locator('.ops-directory').count() === 0, 'session loss retained data');
    assert(errors.length === 0, 'browser script error');
    report.checks.responsive_session_expiry_no_browser_errors = true;
    assert(crypto.createHash('sha256').update(fs.readFileSync(path.join(input, 'ops.json'))).digest('hex') === crypto.createHash('sha256').update(original).digest('hex'), 'original package mutated');
    fs.writeFileSync(path.join(base, 'evidence.json'), JSON.stringify(report, null, 2) + '\n', { mode: 0o600 });
    console.log(JSON.stringify({ evidence: path.join(base, 'evidence.json'), screenshots: ['overview.png', 'desktop.png', 'mobile.png', 'monitors.png'], checks: report.checks }));
  } finally {
    if (browser) await browser.close();
    if (child.exitCode === null) { const done = once(child, 'exit');child.kill('SIGTERM');const timer = setTimeout(() => child.kill('SIGKILL'), 6000);await done;clearTimeout(timer); }
    if (fs.existsSync(pair)) fs.unlinkSync(pair);
  }
})().catch(error => { console.error('Ops browser acceptance failed:', error.message, 'private output:', base);process.exitCode = 1; });
