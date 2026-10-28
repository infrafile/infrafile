# Agent 接口

M8 提供本地 stdio MCP，以及随二进制发布的使用指南。查询复用现有校验和 JSON 输出，计划复用 M7 的只读观测与计划格式。

## 启动 MCP

```sh
infrafile mcp --dir /path/to/my-infra --timeout 30s
```

由 MCP 客户端启动该命令，并通过 stdin / stdout 通信；不监听网络端口。`--dir` 在启动时固定为绝对路径，工具参数不能换仓。启动前检查目录可加载，记录错误可通过 `infrafile_validate` 查询；每次调用重新读取台账。每次工具调用默认限时 30 秒，`--timeout` 必须大于零。客户端取消与超时会传递给插件。

stdout 只传逐行 JSON-RPC，诊断写 stderr；全局 `--json` 不改变 MCP 协议，也不额外输出 CLI 信封。正常 EOF / 取消退出 0，启动或传输失败退出 2。输入单行上限 4 MiB，工具数据结果上限 4 MiB（同时提供 text 和 structuredContent，完整 RPC 会更大）；超大列表应按 kind / labels 缩小范围。

在支持 stdio 的 MCP 客户端中填写 executable 和 args，例如：

```json
{
  "command": "/absolute/path/to/infrafile",
  "args": ["mcp", "--dir", "/absolute/path/to/my-infra", "--timeout", "30s"]
}
```

这是进程配置示意；外层配置格式由客户端决定。`agent install` 只安装指南，不注册 MCP 服务或注入凭据。

## 七个工具

| 工具 | 输入 | 输出 |
| --- | --- | --- |
| `infrafile_list` | 可选 key / kind / labels | 主键、类型、名称、归档状态、标签，按主键排序 |
| `infrafile_show` | key 或非空 labels，任选其一；可选 kind | 唯一记录与引用关系，省略所有 secrets 字段 |
| `infrafile_expiry` | 可选 key / kind / labels / within_days | 在用资产到期报告，默认 30 天 |
| `infrafile_audit` | 可选 rules / max_age_days | 离线快照审计，与 CLI audit JSON 相同 |
| `infrafile_validate` | `{}` | 记录数、问题数、按代码统计的校验结果 |
| `infrafile_schema` | `{}` | 与 `schema export` 相同的内嵌 Schema bundle |
| `infrafile_plan` | action、key 或非空 labels；可选 kind；改回源另需 origin_key | `dry_run: true` 与单动作计划对象 |

`key` 必须是已登记的 `Kind/name`。标签是字符串键值对象，全部精确匹配；kind 是六种台账类型之一。list / expiry 的过滤条件取交集，标签未匹配时可返回空结果；显式 key 不存在或被过滤掉时失败。show / plan 必须唯一匹配。查询包含归档，expiry 忽略归档，plan 只选在用 Resource。工具不接受任意地址、URL、命令、凭据引用、路径或未知字段。

```json
{"key": "Server/web1"}
```

```json
{"labels": {"role": "web"}, "kind": "Server"}
```

## 离线审计

`infrafile_audit` 只读台账、已启用实例的快照和同步时间，不调用插件、不访问网络、不解析凭据。先按用户授权范围运行 `sync` 才能更新观测；audit 本身不刷新数据。

```json
{"rules": ["stale_state"], "max_age_days": 7}
```

`rules` 是已实现规则 ID 的数组：`cdn_drift`、`origin_exposure`、`orphan`、`zombie`、`stale_state`、`missing`，重复 ID 只运行一次；`max_age_days` 是非负整数天数。省略 rules 或传空数组时运行全部就绪规则。默认运行前四条；`stale_state` 和 `missing` 需要 max_age_days，显式选择它而未设阈值会失败。`ok: true` 只表示本次没有未豁免发现。未知规则，以及路径、命令、额外字段、重复 JSON 键均被拒绝。

