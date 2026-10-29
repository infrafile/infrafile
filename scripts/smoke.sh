#!/usr/bin/env bash
# 在临时副本运行快速上手；只使用虚构示例和内置 example 插件。
set -euo pipefail
binary="$(cd "$(dirname "$1")" && pwd)/$(basename "$1")"
example="$(cd "$2" && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
cp -R "$example" "$work/homelab"
demo="$work/homelab"

"$binary" version --json
"$binary" init "$work/empty"
"$binary" validate "$work/empty"
"$binary" validate "$demo"
"$binary" show Server/web1 "$demo" --json > "$work/server.json"
"$binary" show App/blog "$demo" --json > "$work/app.json"
if "$binary" expiry "$demo" --json > "$work/expiry.json"; then
  :
else
  test "$?" -eq 1
fi
"$binary" ssh-config "$demo" > "$work/ssh.conf"
"$binary" sync example --dir "$demo" --json
"$binary" import state --dir "$demo" --json > "$work/draft.json"
"$binary" import state --dir "$demo" --write --json > "$work/import.json"
"$binary" import state --dir "$demo" --write --json > "$work/repeat.json"
"$binary" validate "$demo"
"$binary" dnscontrol "$demo" > "$work/dnsconfig.js"
"$binary" gatus "$demo" --json > "$work/gatus.json"
if "$binary" audit "$demo" --json > "$work/audit.json"; then
  :
else
  test "$?" -eq 1
fi
if "$binary" audit "$demo" --rule stale_state --max-age 0d --json > "$work/stale.json"; then
  :
else
  test "$?" -eq 1
fi
"$binary" agent install codex --dir "$work"
"$binary" agent guide > "$work/guide.md"
cmp "$work/guide.md" "$work/.agents/skills/infrafile/SKILL.md"

# 新管理记录只在独立副本显式启用；不改原示例或旧仓的存储版本。
cp -R "$example" "$work/v1"
v1="$work/v1"
printf '\nstorageVersion: v1\n' >> "$v1/infrafile.yaml"
"$binary" validate "$v1"
"$binary" capabilities --json > "$work/capabilities.json"
"$binary" context "$v1" --key Account/cloudflare-main --max-age 7d --json > "$work/context.json"
"$binary" inspect "$v1" --max-age 7d --json > "$work/inspect.json"
"$binary" findings list "$v1" --json > "$work/history-empty.json"
"$binary" executions list --dir "$v1" --json > "$work/executions-empty.json"
test ! -e "$v1/state/.findings.json"
test ! -e "$v1/history"
"$binary" findings refresh "$v1" --max-age 7d --json > "$work/refresh.json"
"$binary" findings refresh "$v1" --max-age 7d --json > "$work/refresh-repeat.json"
"$binary" findings list "$v1" --json > "$work/history.json"
"$binary" findings changes "$v1" --after 0 --json > "$work/changes.json"
"$binary" inspect "$v1" --max-age 7d --json > "$work/inspect-history.json"
python3 - "$work" <<'PY'
import datetime, json, pathlib, sys
root = pathlib.Path(sys.argv[1])
def read(name):
    return json.loads((root / name).read_text())
assert read('server.json')['record']['metadata']['name'] == 'web1'
assert read('app.json')['record']['kind'] == 'App'
assert all(x['status'] != 'created' for x in read('repeat.json')['records'])
assert 'NO_PURGE' in (root / 'dnsconfig.js').read_text()
assert 'Host web1' in (root / 'ssh.conf').read_text()
gatus = read('gatus.json')
assert gatus['endpoints'] == ['example.com']
assert 'https://example.com/' in gatus['config']
assert '[CERTIFICATE_EXPIRATION] > 240h' in gatus['config']
audit = read('audit.json')
assert audit['rules'] == ['cdn_drift', 'origin_exposure', 'orphan', 'zombie']
assert len(audit['findings']) == 1
finding = audit['findings'][0]
assert finding['rule'] == 'origin_exposure' and finding['target'] == 'Domain/example.net'
today = datetime.datetime.fromisoformat(audit['as_of'].replace('Z', '+00:00')).date()
waived = today <= datetime.date(2027, 6, 30)
assert (finding['waiver'] is not None) == waived and audit['ok'] == waived
stale = read('stale.json')
assert not stale['ok'] and stale['findings']
assert all(f['rule'] == 'stale_state' for f in stale['findings'])
capabilities = read('capabilities.json')
assert capabilities['version'] == 'v1' and len(capabilities['kinds']) == 6
assert len(capabilities['plugins']) == 9
context = read('context.json')
account = next(r['record'] for r in context['records'] if r['selected'])
assert account['kind'] == 'Account' and 'secrets' not in account['spec']
assert context['inspection']['coverage']['unknown'] > 0 and not context['inspection']['ok']
inspection = read('inspect.json')
assert inspection['coverage']['unknown'] > 0 and not inspection['ok']
assert 'history' not in inspection
assert read('history-empty.json')['present'] is False
assert read('history-empty.json')['history'] is None
assert read('executions-empty.json')['executions'] == []
history = read('history.json')
assert history['present'] and history['history']['kind'] == 'FindingHistory'
first, repeat = read('refresh.json'), read('refresh-repeat.json')
assert first['written'] and repeat['changes'] == []
assert len(first['history']['data']['issues']) == len(repeat['history']['data']['issues'])
assert first['history']['data']['last_sequence'] == repeat['history']['data']['last_sequence']
assert read('changes.json')['changes'] == first['changes']
after = read('inspect-history.json')
assert after['history']['input_hash'] == history['history']['data']['input_hash']
assert after['coverage'] == inspection['coverage'] and not after['ok']
print('quickstart smoke passed')
PY
