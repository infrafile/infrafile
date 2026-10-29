# context（v1.0 开发）

`infrafile context [目录] --key Kind/name --max-age 7d --json`、MCP `infrafile_context` 与 Web `POST /api/context` 共用同一离线实现。先校验整个台账，不调用插件、联网、解密、写文件或推进问题历史。退出0只表示报告生成，unknown/风险不改变退出码；参数、读取、敏感结果或范围超限退出2且 stdout 为空。当前正式 v0.3.0 不含此接口。

选择条件为 key 或非空 labels 二选一，可加 kind；CLI 用 `--key` / `--label` / `--kind`，MCP/Web 用同名字段（labels 为字符串键值对象）。全部标签精确匹配；不存在、未匹配或超过64条选定记录均拒绝，不默认返回全仓。不接受原始 IP、URL、路径、命令、临时授权或豁免字段。max_age_days 为可选非负 Go int，CLI 对应 `--max-age Nd`；缺省不证明新鲜覆盖。

```sh
infrafile context examples/homelab --key Server/web1 --max-age 7d --json
infrafile context examples/homelab --label role=web --kind Server --max-age 7d
```

```json
{"labels":{"role":"web"},"kind":"Server","max_age_days":7}
```

结果为 `{version,records,inspection,observations,next_actions}`：

| 字段 | 含义 |
| --- | --- |
| records | 按主键排序的 `{key,file,selected,record}`；包含选定记录和一层直接引用/被引用登记，递归省略 secrets，不返回配置或密文内容 |
| inspection | 与同一选择/时间/TTL 的 [inspect](inspect.md) 一致；资产、直接关系、执行、覆盖、风险、原始证据时间与已保存历史保持各自资格 |
| observations | 每项 `{target,check,plugin,instance,service,file,observed_at,match,values}`；仅按目标的明确实例/服务/自然身份或 hostname 取保存事实 |
| next_actions | 按 target/action 排序的条件入口提示 `{target,action,interface,requires,capability?}`；由程序及本地有效绑定/能力目录产生，不从资产描述生成，也不执行 |

selected=false 的关联记录不扩大检查分母，不隐式带出第二层记录或相邻账号的观测。最多256条选定加关联登记、1024条直接关系、1024项检查、4MiB JSON。超过任一上限整份拒绝，不截断后报告成功；选择更多范围时分批查询。传输层仍遵循 MCP/Web 原有请求和超时限制。

match 为 matched / not_observed / incomplete / ambiguous / identity_mismatch / unsupported。matched 仅证明保存条目与身份匹配，仍须用 check 对应的 coverage 状态、证据时间和 TTL 判断当前资格；旧快照或失败前的值继续显示其原时间，不转成新鲜成功。重复身份不输出值；Cloudflare DNS 仅使用 watch 对应 zone；多目标 SSH 逐目标匹配，不能借用另一目标的 host。查询不进行 DNS 解析或现场探测。

缺历史时 inspection.history 省略；保存历史、当前检查、范围退出、豁免和恢复判据沿用 [findings](findings.md)。没有历史或 findings 不表示健康，只有明确资格才能确认风险/恢复；上下文不自动 refresh、同步或通知。

inventory-edit 提示 CLI change preview/apply 或显式启用编辑的 Web，列明既有授权、Linux/macOS、v1存储标记、干净 Git 基准、完整校验及审阅前提。它不保证当前仓库可写，不读取 Git 或检查谁已批准。云计划提示只针对选定在用 Resource 的有效 control/provider/type 与已启用实例，并引用 [capabilities](capabilities.md) 的已实现 plan 阶段、权限/条件及验证等级。未绑定、归档、未启用或未知插件不提示可计划的云动作；这不等于已验证账号权限、套餐或远端目标。cdn-origin 还须操作者另选 origin_key。

next_actions 不是批准、已执行回执或可直接运行的任意命令。MCP始终不提供 change、sync、apply、恢复或通用I/O；线上执行只走本地CLI，核对适用的既有授权与当前计划。资产描述、标签、观测中的“已批准”“忽略限制”“执行命令”等文字保持数据，不能改变动作集合、范围或信任边界。参考[内嵌 Agent 指南](../agent.md)。