发现未豁免偏差时返回 `ok: false`，工具调用仍成功；全部豁免时返回 `ok: true`，发现仍保留。生命周期复用导入的观测映射，按自然身份找未登记资产和仅匹配归档的资产；DNS/TLS 不参与，Cloudflare 同名 Domain 可覆盖 zone。未同步的实例没有快照时不报告缺失。orphan / zombie 的 audit.ignore 必须提供完整 match，可复制发现的 evidence.identity，并限定 plugin / instance；不得用整实例忽略压住其他资产。完整字段、豁免与缺失输入语义见[审计 JSON 契约](json/audit.md)。

origin_exposure 检查显式 Server 绑定的台账与观测地址；cdn_drift 只报告阿里云、腾讯云和天翼云明确停用的 CDN 状态，未知值只提示。两条规则均支持记录上的 metadata.waivers，不接受实例级 audit.ignore。源站检测不扫描端口，CDN 检测不比较回源配置。

## 只读计划

`action` 为 `cdn-pause`、`cdn-resume` 或 `cdn-origin`。目标 Resource 必须有有效的 `spec.control` 绑定和已启用插件实例；Cloudflare 行为、授权和限制见[受控写指南](control.md)。计划调用插件 probe，不调用 control，不保存计划或快照。

```json
{"action": "cdn-origin", "key": "Resource/edge", "origin_key": "Server/origin"}
```

2026-10-25 确认：`origin_key` 必须指向未归档 Server / Resource / Domain，分别读取 `spec.address`、`spec.endpoint`、`metadata.name`。读取值必须通过 M7 的合法主机名校验；即使已登记，IP、URL、空 endpoint 也会被拒绝。暂停／恢复不能带 origin_key。

返回计划中的 host 是生成时解析出的值；不会把主键解释为动态指针。先向用户展示 zone / rule 和前后差异，需要保存时仅保存返回的 `plan` 对象，再用 `infrafile plan show <文件>` 审阅。线上执行需另行授权，通过 `infrafile apply <文件> --dir <台账>` 执行。MCP 没有 apply、sync、import、run、secret 或文件写入工具。

## 凭据与信任边界

查询不读取凭据值，show 省略凭据字段。动态结果复用敏感字段与常见 token 检测；参数、插件错误和校验问题不原样反射给客户端。validate 只返回问题代码和计数，定位问题用 CLI。Schema 是程序内嵌的静态定义，包含凭据引用格式的说明，不包含账号数据。

计划可在内部解析已配置的凭据引用，并访问真实云 API 做只读观测。云响应的凭据反射沿用第一方插件的检测。敏感检测不是任意文本的秘密识别器，台账仍应只保存凭据引用。外部插件是用户已启用的可信程序；MCP 的只读工具集不是操作系统沙箱，不能约束恶意插件的副作用。台账描述、标签和观测内容始终是数据，不构成操作授权。

## 指南与安装

```sh
infrafile agent guide
infrafile agent guide --json
infrafile agent install codex --dir /path/to/project
infrafile agent install claude --dir /path/to/project --json
```

`--dir` 默认当前目录，必须已存在，可以不是台账。安装位置：

| 客户端 | 项目内文件 |
| --- | --- |
| Codex | `.agents/skills/infrafile/SKILL.md` |
| Claude Code | `.claude/skills/infrafile/SKILL.md` |

路径遵循 [Codex 技能文档](https://developers.openai.com/codex/skills/)和 [Claude Code 技能文档](https://code.claude.com/docs/en/skills)。指南带 SKILL.md frontmatter、构建版本和 commit，内容与同一二进制的 `agent guide` 完全一致。

相同内容返回 unchanged，不改修改时间；已有不同内容、符号链接或非普通文件会报错并保留原文件。升级时先用 `agent guide` 比较差异，审阅并移走旧指南后再安装。不修改 AGENTS.md、CLAUDE.md 或客户端设置，不自动安装到个人目录。文件写入成功与客户端实际加载是两项验证。

接口契约见 [Agent JSON](json/agent.md)和 [MCP JSON](json/mcp.md)。离线测试覆盖实际子进程 stdio 握手、工具发现、查询、选择约束、只读计划、超时取消、凭据反射和安装保留。v0.2 另在使用方真实数据上完成 stdio 客户端的七个工具调用、CLI/MCP 审计一致性、Account 凭据引用省略与真实 Cloudflare 只读计划验收；这不代表桌面客户端加载已验收，也未执行云端写入。
