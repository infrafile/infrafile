const fs = require('node:fs');
const path = require('node:path');

module.exports = async function autoReadViews(browser, entry, { pair, base, assert }) {
  const context = await browser.newContext({ viewport: { width: 1200, height: 800 } });
  const page = await context.newPage();
  page.setDefaultTimeout(5000);
  try {
    await page.clock.install();
    await pair(page, entry);
    await page.getByRole('heading', { name: '登记期望', exact: true }).waitFor();
    const toggle = page.getByRole('checkbox', { name: '自动重读（60 秒）', exact: true });
    const status = page.locator('[data-auto-read]:visible');
    assert(await toggle.isChecked(), 'automatic rereading is not enabled by default');
    await page.clock.pauseAt(new Date());
    let mode = 'normal', expiryFailure = false, summaries = 0, expiries = 0, contexts = 0, active = 0, maximum = 0;
    const timings = [];
    let release, lateSettled;
    page.on('request', request => { if (request.url().endsWith('/api/context')) contexts++; });
    await page.route('**/api/asset-summaries', async route => {
      summaries++; active++; maximum = Math.max(maximum, active);
      const current = mode;
      try {
        if (current === 'slow' || current === 'late') await new Promise(resolve => { release = resolve; });
        if (current === 'error' || current === 'unauthorized') return await route.fulfill({ status: current === 'error' ? 503 : 401, contentType: 'application/json', body: '{}' });
        const started = performance.now();
        const response = await route.fetch();
        const body = await response.json();
        if (current === 'normal') timings.push({ assets: body.rows.length, milliseconds: performance.now() - started });
        if (current === 'changed') { const row = body.rows.find(row => row.kind === 'Server' && !row.archived); row.name = 'changed-by-saved-observation'; row.risk.confirmed = 1; }
        if (current === 'late') body.rows[0].name = 'late-discarded-result';
        await route.fulfill({ response, json: body });
      } catch (error) { if (current !== 'late') throw error; }
      finally { active--; if (current === 'late') lateSettled?.(); }
    });
    await page.route('**/api/expiry', async route => {
      expiries++; active++; maximum = Math.max(maximum, active);
      try { if (expiryFailure) await route.fulfill({ status: 503, contentType: 'application/json', body: '{}' }); else await route.continue(); } finally { active--; }
    });
    const selected = await page.getByRole('option', { selected: true }).innerText();
    const detail = await page.locator('.asset-detail').innerText();
    await page.getByRole('searchbox', { name: '搜索资产', exact: true }).focus();
    await page.clock.runFor(59000);
    assert(summaries === 0, 'automatic reread ran before its interval');
    await page.clock.runFor(1000);
    await page.locator('[data-auto-read]:visible[data-reading="false"]').waitFor();
    await status.getByText('页面读取时间：', { exact: false }).waitFor();
    assert(summaries === 1 && expiries === 1 && maximum === 1, 'one automatic cycle must read summary and expiry serially: ' + JSON.stringify({ summaries, expiries, maximum, active }));
    assert(contexts === 0, 'automatic reread replaced asset details');
    assert(await page.getByRole('option', { selected: true }).innerText() === selected, 'automatic reread changed selection');
    assert(await page.getByRole('searchbox', { name: '搜索资产', exact: true }).evaluate(element => element === document.activeElement), 'automatic reread stole focus');
    assert(!(await page.locator('.asset-detail').innerText()).includes('正在读取资产详情'), 'automatic reread remounted details');
    await page.setViewportSize({ width: 390, height: 844 });
    assert(await toggle.count() === 1 && await toggle.isChecked(), 'narrow asset list hides automatic controls');
    await page.screenshot({ path: path.join(base, 'auto-read-narrow-list.png') });
    await page.getByRole('option', { selected: true }).click();
    assert(await toggle.count() === 1 && await toggle.isChecked(), 'narrow asset detail hides automatic controls');
    await page.screenshot({ path: path.join(base, 'auto-read-narrow-detail.png') });
    await page.setViewportSize({ width: 1200, height: 800 });
    await page.locator('.asset-rows').evaluate(element => { element.scrollTop = 80; });
    const scroll = await page.locator('.asset-rows').evaluate(element => element.scrollTop);

    mode = 'changed';
    await page.clock.runFor(60000);
    await page.getByRole('button', { name: /有 1 项更新/ }).waitFor();
    assert(await page.getByText('changed-by-saved-observation', { exact: true }).count() === 0, 'changed asset was applied without review');
    assert(await page.locator('.asset-rows').evaluate(element => element.scrollTop) === scroll, 'pending update moved list scroll');
    assert((await page.locator('.asset-detail').innerText()).includes(detail.split('\n')[0]), 'automatic update replaced detail contents');
    await page.getByRole('button', { name: /有 1 项更新/ }).click();
    await page.getByRole('option').filter({ hasText: 'changed-by-saved-observation' }).waitFor();

    mode = 'normal'; expiryFailure = true;
    await page.clock.runFor(60000);
    await status.getByText('重读失败', { exact: false }).waitFor();
    assert(await page.getByRole('option').count() > 0, 'failed expiry read cleared successful assets');
    expiryFailure = false;
    await page.clock.runFor(60000);
    await page.locator('[data-auto-read]:visible[data-failures="0"][data-reading="false"]').waitFor();
    mode = 'error';
    for (let attempt = 1; attempt <= 3; attempt++) {
      await page.clock.runFor(60000);
      await status.getByText('重读失败', { exact: false }).waitFor();
      await page.locator(`[data-auto-read]:visible[data-failures="${attempt}"]`).waitFor();
      assert(await page.getByRole('option').count() > 0, 'automatic failure cleared prior success');
    }
    await status.getByText('下列状态可能已过期', { exact: false }).waitFor();
    await page.screenshot({ path: path.join(base, 'auto-read-failed.png') });
    mode = 'normal';
    await page.clock.runFor(60000);
    await page.locator('[data-auto-read]:visible[data-failures="0"]').waitFor();
    assert(await status.getByText('重读失败', { exact: false }).count() === 0, 'recovery retained failure warning');

    await toggle.uncheck();
    assert(await page.evaluate(() => !Object.keys(localStorage).some(key => /auto|refresh/i.test(key))), 'automatic preference persisted to storage');
    const paused = summaries;
    await page.clock.runFor(120000);
    assert(summaries === paused, 'disabled automatic reread continued');
    await toggle.check();
    await page.clock.runFor(31000);
    await page.evaluate(() => window.dispatchEvent(new Event('focus')));
    await page.locator('[data-auto-read]:visible[data-reading="false"]').waitFor();
    assert(summaries === paused + 1, 'focus did not reread stale saved results');
    await page.evaluate(() => window.dispatchEvent(new Event('focus')));
    assert(summaries === paused + 1, 'focus reread ignored the 30-second threshold');

    await page.evaluate(() => { Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' }); document.dispatchEvent(new Event('visibilitychange')); });
    const hidden = summaries;
    await page.clock.runFor(120000);
    assert(summaries === hidden, 'hidden page kept polling');
    await page.evaluate(() => { Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' }); document.dispatchEvent(new Event('visibilitychange')); });
    await page.locator('[data-auto-read]:visible[data-reading="false"]').waitFor();
    assert(summaries === hidden + 1, 'visible page did not reread stale results');

    mode = 'slow';
    await page.clock.runFor(60000);
    await page.locator('[data-auto-read]:visible[data-reading="true"]').waitFor();
    const slow = summaries;
    await page.clock.runFor(120000);
    assert(summaries === slow, 'long automatic reread overlapped another cycle');
    release();
    await page.locator('[data-auto-read]:visible[data-reading="false"]').waitFor();
    mode = 'late';
    const settled = new Promise(resolve => { lateSettled = resolve; });
    await page.clock.runFor(60000);
    await page.locator('[data-auto-read]:visible[data-reading="true"]').waitFor();
    const releaseOld = release;
    mode = 'normal';
    const cancelled = page.waitForEvent('requestfailed', { predicate: request => request.url().endsWith('/api/asset-summaries') && !request.postDataJSON().kind }).catch(error => error);
    const newScope = page.waitForResponse(response => response.url().endsWith('/api/asset-summaries') && response.request().postDataJSON().kind === 'Server').catch(error => error);
    await page.getByRole('button', { name: /^服务器/ }).click();
    assert(!((await newScope) instanceof Error), 'server filter did not load its new scope');
    releaseOld(); await settled;
    assert(!((await cancelled) instanceof Error), 'scope switch did not cancel its automatic request');
    await page.locator('[data-auto-read]:visible[data-reading="false"]').waitFor();
    await page.getByRole('option').first().waitFor();
    assert(await page.getByText('late-discarded-result', { exact: true }).count() === 0, 'cancelled scope response replaced the new list');
    assert((await page.getByRole('option').allTextContents()).every(text => text.includes('Server/')), 'old scope rows leaked into the server filter');
    mode = 'error';
    await page.route('**/api/list', route => route.fulfill({ status: 503, contentType: 'application/json', body: '{}' }));
    await page.getByRole('button', { name: '重新读取', exact: true }).click();
    await page.locator('.asset-list > .error').waitFor();
    assert(await page.getByRole('option').count() === 0, 'manual reread failure retained successful rows');
    await page.unroute('**/api/list');
    for (const [route, heading, names] of [['inspections', '已保存的问题历史', ['inspect']], ['changes', '问题变化时间线', ['change/status', 'changes', 'executions']]]) {
      await page.goto(entry.origin + '/' + route);
      await page.getByRole('heading', { name: heading, exact: true }).waitFor();
      const calls = [];
      const record = request => { const name = request.url().split('/api/')[1]; if (name) calls.push(name); };
      page.on('request', record);
      await page.clock.runFor(60000);
      await page.locator('[data-auto-read]:visible[data-reading="false"]').waitFor();
      page.off('request', record);
      assert(JSON.stringify(calls) === JSON.stringify(names), route + ' automatic calls: ' + JSON.stringify(calls));
    }
    mode = 'normal';
    await page.goto(entry.origin + '/assets');
    await page.getByRole('heading', { name: '登记期望', exact: true }).waitFor();
    mode = 'unauthorized';
    await page.clock.runFor(60000);
    await page.getByRole('heading', { name: '会话已失效', exact: true }).waitFor();
    assert(await page.getByRole('heading', { name: '资产目录', exact: true }).count() === 0, '401 retained workspace');
    await page.screenshot({ path: path.join(base, 'auto-read-expired.png') });
    fs.writeFileSync(path.join(base, 'auto-read-timings.json'), JSON.stringify(timings, null, 2));
    return { interval: true, serial_reads: true, focus_visibility: true, disabled_pauses: true, deferred_updates: true, selection_detail_focus_scroll_preserved: true, failures_and_recovery: true, expiry_failure_preserves: true, scope_cancels_old_request: true, three_pages: true, manual_failure_clears: true, unauthorized_clears: true, summary_timings: timings };
  } finally {
    await context.close();
  }
};

module.exports.edit = async function autoReadEditor(browser, entry, { pair, assert }) {
  const context = await browser.newContext();
  const page = await context.newPage();
  try {
    await page.clock.install();
    await pair(page, entry);
    await page.goto(entry.origin + '/changes#key=Server%2Fweb');
    const editor = page.getByLabel('完整记录 JSON', { exact: true });
    await editor.waitFor();
    await page.clock.pauseAt(new Date());
    const record = JSON.parse(await editor.inputValue()); record.spec.address = '192.0.2.99';
    const draft = JSON.stringify(record, null, 2);
    await editor.fill(draft);
    await page.getByRole('button', { name: '校验并预览变更', exact: true }).click();
    const digest = page.getByText('预览摘要：', { exact: false }); await digest.waitFor();
    const reviewed = page.getByRole('checkbox', { name: '我已核对修改前后的完整内容', exact: true });
    await reviewed.check();
    const before = await digest.innerText();
    const response = page.waitForResponse(response => response.url().endsWith('/api/changes'));
    await page.clock.runFor(60000);
    await response;
    await page.locator('[data-auto-read][data-reading="false"]').waitFor();
    assert(await editor.inputValue() === draft, 'automatic reread overwrote the edit draft');
    assert(await digest.innerText() === before && await reviewed.isChecked(), 'automatic reread replaced reviewed preview');
    return true;
  } finally { await context.close(); }
};
