---
id: infrafile-architecture
type: architecture-design
title: infrafile 架构与设计
status: draft
parent: infrafile-goals
---

# infrafile 设计

本文在原位置纳入 spec graph，产品目标见 `infrafile-goals`。原有已确认决定继续保留；新增模块边界根据当前代码整理，其作为长期依赖约束的意图尚待维护者确认。v1.0 开发中的 `serve` 已交付认证、资产/巡检/问题变化三页、离线只读 API 和显式启用的共享台账编辑；云计划/回执索引仍待 T15，T12 相应集成验收保持开放。

**状态**：设计已确认（2026-10-24），M0–M9 已完成，v0.1.0 已发布（2026-10-25）。实现与验收见 [v0.1 实现计划](plan/v0.1.md)。

2026-10-28：[v0.2.0](https://github.com/infrafile/infrafile/releases/tag/v0.2.0) 已发布，包含离线审计、偏差豁免和 Gatus 配置生成；签名产物与 Homebrew 升级验收完成，范围与延后项见 [v0.2 计划](plan/v0.2.md)。

2026-10-28：[v0.3.0](https://github.com/infrafile/infrafile/releases/tag/v0.3.0) 已发布，包含执行回执、显式范围 missing、CDN 回源漂移和 App 部署源站检查。真实只读验收与使用接入的剩余项见 [v0.3 方案](plan/v0.3.md)；Web UI 仍排在审计体系稳定之后。

2026-10-28：后续按 [v1.0 完整方案](plan/v1.0.md)推进，以真实台账长期使用、外部独立上手和 Agent 受控流程为发布门槛；[任务拆分](plan/v1.0-tasks.md)包含 6 个工作包、20 个任务。新增能力尚未实现，当前行为仍以各命令文档为准。

> 标「建议」的是落实时的默认做法，可以调整；其余是已定的决定。

## 定位

面向个人和小团队：一个以 git 为后端的基础设施资产台账，加上凭据引用、多云只读同步和受控写，并且对 AI agent 友好。

已有的开源项目中，NetBox、Infrahub 偏重型 CMDB，Steampipe、CloudQuery 只做查询，没有同时具备「git 明文台账、凭据引用、观测快照进 git、受控写、agent 接口」这几样的。这块空位就是 infrafile 要做的事。

**不做的事**（能复用的不自己造）：
- 不做重型 CMDB。
- 不做通用云资产查询，交给 Steampipe / CloudQuery 的外部插件。
- DNS 写入交给 DNSControl。
- 监控引擎交给 Gatus。
- 不做密码管理器：内置 sops + age，外部后端只做引用。

## 项目约定

| 项 | 决定 |
|---|---|
| 语言 | Go 单二进制；sops 和 age 以库的方式链接进来 |
| 命令 | `infrafile`，安装时附带短别名 `ifl`；文档、脚本和 agent 指南一律写全名 |
| 仓库 | 只放工具代码和虚构示例。**工具与数据分离**，用户的真实台账放在用户自己的仓库里 |
| 许可证 | Apache-2.0 |
| 文档语言 | 中文为主 |
| 治理 | BDFL + DCO，见 [CONTRIBUTING](../CONTRIBUTING.md) |
| 分发 | GitHub Release（goreleaser，多平台二进制 + checksum + 签名）+ Homebrew tap |

## 模块拓扑与边界

CLI 是装配入口；数据仓是文件协议，不引入常驻数据库。以下是生产 Go 代码当前的直接项目依赖，测试依赖不在此表中。各模块的职责和本地契约由对应 `SPEC.md` 说明；共享依赖关系只在这里叙述。

| 模块 spec id | 直接依赖的模块 spec id |
|---|---|
| `infrafile-cli` | `infrafile-agent`、`infrafile-audit`、`infrafile-control`、`infrafile-importer`、`infrafile-ledger`、`infrafile-plugin`、`infrafile-secret`、`infrafile-validate` |
| `infrafile-audit` | `infrafile-ledger`、`infrafile-observation`、`infrafile-plugin`、`infrafile-snapshot` |
| `infrafile-control` | `infrafile-ledger`、`infrafile-plugin`、`infrafile-protocol` |
| `infrafile-importer` | `infrafile-ledger`、`infrafile-observation`、`infrafile-snapshot`、`infrafile-validate` |
| `infrafile-plugin` | `infrafile-ledger`、`infrafile-observation`、`infrafile-secret`、`infrafile-snapshot`、`infrafile-validate`、`infrafile-protocol` |
| `infrafile-snapshot` | `infrafile-ledger`、`infrafile-validate`、`infrafile-protocol` |
| `infrafile-validate` | `infrafile-ledger`、`infrafile-observation` |
| `infrafile-observation` | `infrafile-ledger` |
| `infrafile-ledger`、`infrafile-secret`、`infrafile-agent`、`infrafile-protocol` | 无项目内依赖 |

`internal/testutil` 仅为测试辅助，不是产品模块。`protocol` 是外部插件可复用的公开包；其余库受 Go `internal` 可见性限制，CLI 入口不被库反向导入。

审计对 plugin 的依赖只使用稳定 JSON 哈希等无 I/O 的辅助函数；它不打开插件或解析凭据。同步写入与快照读取刻意分属 plugin、snapshot：snapshot 提供共享读取和状态解析，不负责采集或更新文件。将这些当前边界作为长期禁止反向依赖的规则，是本次整理的推断，尚待确认；代码没有专门的架构 lint。

## 数据模型

### 每条记录一个文件 + 轻量信封

每条记录是一个 YAML 文件，结构参照 Backstage 的 `apiVersion/kind/metadata/spec`。这样 diff 和合并冲突都只落在单条记录上，外部工具也容易读写。

```yaml
apiVersion: infrafile/v1alpha1
kind: Server
metadata:
  name: web1            # 主键，同 kind 内唯一，也是外键引用的目标
  labels: {env: prod}
spec:
  provider: example-cloud
  account: example-main  # 外键 → Account
  address: 203.0.113.10
  ssh: {user: deploy, port: 22}
  expires_at: 2027-03-01
  secrets:
    root_password: sops://secrets.enc.yaml#servers.web1.root_password
```

- **kind**：初始有六种：Server / App / Domain / Account / Resource / Repo。
- **schema**：字段用声明式 schema 定义，**同时发布 JSON Schema**。校验和 `show` 的外键关系提取复用这份 schema；MCP 的 `schema` 工具返回相同 bundle，工具参数 Schema 则由 MCP 适配层单独定义。
- **外键完整性**：任何引用都必须指向存在的记录；在用记录不能引用已归档记录，归档记录可以保留到归档记录的历史引用。校验失败时退出码非零。
- **下线不删除**：下线的记录归档而不是删除，保留历史事实。
- **人写的和机器写的分开**：
  - sync 产生的机器观测只写 `state/` 目录。
  - 人维护的记录放在 inventory 目录；sync、audit 和 plan / apply 不改这些记录。显式 `import --write` 只创建不存在的记录，不覆盖人写字段。
  - v1.0 规划共享编辑入口，管理意图可由人编辑或经明确授权的导入/变更流程写入；该入口尚待实现，不能把 observed 自动提升为 desired。

### 凭据引用：统一 URI

记录里只放引用，不放值。

| 写法 | 后端 |
|---|---|
| `sops://secrets.enc.yaml#servers.web1.root_password` | 内置 sops + age 加密文件；相对文件路径与以点分隔的字段路径 |
| `op://Vault/Item/field` | 1Password（调用 `op` CLI） |
| `env://CF_TOKEN` | 环境变量 |

- `secret check` 只检查引用能否定位，**不请求凭据值**：SOPS 不加载私钥、不解密，1Password 只请求字段 ID，env 检查变量是否存在。
- 需要用明文时，用 `infrafile run --secret NAME=<ref> -- <cmd>` 注入到子进程环境变量里，明文不落盘，也不出现在标准输出上。

M3 的具体支持范围、输出遮盖和子进程边界见[凭据指南](secrets.md)。

## 插件

- **内置和外部可执行两种形态，用同一套契约**。
- 外部插件和主程序之间走 **stdio JSON-RPC 2.0**：
  - 启动后先调用 `describe` 握手，插件声明协议版本和支持的能力（sync / probe / control / import）。
  - 协议用 JSON Schema 定义，任何语言都能写插件。
- **v0.1 第一方插件**：
  - Cloudflare。
  - 通用探测：DNS、TLS 到期、SSH 实测机器状态、WHOIS。
  - 阿里云、腾讯云、百度云：第一版只列出注册域名和 CDN；天翼云先支持 CDN，注册域名暂不支持（2026-10-25 确认，尚无可核验的公开列表 API）。
- Steampipe / CloudQuery 不内置，文档会给一个外部插件示例。
- 建议：快照 schema 加一层 deny-list 校验，防止插件把敏感字段写进 `state/`。

M4 已实现宿主、内置与 Python 虚构示例、递归 deny-list、独立实例超时和快照写入；配置、协议和失败行为见[插件指南](plugins.md)。

M5 九个第一方插件复用同一宿主，云请求只读、结果按字段投影；配置与已验证边界见[第一方插件指南](first-party-plugins.md)。

## 导入

- **从云端反向生成**：用 sync 结果生成记录草稿。规则是**发现只建不覆盖**：可以提议新增记录，但永远不覆盖人写的字段。
- **从 `~/.ssh/config` 导入**：生成 Server 记录。

M6 已实现两种离线导入，默认输出 YAML 草稿，支持待审目录和显式 `--write`；已有主键（含归档）不覆盖。2026-10-25 确认：注册域名和 WHOIS 生成 Domain，CDN 和 DNS zone 生成 Resource，SSH 实测生成 Server，DNS / TLS 仅作观测并报告跳过。具体行为见[导入指南](import.md)。

## Agent 接口

- **CLI 的 `--json` 输出是稳定契约**：输出结构带版本号，破坏性变更要升主版本。
- **本地 stdio MCP**：
  - 只能读，或者生成 dry-run plan；**永远不返回凭据明文**。
  - 工具参数只接受台账主键和标签，不接受原始 IP 或主机名；台账里没有的目标直接拒绝。
- **agent 指南内嵌在二进制里**：
  - `infrafile agent install` 把与当前版本匹配的指南写进 Claude Code、Codex 等工具的配置目录。
  - `infrafile agent guide` 直接打印指南。

M8 已实现六个只读查询／计划工具和项目技能安装；离线 audit 接入后 MCP 共七个工具。2026-10-25 确认改回源按在用 Server / Resource / Domain 主键取主机名；标签选择计划目标必须唯一。查询省略凭据字段，计划仅内部使用配置的凭据引用认证。工具目录固定、结果带版本；安装保留已有不同内容，不修改客户端 MCP 设置。见[Agent 指南](agent.md)。

## 受控写

- **`plan` 和 `apply` 分开**：`plan` 生成一份可审查的计划文件，`apply` 只执行审过的这份计划。
- 计划里记录生成时的观测状态；`apply` 前回读一次，状态变了就拒绝执行，避免审完到执行之间云端发生变化（TOCTOU）。
- **CDN** 的写操作通过插件的 control 能力执行，执行后回读确认。
- **DNS** 不直接写，只生成 DNSControl 配置，交给 DNSControl 去 preview / push。

M7 已实现，2026-10-25 确认：Cloudflare 暂停／恢复按 zone，改回源只改已有 Origin Rule 的目标主机；DNSControl 从新增 Domain.spec.dns 显式记录生成，固定 NO_PURGE。控制目标必须是未归档的 Resource 主键，并有明确插件实例及远端 ID；v1 计划包含单个动作。调用前比对台账、配置与远端状态，调用后再回读；没有服务端原子 CAS、自动重试或回滚。详见[受控写指南](control.md)。

## 安全默认值

以下规则要做成校验规则和默认行为，不能只写在文档里：
- 明文凭据不进台账。
- 下线的记录归档，不删除；`validate --base <git-ref>` 检查相对基线的删除，未提供基线时不推断历史删除。
- 每次修改后都要能通过校验。
- 对外域名不直接暴露源站 IP（v0.2 审计规则）。

## 版本规划

| 版本 | 范围 |
|---|---|
| **v0.1** | 账本核心（kind / schema / JSON Schema、外键校验、归档、到期检查、`show` 与关系图、`ssh-config`、`init` 脚手架）；凭据（内置 sops + age、外部引用后端、`run --secret`）；agent 接口（`--json`、只读 MCP、内嵌指南）；`sync` 写入 `state/`；上述插件与导入器；plan / apply 受控写 |
| **v0.2** | 审计与 drift 检测（孤儿记录、僵尸记录、CDN 配置漂移、源站暴露等）；waivers 偏差豁免；生成 Gatus 监控配置（单向渲染） |
| **v0.1 之后** | 内置 Web UI `infrafile serve`，读写同一个 git 仓库 |
| **v1.0 规划中** | 能力/预检、可信巡检与问题历史、单用户工作台、受控 Agent 流程、正式迁移及独立上手；按[工作包](plan/v1.0-tasks.md)交付 |

### waivers 偏差豁免（v0.2）

豁免写在记录上，删除记录时豁免一并消失，不会留下孤儿：

```yaml
metadata:
  waivers:
    - rule: origin_exposure
      reason: 内网管理面板，只对办公网放行   # 必填
      until: 2027-01-01                     # 可选；过期后失效，重新报警
```

`rule` 只能取 `stale_state`、`orphan`、`zombie`、`origin_exposure`、`cdn_drift`、`missing`。规则未知、`reason` 为空、`until` 不是日期时，`validate` 报 `schema`。豁免过期不算校验错误，由审计处理。

### `serve` 的既定要求

当前入口与安全契约见[工作台文档](serve.md)，台账事务见 [change](json/change.md)；发行包使用 Go 嵌入页面，不要求 Node 或另起前端服务。云计划/回执索引仍待 T15。

- **单用户**，可以跨设备登录。第一版不做协作者和多角色。
- 编辑台账时先看 diff、跑校验，保存后形成 git 提交。
- 基于过期版本提交时，**停止保存并提示**，不自动合并。
- **保存台账不等于线上已经变更**：线上变更只走 plan / apply，并展示回读结果。
