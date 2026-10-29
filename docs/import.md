# 离线导入与记录草稿

共享 YAML 解析器拒绝循环别名、超过 100 层的嵌套、单文件累计展开超过 100,000 个节点，或展开后的字段路径及标量累计超过 16 MiB。正常锚点复用仍受支持；别名展开计入上述上限，超限不生成部分草稿。台账加载和 state 导入的解析过程响应取消信号。

`infrafile import` 从已有 SSH 配置或 `state/` 快照生成资产记录。它不连接服务器、不请求云 API、不解析凭据值、不运行 `ssh`。两种导入均先校验目标数据仓；请先用 `infrafile init` 建立台账。

```sh
# 默认读取当前用户的 ~/.ssh/config，草稿只输出到 stdout
infrafile import ssh-config --dir /path/to/my-infra
# 显式指定输入；相对路径以命令工作目录为基准
infrafile import ssh-config --file ./ssh.conf --dir /path/to/my-infra --json
# 读取目标数据仓的 state/，生成待审文件
infrafile import state --dir /path/to/my-infra --output ./review
# 显式写入台账，只创建不存在的记录
infrafile import state --dir /path/to/my-infra --write --json
```

`--output`（`-o`）与 `--write` 互斥。不带这两个选项时不会创建任何文件，文本模式输出以 `---` 分隔的 YAML 文档，JSON 模式输出结构化草稿。一个 inventory 文件只能有一条记录，不要把整段多文档输出重定向为单条记录文件。

待审目录按 `<kind 复数>/<name>.yaml` 布局，已有文件也跳过。待审目录不能位于 `inventory/` 内，符号链接及大小写别名不能绕过这个限制；台账根目录下的 inventory 大小写变体也保留不用。重新执行 `--write` 会重新读取原始来源，**不会读取手工修改过的 review 草稿**；修改后的单条草稿应由人移入对应 inventory 目录，再运行 `validate`。

## 只建不覆盖

以下为原有 stdout / `--output` / `--write` 模式，其退出码和跳过行为保持不变。可保存并反复校验手改候选的新流程见下一节。

已有记录按 `Kind/metadata.name` 判断，包括归档记录；内容不同也直接跳过，不合并字段、不刷新到期日、不恢复归档。同一个地址但名字不同，可以生成不同草稿；导入不会猜测两个别名是不是同一台机器。

所有候选先通过记录 Schema 和敏感数据检测，然后才输出或写入。不创建账号、不填未知字段、不推断账号或部署外键。多个来源生成完全相同的新记录时去重；若同一主键内容不同，整次预检失败，不输出草稿、不开始写入。先人工确认并建立该记录后，后续导入会按已有主键跳过。

写入时单个文件先完整生成，再以不替换已有名字的方式发布；即使另一个进程抢先创建目标，也只报告已存在。已有文件、注释和修改时间保持不变。目标文件系统需要支持硬链接；不支持时明确报错。多文件写入不是事务，I/O 中途失败时保留此前成功创建的记录，重试仍只创建缺失项。导入不修改来源文件、`infrafile.yaml` 或同步时间。

退出码为 0 成功（包括全部已存在、无记录或明确跳过的观测），2 用法、源数据、冲突或写入错误。退出 2 时 stdout 为空，错误在 stderr；若写入中途失败，stderr 会明确说明可能已有部分记录创建。JSON 契约见 [import.md](json/import.md)。

## 可重复草稿与审阅（v1.0 开发接口）

当前 v0.3 发行包尚不包含本节接口；源码构建可用。需要保存人工修改并核对来源时，使用独立的版本化草稿文件：

```sh
infrafile import state --dir /path/to/my-infra --draft ./review/draft.json --json
# 编辑 draft.json 中的 record，或用 selected: false 排除单个候选
infrafile import review ./review/draft.json --dir /path/to/my-infra --json

# SSH 的生成和审阅都须由操作者显式选择来源文件
infrafile import ssh-config --file ./ssh.conf --dir /path/to/my-infra --draft ./review/ssh.json --json
infrafile import review ./review/ssh.json --file ./ssh.conf --dir /path/to/my-infra --json
```

