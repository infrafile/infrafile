// The fixture and every adapter are local; this runner never needs a private package.
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { execFileSync } = require('node:child_process');

const root = path.resolve(__dirname, '..');
const base = fs.mkdtempSync(path.join(os.tmpdir(), 'infrafile-ops-suites-'));
const fixture = path.join(base, 'package');
const env = { ...process.env, INFRAFILE_OPS_FIXTURE_DIR: fixture, INFRAFILE_OPS_PACKAGE: fixture,
  INFRAFILE_BINARY: path.resolve(process.env.INFRAFILE_BINARY || path.join(root, 'bin/infrafile')) };
execFileSync('go', ['test', './cmd/infrafile', '-run', '^TestExportOpsBrowserFixture$', '-count=1'], { cwd: root, env, stdio: 'inherit', timeout: 120000 });
for (const [suite, options] of [
  ['browser', {}],
  ['read_browser', {}],
  ['read_browser', { INFRAFILE_OPS_MULTI_PROFILE: '1' }],
  ['read_browser', { INFRAFILE_OPS_READ_PROFILE: 'host-read-standby' }],
  ['action_browser', {}],
  ['action_browser', { INFRAFILE_OPS_MULTI_ACTION: '1' }],
  ['action_browser', { INFRAFILE_OPS_REFRESH: '1' }],
  ['recovery_browser', {}],
  ['saved_read_browser', {}],
  ['criteria_browser', {}],
]) {
  execFileSync(process.execPath, [path.join(__dirname, `test_ops_${suite}.cjs`)], { cwd: root, env: { ...env, ...options }, stdio: 'inherit', timeout: 120000 });
}
console.log('Ops browser suites passed using fictional fixtures');
