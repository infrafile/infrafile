# MCP 工具 JSON 契约

`infrafile mcp` 使用官方 Go SDK 的 stdio JSON-RPC。握手与传输版本由 SDK 协商；`--json` 不生成额外 CLI 输出。每个工具结果在 `structuredContent` 和单个 text content 的 JSON 中提供语义相同的对象，业务对象顶层 `version: "v1"`。

完整工具名、描述、参数 Schema、annotations 和典型结果由 `cmd/infrafile/testdata/golden/mcp.json` 锁定。参数禁止额外字段及重复 JSON 键。选择规则见[Agent 指南](../agent.md)。

| 工具 | 结果对象 |
| --- | --- |
| infrafile_list | `{version, records: [{key, kind, name, archived, labels}]}`；archived 是布尔，未填写 labels 时为 null |
| infrafile_show | 与 [show](show.md) 相同的 file / record / incoming / outgoing 字段；递归省略 secrets |
| infrafile_expiry | 与 [expiry](expiry.md) 相同，再按选择条件过滤 entries 并重算 ok |
| infrafile_audit | 与 [audit](audit.md) 相同，保留发现、豁免、快照时间及提示 |
| infrafile_validate | `{version, ok, records, problem_count, codes}`；codes 为问题代码 → 个数，无详细消息或文件路径 |
| infrafile_schema | `{version, schemas}`；schemas 等于 `schema export` 的 JSON bundle |
| infrafile_plan | `{version, dry_run: true, plan}`；plan 为 [plan](plan.md) 的 v1 单动作文件对象 |

expiry 的 within_days 为非负整数，默认 30；沿用 CLI 的有效日期范围校验。发现到期项返回 ok=false，仍是成功的工具调用（isError=false）。validate 发现台账问题同样返回 ok=false / isError=false；无法加载则是工具失败。list / show / expiry / audit / plan 先校验整个台账，失败时不返回部分业务结果。

audit 仅接受可选的 rules（cdn_drift / origin_exposure / orphan / zombie / stale_state 的 ID 数组）和 max_age_days（非负整数，须能表示为 Go int）。省略 rules 或传空数组时运行全部就绪规则；重复 ID 只运行一次。默认运行前四条；stale_state 需要 max_age_days，显式点名却未提供阈值时失败。未豁免发现返回 ok=false / isError=false；无快照不报缺失；不能传 key / labels / kind、路径或命令。未知规则、额外字段与重复键均被拒绝。审计不调用插件、不访问网络、不读取凭据值。精确 match、自然身份、源站证据和 CDN 状态映射与 [CLI audit 契约](audit.md) 相同，豁免从数据仓配置读取，不能通过工具参数临时覆盖。

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

error.code 为 arguments（参数）、request（无法完成，包括不存在 / 不唯一 / 插件失败）、cancelled（超时或取消）、sensitive（动态结果敏感检查失败）、size（编码或结果大小限制）。客户端主动取消可直接收到 SDK 的取消错误；未知工具、SDK 参数解析失败等是协议错误，不属于业务信封。协议响应出口保留原错误码与请求 ID，替换 message 并省略 data；错误不回显参数或云端原文。

全部工具标记 readOnlyHint=true、idempotentHint=true；仅 plan 的 openWorldHint=true，因为它会访问已配置的插件 / 云端。只读提示不代表不使用凭据；计划可在内部解析引用以做观测，不向客户端返回明文。MCP 不暴露执行和通用 I/O 工具。
