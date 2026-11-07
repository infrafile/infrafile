// Real browser + two real local server processes. All adapters are local fakes.
const fs = require('node:fs'), path = require('node:path'), os = require('node:os'), crypto = require('node:crypto');
const { spawn } = require('node:child_process'), { once } = require('node:events');
const pw = require('../web/node_modules/playwright');
const assert = (ok, message) => { if (!ok) throw Error(message); };
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
const hash = file => crypto.createHash('sha256').update(fs.readFileSync(file)).digest('hex');
const write = (file, data) => { fs.mkdirSync(path.dirname(file), { recursive: true }); fs.writeFileSync(file, JSON.stringify(data), { mode: 0o600 }); };
const input = process.env.INFRAFILE_OPS_PACKAGE, binary = path.resolve(process.env.INFRAFILE_BINARY || 'bin/infrafile');
assert(input, 'set generated fixture package');
const base = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), 'infrafile-ops-recovery-browser-'))); fs.chmodSync(base, 0o700);
const old = path.join(base, 'old'), current = path.join(base, 'current'), authority = path.join(base, 'authority'), state = path.join(base, 'receipts'), remote = path.join(base, 'remote');
for (const dir of [old, current, authority, state, remote]) fs.mkdirSync(dir, { mode: 0o700 });
for (const pkg of [old, current]) fs.cpSync(path.join(input, 'bundle/candidate'), path.join(pkg, 'bundle/candidate'), { recursive: true });
const model = JSON.parse(fs.readFileSync(path.join(input, 'ops.json'))), reader = JSON.parse(fs.readFileSync(path.join(input, 'reader.json')));
for (const name of Object.keys(model.authority_files)) {
  for (const dir of [authority, path.join(old, 'bundle/source')]) {
    const file = path.join(dir, name); fs.mkdirSync(path.dirname(file), { recursive: true }); fs.copyFileSync(path.join(input, 'bundle/source', name), file);
  }
}
const cap = { ...reader.capabilities[0], id: 'selected-once', label: '模拟固定观察任务', description: 'Local fixture only', timeout_seconds: 2 };
const target = model.servers.find(s => s.key === cap.target);
const oldModel = path.join(old, 'ops.json'), oldActor = path.join(old, 'actor.json'), oldCode = path.join(base, 'old.py'), statusCode = path.join(base, 'status.py');
write(oldModel, model);
const common = `import datetime,json,pathlib,sys
r=json.load(sys.stdin);root=pathlib.Path(sys.argv[1]);op=r['operation'];entry=root/(r['id']+'.json')
counter=root/('count-'+op);counter.write_text((counter.read_text() if counter.exists() else '')+'x')
`;
fs.writeFileSync(oldCode, common + `
if op=='plan':
 out={**r,'state':'prepared','error_code':'ok','digest':'a'*64,'expires_at':(datetime.datetime.now(datetime.timezone.utc)+datetime.timedelta(seconds=30)).isoformat(),'invocation_id':'','disposition':'submit_once','fields':[]};entry.write_text(json.dumps(out))
elif op=='apply':
 out=json.loads(entry.read_text());out.update(operation=op,state='completed',invocation_id='b'*32,disposition='submitted');entry.write_text(json.dumps(out));out.update(state='uncertain',error_code='transport_failed',invocation_id='')
else:raise Exception('old code must not reconcile')
print(json.dumps(out))
`);
fs.writeFileSync(statusCode, `import json,pathlib,sys
r=json.load(sys.stdin)
assert r['operation']=='status'
root=pathlib.Path(sys.argv[1]);counter=root/'count-status';counter.write_text((counter.read_text() if counter.exists() else '')+'x')
out=json.loads((root/(r['id']+'.json')).read_text());out['operation']='status';print(json.dumps(out))
`);
const config = (file, overview, version = 'infrafile-ops-actor/v1') => ({ version, executable: reader.executable, overview_sha256: hash(overview), command: [reader.command[0], '-I', file, remote], directory: base, files: { [reader.executable]: hash(reader.executable), [file]: hash(file) }, capabilities: [cap] });
write(oldActor, config(oldCode, oldModel));
let child, browser;
const report = { binary_sha256: hash(binary), checks: {} };
const count = op => fs.existsSync(path.join(remote, 'count-' + op)) ? fs.readFileSync(path.join(remote, 'count-' + op)).length : 0;
async function start(pkg, listen, recovery) {
  const pair = path.join(pkg, 'pair'); let log = '';
  child = spawn(binary, ['serve', '--dir', path.join(pkg, 'bundle/candidate'), '--ops-overview', path.join(pkg, 'ops.json'), '--ops-authority', authority, '--ops-actions', path.join(pkg, 'actor.json'), '--ops-action-state', state, '--listen', listen, '--pair-file', pair, ...(recovery ? ['--ops-recoveries', recovery] : [])], { stdio: ['ignore', 'pipe', 'pipe'] });
  child.stderr.on('data', data => { log += data; });
  for (let i = 0; i < 200; i++) { const url = log.match(/工作台：(http:\/\/[^\s]+)/)?.[1]; if (url) return { url, pair }; assert(child.exitCode === null, 'local candidate exited'); await sleep(30); }
  throw Error('local candidate readiness timeout');
}
async function stop() { if (child && child.exitCode === null) { const done = once(child, 'close'); child.kill('SIGTERM'); await done; } }
async function login(page, entry) {
  await page.goto(entry.url); await page.getByLabel('配对码', { exact: true }).fill(fs.readFileSync(entry.pair, 'utf8').trim()); await page.getByRole('button', { name: '连接工作台', exact: true }).click(); await page.getByRole('heading', { name: '所有机器与应用', exact: true }).waitFor();
}
(async () => { try {
  const first = await start(old, '127.0.0.1:0');
  browser = await pw[process.env.INFRAFILE_BROWSER_ENGINE || 'chromium'].launch({ headless: true });
  const page = await browser.newPage({ viewport: { width: 1440, height: 1050 } }); const errors = []; page.on('pageerror', e => errors.push(e.message));
  await login(page, first);
  await page.getByLabel('搜索', { exact: true }).fill(target.name); await page.getByRole('button', { name: '查看 ' + target.name, exact: true }).click();
  let response = page.waitForResponse(r => r.url().endsWith('/api/ops-action/plan')); await page.getByRole('button', { name: '预检单次执行', exact: true }).click(); const planned = await (await response).json();
  await page.getByLabel('已核对目标、固定任务、影响和排程变化', { exact: true }).check(); await page.getByRole('button', { name: '提交单次执行', exact: true }).click(); await page.getByText('执行结果待核实 · 不要重复提交', { exact: true }).waitFor();
  const record = JSON.parse(fs.readFileSync(path.join(state, planned.id + '.json'))), claim = hash(path.join(state, planned.id + '.dispatch'));
  assert(record.binding === hash(oldActor) && count('apply') === 1, 'initial submission not durable');
  await stop();
  fs.appendFileSync(oldCode, '\nraise Exception("historical program disabled")\n');
  const changed = Object.keys(model.authority_files)[0]; fs.appendFileSync(path.join(authority, changed), '\n');
  const next = structuredClone(model); next.captured_at = new Date().toISOString(); next.authority_files[changed] = hash(path.join(authority, changed)); write(path.join(current, 'ops.json'), next);
  write(path.join(current, 'actor.json'), config(statusCode, path.join(current, 'ops.json')));
  const recovery = config(statusCode, oldModel, 'infrafile-ops-reconciler/v1'); recovery.files[oldActor] = record.binding; recovery.files[oldModel] = hash(oldModel);
  for (const [name, digest] of Object.entries(model.authority_files)) recovery.files[path.join(old, 'bundle/source', name)] = digest;
  const statusBinding = path.join(current, 'status.json'); write(statusBinding, recovery);
  const registry = path.join(current, 'recoveries.json'); write(registry, { version: 'infrafile-ops-recoveries/v1', bindings: [{ binding: record.binding, overview: oldModel, actor: oldActor, provider: statusBinding }] });
  const second = await start(current, new URL(first.url).host, registry); assert(second.url === first.url, 'fixed address changed');
  await login(page, second);
  const center = page.getByRole('region', { name: '待核实执行回执', exact: true }); await center.waitFor();
  assert(count('status') === 0 && count('apply') === 1 && count('plan') === 1, 'restart implicitly invoked adapter'); report.checks.restart_after_code_and_source_change_preserves_pending_without_execution = true;
  await center.getByRole('button', { name: '查看原执行记录', exact: true }).click(); await center.getByText('执行结果待核实 · 不要重复提交', { exact: true }).waitFor(); assert(count('status') === 0, 'local record read contacted remote');
  await center.getByRole('button', { name: '核实原执行回执', exact: true }).click(); await center.getByText('本次执行已完成', { exact: true }).waitFor();
  assert(count('status') === 1 && count('apply') === 1 && count('plan') === 1, 'recovery repeated submission');
  const done = JSON.parse(fs.readFileSync(path.join(state, planned.id + '.json'))); assert(done.binding === record.binding && done.plan.digest === record.plan.digest && done.result.invocation_id === 'b'.repeat(32) && hash(path.join(state, planned.id + '.dispatch')) === claim, 'original receipt attribution changed');
  report.checks.explicit_status_only_uses_original_binding_and_keeps_dispatch = true;
  assert(await center.getByRole('button', { name: '核实原执行回执', exact: true }).isDisabled(), 'terminal receipt still offered reconciliation'); report.checks.terminal_recovery_is_not_repeated = true;
  await page.screenshot({ path: path.join(base, 'recovered-desktop.png'), fullPage: true }); await page.setViewportSize({ width: 390, height: 844 }); await page.screenshot({ path: path.join(base, 'recovered-mobile.png'), fullPage: true });
  await page.reload(); await page.getByRole('heading', { name: '所有机器与应用', exact: true }).waitFor(); assert(count('apply') === 1 && count('status') === 1, 'reload replayed operation'); report.checks.reload_does_not_replay = true;
  assert(errors.length === 0, 'browser error'); write(path.join(base, 'evidence.json'), report); console.log(JSON.stringify({ evidence: path.join(base, 'evidence.json'), checks: report.checks }));
} finally { await browser?.close(); await stop(); } })().catch(error => { console.error(error.message); process.exitCode = 1; });
