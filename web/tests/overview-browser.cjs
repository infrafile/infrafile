const fs = require('node:fs');
const path = require('node:path');

module.exports = async function overviewViews(browser, entry, { pair, base, assert }) {
  const context = await browser.newContext({ viewport: { width: 1200, height: 850 } });
  const page = await context.newPage(); page.setDefaultTimeout(5000);
  try {
    await page.clock.install();
    let calls = [], many = false, changed = false;
    page.on('request', request => { const name = request.url().split('/api/')[1]; if (name) calls.push(name); });
    await page.route('**/api/asset-summaries', async route => {
      const response = await route.fetch(), body = await response.json();
      if (many) {
        const server = body.rows.find(row => row.kind === 'Server' && !row.archived);
        for (let n = 0; n < 20; n++) body.rows.push({ ...server, key: 'Server/generated-' + n, name: 'generated-' + n, hardware: { cpu: changed ? '8 cores saved update' : '4 cores saved', memory: '16 GiB', disk: '100 GiB', os: 'Linux' } });
      }
      await route.fulfill({ response, json: body });
    });
    await pair(page, entry);
    await page.getByRole('heading', { name: '总览', exact: true }).waitFor();
    await page.getByRole('heading', { name: '服务器配置', exact: true }).waitFor();
    await page.getByText('台账登记值 · 非实时', { exact: true }).waitFor();
    assert(JSON.stringify(calls) === JSON.stringify(['asset-summaries', 'expiry', 'certificates']), 'overview must use three fixed serial queries: ' + JSON.stringify(calls));
    await page.getByRole('link', { name: /^暂无检查 \d/ }).click();
    await page.getByRole('heading', { name: '资产目录', exact: true }).waitFor();
    assert(await page.getByRole('radio', { name: '暂无检查', exact: true }).isChecked(), 'overview category link lost the filter');
    await page.goto(entry.origin + '/assets#key=Server%2Fweb1&days=7');
    await page.getByRole('heading', { name: 'Server/web1', exact: true }).waitFor();
    await page.getByRole('heading', { name: '登记期望', exact: true }).waitFor();
    many = true; calls = [];
    await page.goto(entry.origin + '/overview');
    await page.getByText('generated-19', { exact: true }).waitFor();
    assert(calls.length === 3 && !calls.includes('show') && !calls.includes('context'), 'server count increased overview query count: ' + JSON.stringify(calls));
    for (const [name, theme] of [['浅色', 'light'], ['深色', 'dark']]) {
      await page.getByRole('radio', { name, exact: true }).click();
      await page.locator('.radix-themes.' + theme).waitFor();
      await page.screenshot({ path: path.join(base, 'overview-' + theme + '.png') });
    }
    await page.setViewportSize({ width: 390, height: 844 });
    assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'overview overflows narrow viewport');
    await page.screenshot({ path: path.join(base, 'overview-narrow.png') });
    await page.setViewportSize({ width: 1200, height: 850 });
    await page.clock.pauseAt(new Date());
    await page.route('**/api/expiry', route => route.fulfill({ status: 503, contentType: 'application/json', body: '{}' }));
    changed = true;
    for (let n = 1; n <= 3; n++) {
      await page.clock.runFor(60000);
      await page.locator(`[data-auto-read][data-failures="${n}"]`).waitFor();
    }
    const expiry = page.getByRole('region', { name: '登记到期', exact: true });
    await expiry.getByRole('alert').waitFor();
    await expiry.getByText('上次读取时间：', { exact: false }).waitFor();
    await page.locator('[data-auto-read]').getByText('下列状态可能已过期', { exact: false }).waitFor();
    assert(await page.getByText('8 cores saved update', { exact: true }).count() === 0, 'automatic response replaced overview contents');
    await page.getByRole('button', { name: /有 \d 项更新/ }).click();
    await page.getByText('8 cores saved update', { exact: true }).first().waitFor();
    await page.getByText('没有 tls 探测快照 · 不代表证书正常', { exact: true }).waitFor();
    await page.unroute('**/api/expiry');
    await page.clock.runFor(60000);
    await page.locator('[data-auto-read][data-failures="0"][data-reading="false"]').waitFor();
    await page.getByRole('button', { name: '重新读取', exact: true }).click();
    await page.getByText('generated-19', { exact: true }).waitFor();
    await page.route('**/api/expiry', route => route.fulfill({ status: 503, contentType: 'application/json', body: '{}' }));
    await page.getByRole('button', { name: '重新读取', exact: true }).click();
    await expiry.getByRole('alert').waitFor();
    assert(await expiry.getByText('上次读取时间：', { exact: false }).count() === 0, 'manual failed block retained old content');
    await page.getByRole('heading', { name: '服务器配置', exact: true }).waitFor();
    await page.route('**/api/certificates', route => route.fulfill({ status: 401, contentType: 'application/json', body: '{}' }));
    await page.getByRole('button', { name: '重新读取', exact: true }).click();
    await page.getByRole('heading', { name: '会话已失效', exact: true }).waitFor();
    assert(await page.getByRole('navigation', { name: '工作台', exact: true }).count() === 0, 'late block 401 retained business interface');
    return { default_route: true, four_categories: true, legacy_deep_link: true, fixed_three_queries_with_many_servers: true, themes_narrow: true, independent_failure_retention_recovery: true, deferred_updates: true, manual_failure_clears: true, late_block_401_clears: true };
  } finally { await context.close(); }
};
