# plan / plan show JSON 契约（v1 / v2）

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

计划文件上限为 4 MiB；生成器检查最终格式化 JSON 的大小，超出时不输出、不创建文件。需要由插件缩小观测范围后重新生成。

带 `-o` 只创建文件，JSON stdout 仍返回同一计划；不带 `-o` 时，即使未指定 `--json`，stdout 也输出可保存的计划 JSON。show 的文本模式显示前后状态，JSON 模式保留所有字段。计划是普通 JSON，不含凭据；可读格式与实际执行字段相同。

退出 0 成功，2 无法生成／读取／校验或输出失败；错误只写 stderr，正常失败路径不输出部分计划。不接受未知结构字段、重复 JSON 键、无效版本或哈希、额外预期修改、多个动作、敏感字段。语义和权限见[受控写指南](../control.md)。

显式启用 `storageVersion: v1` 的仓库生成 `version: v2`，保留上述单动作字段，并增加：

```json
{"expires_at":"2026-12-01T12:30:00Z","digest":"<完整计划摘要>"}
```

`expires_at` 必须恰好是 `generated_at` 后 30 分钟。`digest` 为移除 digest 字段后，对 Plan 结构使用 Go encoding/json 编码所得字节的 SHA-256（稳定对象键序、HTML 转义及原 JSON 数字精度/写法）；不是 RFC 8785，也不是原文件字节哈希。缩进、换行和对象成员顺序不改变摘要，字段值、数字写法、生成时间或有效期变化会改变摘要。show 文本显示摘要和有效至时间；JSON 返回完整 v2 对象。

v2 执行要求独立提供已审阅摘要、授权来源引用与主体说明，详见 [apply](apply.md)。v1 文件仍可读取/展示；旧仓继续生成和执行 v1，不自动启用存储。已启用新存储的仓库拒绝执行旧 v1 计划，须重新生成并审阅 v2。MCP 按相同规则生成，不保存计划或回执。

新生成的 v2 另含 `scope: {level, zone_name?, rule_expression?, related, unknown}`，纳入 digest。Cloudflare 暂停/恢复 level=zone，改回源为 origin_rule；其它插件为 plugin_target，实际语义未知，不套用 Cloudflare 的作用范围。标识来自 action.binding，zone_name/规则表达式来自计划原始观测；CLI 文本以安全引用显示标识和表达式。

related 最多256条，每项 `{key,via:[reference|zone_hostname],record_hash}`，按主键及来源排序。只包括在用记录：观测 zone 名称下的 Domain 名称、Resource endpoint、Server address，以及目标/这些主机名登记的一层入出引用；不沿账号扩展到其它无关资源，不执行描述文本或解析 DNS。URL/端口只本地取主机名，标签边界匹配避免相似后缀误入。只保存关联主键、来源和整条登记的哈希，不复制凭据引用或原始记录。

unknown 明确保留云端清单不全、实际流量/业务未验、关联不代表必然影响、传递依赖未评估、权限/套餐未验；Cloudflare 另标 DNS 委派未核对，缺观测名称/规则表达式及外部插件语义各有明确未知项。超限或取消整份拒绝，不截断关联。执行开始和控制前重算带 scope 计划的关联/哈希，新增、移除或修改相关登记会拒绝；无关登记改动不扩大执行范围。

兼容先前不含 scope 的 v2 文件：仍可读取并沿用原 T15 执行前提，CLI 显示范围未保存，不据此宣称已知影响；真实 T16 验收须重新生成有 scope 的计划。v1 不接受 scope。完整准备/审阅/执行与恢复流程见[Agent 场景验收](../agent-acceptance.md)。
