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
"$binary" agent install codex --dir "$work"
"$binary" agent guide > "$work/guide.md"
cmp "$work/guide.md" "$work/.agents/skills/infrafile/SKILL.md"
python3 - "$work" <<'PY'
import json, pathlib, sys
root = pathlib.Path(sys.argv[1])
def read(name):
    return json.loads((root / name).read_text())
assert read('server.json')['record']['metadata']['name'] == 'web1'
assert read('app.json')['record']['kind'] == 'App'
assert all(x['status'] != 'created' for x in read('repeat.json')['records'])
assert 'NO_PURGE' in (root / 'dnsconfig.js').read_text()
assert 'Host web1' in (root / 'ssh.conf').read_text()
print('quickstart smoke passed')
PY
