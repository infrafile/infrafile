# infrafile 设计

**状态**：设计已确认（2026-10-24），M0–M3 已完成。实现顺序见 [v0.1 实现计划](plan/v0.1.md)。

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
- **schema**：字段用声明式 schema 定义，**同时发布 JSON Schema**。校验、`show` 和 MCP 工具描述都从这份 schema 生成。
- **外键完整性**：任何引用都必须指向一条存在的 live 记录，校验失败时退出码非零。
- **下线不删除**：下线的记录归档而不是删除，保留历史事实。
- **人写的和机器写的分开**：
  - 机器只写 `state/` 目录（观测快照）。
  - 人写的记录放在 inventory 目录，机器永远不改。

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
  - 阿里云、腾讯云、百度云、天翼云：第一版只列出域名和 CDN。
- Steampipe / CloudQuery 不内置，文档会给一个外部插件示例。
- 建议：快照 schema 加一层 deny-list 校验，防止插件把敏感字段写进 `state/`。

## 导入

- **从云端反向生成**：用 sync 结果生成记录草稿。规则是**发现只建不覆盖**：可以提议新增记录，但永远不覆盖人写的字段。
- **从 `~/.ssh/config` 导入**：生成 Server 记录。

## Agent 接口

- **CLI 的 `--json` 输出是稳定契约**：输出结构带版本号，破坏性变更要升主版本。
- **本地 stdio MCP**：
  - 只能读，或者生成 dry-run plan；**永远不返回凭据明文**。
  - 工具参数只接受台账主键和标签，不接受原始 IP 或主机名；台账里没有的目标直接拒绝。
- **agent 指南内嵌在二进制里**：
  - `infrafile agent install` 把与当前版本匹配的指南写进 Claude Code、Codex 等工具的配置目录。
  - `infrafile agent guide` 直接打印指南。

## 受控写

- **`plan` 和 `apply` 分开**：`plan` 生成一份可审查的计划文件，`apply` 只执行审过的这份计划。
- 计划里记录生成时的观测状态；`apply` 前回读一次，状态变了就拒绝执行，避免审完到执行之间云端发生变化（TOCTOU）。
- **CDN** 的写操作通过插件的 control 能力执行，执行后回读确认。
- **DNS** 不直接写，只生成 DNSControl 配置，交给 DNSControl 去 preview / push。

## 安全默认值

以下规则要做成校验规则和默认行为，不能只写在文档里：
- 明文凭据不进台账。
- 下线的记录归档，不删除。
- 每次修改后都要能通过校验。
- 对外域名不直接暴露源站 IP（v0.2 审计规则）。

## 版本规划

| 版本 | 范围 |
|---|---|
| **v0.1** | 账本核心（kind / schema / JSON Schema、外键校验、归档、到期检查、`show` 与关系图、`ssh-config`、`init` 脚手架）；凭据（内置 sops + age、外部引用后端、`run --secret`）；agent 接口（`--json`、只读 MCP、内嵌指南）；`sync` 写入 `state/`；上述插件与导入器；plan / apply 受控写 |
| **v0.2** | 审计与 drift 检测（孤儿记录、僵尸记录、CDN 配置漂移、源站暴露等）；waivers 偏差豁免；生成 Gatus 监控配置（单向渲染） |
| **v0.1 之后** | 内置 Web UI `infrafile serve`，读写同一个 git 仓库 |

### waivers 偏差豁免（v0.2）

豁免写在记录上，删除记录时豁免一并消失，不会留下孤儿：

```yaml
metadata:
  waivers:
    - rule: origin_exposure
      reason: 内网管理面板，只对办公网放行   # 必填
      until: 2027-01-01                     # 可选；过期后失效，重新报警
```

### `serve` 的既定要求

- **单用户**，可以跨设备登录。第一版不做协作者和多角色。
- 编辑台账时先看 diff、跑校验，保存后形成 git 提交。
- 基于过期版本提交时，**停止保存并提示**，不自动合并。
- **保存台账不等于线上已经变更**：线上变更只走 plan / apply，并展示回读结果。
