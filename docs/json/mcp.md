# MCP 工具 JSON 契约

`infrafile mcp` 使用官方 Go SDK 的 stdio JSON-RPC。握手与传输版本由 SDK 协商；`--json` 不生成额外 CLI 输出。每个工具结果在 `structuredContent` 和单个 text content 的 JSON 中提供语义相同的对象，业务对象顶层 `version: "v1"`。

完整工具名、描述、参数 Schema、annotations 和典型结果由 `cmd/infrafile/testdata/golden/mcp.json` 锁定。参数禁止额外字段及重复 JSON 键。选择规则见[Agent 指南](../agent.md)。

| 工具 | 结果对象 |
| --- | --- |
| infrafile_list | `{version, records: [{key, kind, name, archived, labels}]}`；archived 是布尔，未填写 labels 时为 null |
| infrafile_show | 与 [show](show.md) 相同的 file / record / incoming / outgoing 字段；递归省略 secrets |
| infrafile_expiry | 与 [expiry](expiry.md) 相同，再按选择条件过滤 entries 并重算 ok |
| infrafile_validate | `{version, ok, records, problem_count, codes}`；codes 为问题代码 → 个数，无详细消息或文件路径 |
| infrafile_schema | `{version, schemas}`；schemas 等于 `schema export` 的 JSON bundle |
| infrafile_plan | `{version, dry_run: true, plan}`；plan 为 [plan](plan.md) 的 v1 单动作文件对象 |

expiry 的 within_days 为非负整数，默认 30；沿用 CLI 的有效日期范围校验。发现到期项返回 ok=false，仍是成功的工具调用（isError=false）。validate 发现台账问题同样返回 ok=false / isError=false；无法加载则是工具失败。list / show / expiry / plan 先校验整个台账，失败时不返回部分业务结果。

工具执行失败使用 `isError: true`，同时提供固定消息：

```json
{
  "version": "v1",
  "ok": false,
  "error": {
    "code": "arguments",
    "message": "参数不合法；目标必须是已登记主键或标签"
  }
}
```

error.code 为 arguments（参数）、request（无法完成，包括不存在 / 不唯一 / 插件失败）、cancelled（超时或取消）、sensitive（动态结果敏感检查失败）、size（编码或结果大小限制）。客户端主动取消可直接收到 SDK 的取消错误；未知工具等路由问题是协议错误，不属于业务信封。错误不回显用户输入或云端原文。

全部工具标记 readOnlyHint=true、idempotentHint=true；仅 plan 的 openWorldHint=true，因为它会访问已配置的插件 / 云端。只读提示不代表不使用凭据；计划可在内部解析引用以做观测，不向客户端返回明文。MCP 不暴露执行和通用 I/O 工具。
