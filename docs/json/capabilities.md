# 能力目录 JSON 契约

`infrafile capabilities --json` 与 MCP 的 `infrafile_capabilities` 返回相同的离线目录。CLI 不接收位置参数、不需要数据仓；MCP 输入仅为 `{}`，仍使用启动时绑定的数据仓。查询不解析凭据、不访问网络、不执行外部插件、不写文件。成功退出 0；错误参数退出 2，stdout 为空。

顶层字段固定为 `{version, kinds, plugins, external_details}`，`version` 为 `v1`。完整输出由 `cmd/infrafile/testdata/golden/capabilities-json.txt` 锁定。

| 字段 | 含义 |
| --- | --- |
| `kinds` | 可人工登记的六种台账记录类型，沿用 Schema 的名称 |
| `plugins[].plugin` | 内置插件名；不枚举本机外部可执行文件 |
| `plugins[].methods` | 现有 v1 插件 describe 声明的方法；Cloudflare 为 sync/probe/control，其余为 sync |
| `plugins[].services[]` | 一个服务或动作的能力条目；顺序稳定，新增条目允许追加 |
| `external_details` | 固定为 `unknown`；未查询外部插件的字段、动作或验收状态 |

每个服务条目包含 `service`、可选 `action`、`fields`、`stages`、`permissions`、`conditions`、`verification`。`fields` 是该条目声明的观测或控制字段范围，不是完整原始 API 响应；字段不保证每次存在。天翼注册域名条目显式为 unsupported；百度 CDN 列表仅声明 name。

`stages` 固定包含六项：`register` 表示从该来源生成登记草案（不代表自动纳管或任意创建资产），`observe` 表示采集或控制前探测，`compare` 表示现有离线审计比较，`plan` / `execute` / `readback` 分别表示单动作计划、执行、配置回读。服务内不同字段的额外限制由 conditions 声明；例如 Cloudflare 回源只观测 hosts/port，协议不可观测。

| 状态 | 含义 |
| --- | --- |
| `supported` | 在声明范围内实现且没有额外配置或范围前提；当前目录没有此类条目 |
| `conditional` | 已实现，但依赖配置、权限、绑定、观测或动作范围；须同时阅读 permissions / conditions |
| `unsupported` | 当前未实现；不能据此发起远端操作 |
| `unknown` | 没有足够描述，不能当作支持或正常 |

`permissions` 是实现所需的权限前提，不是当前账号授权检测结果；未核实的最小 IAM 会明确标注。`verification` 当前保守使用 `automated-only`（自动化测试覆盖实现）或 `unverified`（未实现条目），不复用其他版本的真实账号结果冒充本目录验收。TLS 仅观测服务证书，不登记证书资产；Cloudflare pause/resume 作用于整个 zone。回读一致只证明配置接受，不证明流量、源站或 TLS 业务健康。

目录与内置插件的方法声明、Cloudflare 动作拒绝共用数据。未支持的方法/服务/动作沿用原有校验，在凭据解析及远端调用前拒绝。旧插件的 describe v1 结构及 Schema 保持不变，未增加必需字段；新增目录属于 CLI/MCP 查询契约，不改变插件握手。旧数据、快照及已支持 MCP 工具的参数和结果保持原有含义。本任务未新增持久化格式；完整版本、升级和回退策略由 T02 定义。