`--draft` 与 `--output` / `--write` 互斥。只创建一个完整文件，不替换已有审阅；请为重新生成的版本另取文件名。文件默认权限 0600，不能放入 inventory/state，路径大小写和符号链接别名也不能绕过该限制。审阅不写台账、来源、状态或草稿，不联网、调用插件或解析凭据。

每份草稿保存来源类型、实际读取文件的 SHA-256、配置与 inventory 的语义基准，以及候选的稳定来源/自然身份。SSH Include 的真实文件字节参与哈希，重复 Include 仍计入原有累计预算；state 使用实际快照字节，不改同步时间。来源的增删、注释变化或 Include glob 新匹配都能使审阅返回 source-changed，必须重新生成。配置/记录语义变化返回 repository-changed；此基准不绑定 Git HEAD、注释或文件权限，也不是写锁或批准证明。T13 发布时仍须绑定当前完整内容/HEAD 并重新校验。

来源清单只用于对照，不用于选择读取路径。state 固定读取显式数据仓；SSH 审阅必须提供 `--file`，不会按草稿内的文件清单打开任意路径。操作者选定的 SSH 配置及其 Include 仍须可信；此流程不隔离同一 OS 用户。

编辑 `record` 可以补日期、账号、标签、关系或改名；来源、identity、id 不可手改，排除候选用 selected: false，不删除候选。完整候选批次与现有记录共同执行原有 Schema、外键、归档引用、watch 和明文凭据校验，允许同批新记录互相引用。元数据或字段不合法不会发布；敏感值及原始解析错误不出现在审阅报告。

报告区分 draft、exists、archived、duplicate、excluded、conflict、invalid。同主键的候选字段若已经包含在现有记录中，则为 exists，额外手写字段保留；字段值不同则冲突。自然身份撞到另一个主键时要求人工确认，不自动合并、重命名或恢复归档。Resource 的两个明确不同账号彼此区分；任一账号未知时，不把相同 provider/ID 或 endpoint 当作已证明同一范围。多个来源产生相同新主键且相同内容时去重，不同内容阻断；可以排除一个来源后重新审阅。

缺日期、resource_id、SSH user/port 保持缺省并列出 gaps，不补默认值；人工改变自然身份另列 identity-edited。gaps 不一定阻断，但 ok 不代表所有字段、迁移语义、所有权或新鲜度已验证。SSH 未映射配置、DNS/TLS 的观测限制与原映射保持一致。审阅只确认这次内存输入，源/仓库可在其后变化；不能把一次通过结果当作后续写入授权。

草稿最多 4 MiB、4096 个候选、1024 个来源文件，并受共享解析器的深度、展开节点/字节限制；生成与读取使用同一限制。读取拒绝符号链接、非普通文件、重复键、多文档、未知版本/字段、缺失字段和大小写变体。ImportDraft 是独立审阅产物，不是新管理历史，不触发 storageVersion 或自动升级。

新流程退出码：0 本次审阅通过；1 冲突、无效候选或版本基准已变化，带结构化报告；2 无法生成/读取/保存或参数错误，stdout 为空。`--draft` 遇业务冲突仍保存待修草稿并返回 1。**T05 仅提供预览与候选校验，尚无从本文件发布资产的入口**；后续 T13 接入共享变更事务，不能用重新运行原有 `--write` 来发布这份人工修改。

新 `--draft` / `review` 流程的 state 来源另限累计 64 MiB、1024 个快照文件，超限不保留部分候选；SSH 沿用下面的累计读取和求值预算。这些上限是输入与解析预算，不表示整个进程的堆内存上限。原有 state 导入的读取接口保持兼容。

## SSH config 支持范围

单次 SSH 导入最多累计读取 1,024 次文件、8 MiB 配置文本，重复 Include 也计入；所有 Host 共享 6,400 万次求值工作量上限（指令、参数及子配置引用均计数）。超限会报错，不写入草稿。解析和求值支持通过 Ctrl+C 或 SIGTERM 取消。

从显式 `Host` 中发现字面别名；支持一个 Host 的多个别名、`*` / `?` 默认匹配、`!` 排除、首个匹配值优先、引号、注释、等号分隔。只导入 `HostName`、`User`、`Port`，未指定 HostName 时保留原别名作为 address；未指定 user / port 时不补造本机用户名或默认端口。

