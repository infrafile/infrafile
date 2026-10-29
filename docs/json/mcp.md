# MCP 工具 JSON 契约

`infrafile mcp` 使用官方 Go SDK 的 stdio JSON-RPC。握手与传输版本由 SDK 协商；`--json` 不生成额外 CLI 输出。每个工具结果在 `structuredContent` 和单个 text content 的 JSON 中提供语义相同的对象，业务对象顶层 `version: "v1"`。

完整工具名、描述、参数 Schema、annotations 和典型结果由 `cmd/infrafile/testdata/golden/mcp.json` 锁定。参数禁止额外字段及重复 JSON 键。选择规则见[Agent 指南](../agent.md)。

| 工具 | 结果对象 |
| --- | --- |
| infrafile_list | `{version, records: [{key, kind, name, archived, labels}]}`；archived 是布尔，未填写 labels 时为 null |
| infrafile_show | 与 [show](show.md) 相同的 file / record / incoming / outgoing 字段；递归省略 secrets |
| infrafile_expiry | 与 [expiry](expiry.md) 相同，再按选择条件过滤 entries 并重算 ok |
| infrafile_audit | 与 [audit](audit.md) 相同，保留发现、豁免、快照时间及提示 |
| infrafile_inspect | 与 [inspect](inspect.md) 相同的选定资产/直接关系、执行/覆盖/风险；可选 key / labels / kind / max_age_days，key 与 labels 互斥 |
| infrafile_context | 与 [context](context.md) 相同的限定登记/一层关系、inspection、匹配观测、条件动作提示；key 或非空 labels 二选一，可加 kind / max_age_days |
| infrafile_validate | `{version, ok, records, problem_count, codes}`；codes 为问题代码 → 个数，无详细消息或文件路径 |
| infrafile_schema | `{version, schemas}`；schemas 等于 `schema export` 的 JSON bundle |
| infrafile_capabilities | 与 [capabilities](capabilities.md) 相同的离线能力目录；输入为 `{}` |
| infrafile_plan | `{version, dry_run: true, plan}`；plan 为 [plan](plan.md) 的单动作对象：旧仓 v1，显式新存储仓 v2（含摘要及30分钟有效期）；均不保存或执行 |

expiry 的 within_days 为非负整数，默认 30；沿用 CLI 的有效日期范围校验。发现到期项返回 ok=false，仍是成功的工具调用（isError=false）。validate 发现台账问题同样返回 ok=false / isError=false；无法加载则是工具失败。list / show / expiry / audit / plan 先校验整个台账，失败时不返回部分业务结果。

inspect 同样先校验整个台账，使用共享离线实现；max_age_days 为可选非负 Go int。缺 TTL、证据不足或风险返回 ok=false / isError=false，成功生成报告不表示全部通过。参数不接受路径/地址/命令或临时豁免。正式v0.3原七工具保留；capabilities、inspect、context 为开发新增，共十工具。

context 要求明确的非空选择；最多64条选定、256条含一层关联登记、1024条关系/检查及4MiB结果，超限整份拒绝。Account等记录的秘密引用在共享层省略；未匹配、归档、未知实例和旧证据不冒充当前健康。next_actions 仅列本地绑定及能力目录支持的条件入口，不执行、不授权、不保证远端权限；台账指令文本始终是数据。CLI/Web同输入与时刻返回相同内容；取消/超时不返回部分成功。详见 [context](context.md)。

audit 仅接受可选的 rules（cdn_drift / origin_exposure / orphan / zombie / stale_state / missing 的 ID 数组）和 max_age_days（非负整数，须能表示为 Go int）。省略 rules 或传空数组时运行全部就绪规则；重复 ID 只运行一次。默认运行前四条；stale_state 和 missing 需要 max_age_days，显式点名却未提供阈值时失败。未豁免发现返回 ok=false / isError=false；无有效快照不报缺失，声明的 watch 目标输出 missing_unknown；不能传 key / labels / kind、路径或命令。未知规则、额外字段与重复键均被拒绝。审计不调用插件、不访问网络、不读取凭据值。精确 match、自然身份、源站证据和 CDN 状态映射与 [CLI audit 契约](audit.md) 相同，豁免从数据仓配置读取，不能通过工具参数临时覆盖。

v1.0 开发分支 audit / inspect 的 finding 兼容增加 issues 身份数组，与 CLI 同输入一致；字段和恢复语义见[问题身份](issues.md)。工具不推进或保存问题历史，不接受客户端提供“已恢复”结论。

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
