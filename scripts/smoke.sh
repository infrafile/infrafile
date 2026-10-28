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
print('quickstart smoke passed')
PY