Include 支持绝对路径、`~/`、相对路径和 glob；相对路径遵循用户 SSH 配置语义，以 `~/.ssh/` 为基准，即使主文件由 `--file` 指定也一样。文件按字典序读取，条件 Include 只在外层 Host 生效时应用，子文件不会改变外层匹配状态。循环或超过 16 层嵌套会报错。相关语义依据 [OpenSSH 配置手册](https://man.openbsd.org/ssh_config)及[配置解析源码](https://github.com/openssh/openssh-portable/blob/master/readconf.c)。

支持 HostName 的 `%h` 替换；其他 token、Include 的环境变量展开、Match 条件和需要 DNS 的地址规范化不支持，遇到时明确报错。不会执行 Match exec、ProxyCommand、LocalCommand、RemoteCommand，也不会隐式读取系统级 SSH 配置。IdentityFile、代理、转发及其他未映射的 SSH 配置不会导入；需要连接时仍应保留原 SSH 配置并人工核对。

别名转为小写，记录名不允许的字符转为连字符，去掉首尾点和连字符；空名、超长名或规范化后产生不同草稿的主键冲突会报错。完整文件名（含 `.yaml`）最多 255 字节，因此导入的 SSH / Domain 主键最多 250 字节；自动生成的 Resource 名会为后缀预留空间。导入只生成资产字段，不承诺完整重建 SSH 客户端配置。

## state 映射

观测到资产的字段与主键生成由 `internal/observation` 统一映射，`import state` 与生命周期审计共用。导入仍按主键只建不覆盖，审计另外按自然身份比对，不改变这里的生成与跳过行为。

读取 `state/<插件>/<实例>.yaml`；不要求插件当前仍启用，也不会自动同步。`_sync.yaml` 不是资产来源。没有 state 时返回空结果；存在 `.sync.lock` 时拒绝读取，避免在同步过程中导入。快照必须是普通文件、信封有效、实例名与文件名一致；重复 YAML 键、多文档、敏感字段或错误类型会被拒绝。

| 来源 | 草稿 | 保留字段 |
|---|---|---|
| 阿里云、腾讯云、百度云 `domains` | Domain，名字为规范化域名 | registrar 为供应商标识、expires_at（若有） |
| WHOIS `found: true` | Domain | 域名、公开的 registrar 和 expires_at |
| 四家云 `cdn` | Resource，type 为 `cdn` | provider、域名 endpoint、resource_id（若有） |
| Cloudflare `zones` | Resource，type 为 `dns-zone` | provider、zone 名 endpoint、zone ID |
| SSH 实测 | Server，名字为实例名 | host → address；system + release → hardware.os |
| DNS、TLS | 不生成资产 | 报告 `observation-only` |
| WHOIS 无匹配 | 不生成资产 | 报告 `not-found` |
| 未支持的插件（含虚构 example） | 不生成资产 | 报告 `unsupported-plugin` |

天翼注册域名仍不支持；出现该字段会报错。DNS zone 和 CDN 域名不会被视为注册域名。域名转小写并去掉尾点，国际域名需使用 ASCII punycode。Resource 名字由供应商、实例、类型、域名和稳定的短哈希组成，以区分实例并满足名称长度；同一来源再次导入保持相同主键。

阿里云 CDN 可能用 `.example.com` 表示泛域名，导入与自然身份匹配统一为 `*.example.com`，不会合并成根域名；原始 state 保留供应商写法。只对阿里云的 CDN 资源应用此兼容，不允许注册域名或其他提供方借此绕过名称校验。格式说明见[阿里云泛域名文档](https://www.alibabacloud.com/help/zh/cdn/does-alibaba-cloud-cdn-support-wildcard-domain-names)。

观测属于采集当时的事实，导入不推断其新鲜度或所有权，也不把 DNS 地址自动连成 Server 外键。Cloudflare DNS 记录、CDN 状态及 SSH 架构等未映射字段保留在 state，草稿不复制整份原始快照。

虚构演示源已放在 [homelab](../examples/homelab/README.md)，自动化测试覆盖五家云回放 → 同步 → 导入 → 校验，以及重复导入、归档保护、冲突和路径边界。
