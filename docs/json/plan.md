# plan / plan show JSON 契约（v1）

`plan cdn-pause|cdn-resume|cdn-origin Resource/<名称> [--dir .] [-o 文件] [--timeout 30s]` 生成计划；cdn-origin 另需 `--host`。`plan show <文件>` 校验并展示。`--json` 均返回计划本身：

```json
{
  "version": "v1",
  "generated_at": "2026-12-01T12:00:00Z",
  "actions": [
    {
      "target": "Resource/edge",
      "binding": {"plugin": "cloudflare", "instance": "main", "zone_id": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
      "record_hash": "<64 位小写十六进制>",
      "config_hash": "<64 位小写十六进制>",
      "operation": "cdn-pause",
      "parameters": {},
      "before": {"zone_id": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "name": "example.com", "paused": false},
      "before_hash": "<64 位小写十六进制>",
      "after": {"zone_id": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "name": "example.com", "paused": true}
    }
  ]
}
```

例中的哈希为说明占位，不能 apply。actions 必须恰好一项；暂停／恢复 parameters 为 `{}`，改回源为 `{"host":"origin.example.net"}`，binding 另含 ruleset_id、rule_id。Cloudflare Origin Rules 观测含 ruleset_id 和完整有序 rules，不含版本号与更新时间。

带 `-o` 只创建文件，JSON stdout 仍返回同一计划；不带 `-o` 时，即使未指定 `--json`，stdout 也输出可保存的计划 JSON。show 的文本模式显示前后状态，JSON 模式保留所有字段。计划是普通 JSON，不含凭据；可读格式与实际执行字段相同。

退出 0 成功，2 无法生成／读取／校验或输出失败；错误只写 stderr，正常失败路径不输出部分计划。不接受未知结构字段、重复 JSON 键、无效版本或哈希、额外预期修改、多个动作、敏感字段。语义和权限见[受控写指南](../control.md)。
