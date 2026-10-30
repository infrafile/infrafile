// Run after make build: node web/tests/expiry-browser.cjs. Only uses a copied fictional ledger.
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const assert = require('node:assert/strict');
const { spawn, spawnSync } = require('node:child_process');
const { once } = require('node:events');
const { chromium } = require('playwright');

async function main() {
  const root = path.resolve(__dirname, '../..');
  const output = fs.mkdtempSync(path.join(process.env.INFRAFILE_BROWSER_OUTPUT_PARENT || os.tmpdir(), 'infrafile-expiry-'));
  fs.chmodSync(output, 0o700);
  const ledger = path.join(output, 'ledger'), pairFile = path.join(output, 'pair');
  fs.cpSync(path.join(root, 'examples/homelab'), ledger, { recursive: true });
  const binary = path.join(root, 'bin/infrafile');
  const child = spawn(binary, ['serve', '--dir', ledger, '--listen', '127.0.0.1:0', '--pair-file', pairFile], { stdio: ['ignore', 'pipe', 'pipe'] });
  let stderr = '', browser;
  child.stderr.on('data', b => stderr += b);
  const ready = new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(Error('fixture readiness timeout')), 10000);
    child.stderr.on('data', () => { const match = stderr.match(/工作台：(http:\/\/[^\s]+)/); if (match) { clearTimeout(timer); resolve(match[1]); } });
    child.once('exit', () => { clearTimeout(timer); reject(Error('fixture exited before ready')); });
  });
  try {
    const origin = await ready;
    browser = await chromium.launch({ headless: true });
    const page = await browser.newPage({ viewport: { width: 1200, height: 800 } });
    const expiryRequests = [];
    page.on('request', r => { if (r.url() === origin + '/api/expiry') expiryRequests.push(r); });
    await page.goto(origin);
    await page.getByLabel('配对码', { exact: true }).fill(fs.readFileSync(pairFile, 'utf8').trim());
    await page.getByRole('button', { name: '连接工作台', exact: true }).click();
    await page.getByRole('heading', { name: '资产目录', exact: true }).waitFor();
    const cli = spawnSync(binary, ['expiry', ledger, '--within', '3652500d', '--json'], { encoding: 'utf8' });
    assert.ok(cli.status === 0 || cli.status === 1);
    const report = JSON.parse(cli.stdout);
    for (const name of ['example.net', 'example.com', 'assets-cdn', 'web1']) assert.ok(report.entries.some(entry => entry.name === name), 'missing fixture expiry ' + name);
    const panel = page.getByRole('region', { name: '登记到期', exact: true });
    for (const entry of report.entries) {
      const key = entry.kind + '/' + entry.name;
      await page.evaluate(key => { location.hash = new URLSearchParams({ key, days: '7' }).toString(); }, key);
      await page.getByRole('heading', { name: key, exact: true }).waitFor();
      await panel.getByText(entry.expires_at, { exact: true }).waitFor();
      const expected = entry.days_left < 0 ? `已过期 ${-entry.days_left} 天` : entry.days_left === 0 ? '今天到期' : `剩余 ${entry.days_left} 天`;
      await panel.getByText(expected, { exact: true }).waitFor();
      assert.ok((await panel.innerText()).includes(report.as_of));
    }
    assert.equal(expiryRequests.length, 1, 'changing selection must reuse the single expiry report');
    assert.equal((await expiryRequests[0].response()).status(), 200);
    const headers = await expiryRequests[0].allHeaders();
    assert.equal(expiryRequests[0].method(), 'POST');
    assert.ok(headers.cookie?.includes('infrafile_session=') && headers['x-csrf-token']);
    assert.deepEqual(expiryRequests[0].postDataJSON(), { within_days: 3652500 });
    await panel.scrollIntoViewIfNeeded();
    await page.screenshot({ path: path.join(output, 'expiry-detail.png') });
    await page.route('**/api/expiry', route => route.fulfill({ status: 503, body: '{}' }));
    await page.getByRole('button', { name: '重新读取', exact: true }).click();
    await panel.getByRole('alert').filter({ hasText: '到期信息读取失败' }).waitFor();
    assert.equal(await panel.locator('time').count(), 0, 'failed refresh retained the old expiry result');
    assert.ok(!(await panel.innerText()).includes('没有到期项'));
    await panel.scrollIntoViewIfNeeded();
    await page.screenshot({ path: path.join(output, 'expiry-failed.png') });
    await page.unroute('**/api/expiry');
    await page.route('**/api/expiry', route => route.fulfill({ status: 401, body: '{}' }));
    await page.getByRole('button', { name: '重新读取', exact: true }).click();
    await page.getByRole('heading', { name: '会话已失效', exact: true }).waitFor();
    assert.equal(await page.getByRole('heading', { name: '资产目录', exact: true }).count(), 0);
    fs.writeFileSync(path.join(output, 'report.json'), JSON.stringify({ entries_equal_cli: report.entries.length, single_expiry_request: true, csrf_cookie: true, failure_clears_result: true, unauthorized_clears_workspace: true }, null, 2));
    console.log('Expiry browser checks passed; evidence: ' + output);
  } finally {
    if (browser) await browser.close();
    if (child.exitCode === null) {
      const stopped = once(child, 'exit');
      child.kill('SIGTERM');
      const timer = setTimeout(() => child.kill('SIGKILL'), 6500);
      const [code, signal] = await stopped;
      clearTimeout(timer);
      assert.equal(code, 0); assert.equal(signal, null);
    }
    assert.ok(!fs.existsSync(pairFile), 'fixture pairing material retained');
  }
}
main().catch(error => { console.error(error); process.exitCode = 1; });
