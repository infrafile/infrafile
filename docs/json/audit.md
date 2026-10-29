# `audit --json`

`infrafile audit [目录] [--rule <id>]... [--max-age 7d] --json` 先完整校验台账，再离线读取已启用插件实例的快照和 `state/_sync.yaml`。不访问网络、不解析凭据、不写文件。

当前实现 `cdn_drift`、`origin_exposure`、`orphan`、`zombie`、`stale_state` 和 `missing`。未指定 `--rule` 时运行全部已就绪规则：前四条默认运行，`stale_state` 和 `missing` 仅在提供 `--max-age` 时运行。显式指定这两条规则必须同时提供 `--max-age`；未知规则退出 2。重复规则 ID 只运行一次。`ok: true` 只表示没有未豁免发现，不代表资产或快照完整、健康。

| 字段 | 含义 |
| --- | --- |
| version | 固定 `"v1"` |
| ok | 没有未豁免发现时为 true |
| as_of | 本次检查时刻，UTC RFC3339 |
| max_age_days | 非负整数天数，未指定为 null；CLI 写作 `7d` |
| rules | 实际运行的规则 ID，按 ID 排序 |
| snapshots | 已启用实例的快照，按插件、实例排序 |
| findings | 发现，按 rule、target 排序；包含已豁免发现 |
| notices | 不影响 ok 或退出码的提示 |

数组没有条目时输出 `[]`。`snapshots[]` 包含 `plugin`、`instance`、仓内相对路径 `file`、`observed_at`（UTC RFC3339 或 null）、`age_days`（观测至检查时刻的整天数，向零取整，或 null）。未来观测时间可能产生负数，不报过旧。

`findings[]` 包含 `rule`、`target`、`file`、`message`、`evidence`、`waiver`。`stale_state` 的 target 是 `plugin/instance`，evidence 包含 `observed_at`、`age_days`、`max_age_days`。判断使用实际经过时间，严格超过阈值才报告，恰好等于阈值不报；显示的整天数不用于舍弃不足一天的超时。

v1.0 开发分支兼容增加 `issues[]`，为各规则/方面提供 v1 指纹、适用范围摘要及结构化 key；原 finding 数量和以上字段不变。回源多方面可对应多个问题身份；消息/时间/观测值不入指纹。详见[问题身份与恢复规则](issues.md)。audit 查询不推进历史，unknown、豁免或范围退出不表示恢复。

例如以下发现未被豁免，导致 `ok: false`、退出 1：

```json
{
  "rule": "stale_state",
  "target": "cloudflare/home",
  "file": "state/cloudflare/home.yaml",
  "message": "观测已 91 天未更新（上限 45 天）",
  "evidence": {
    "age_days": 91,
    "max_age_days": 45,
    "observed_at": "2026-09-01T03:00:00Z"
  },
  "waiver": null
}
```

快照没有同步记录时，observed_at 和 age_days 均为 null；只要运行 `stale_state` 就报告。`state/` 不存在、或启用实例尚无快照时，不为缺失的快照生成发现，输出中也没有对应 snapshot。missing 对已声明但无有效证据的目标给出提示，见下文。`_sync.yaml` 只记录最近一次成功同步时间。时效规则不屏蔽生命周期发现，旧快照仍参与比对，应先刷新观测再决定修复。

v0.3额外离线读取 `state/.sync-status.yaml`。启用实例最近一次失败输出 sync_failed，running 输出 sync_incomplete；回执缺失或配置哈希、成功时间、快照字节不对应时输出 sync_status_unverified。首次失败即使没有快照也会提示。无回执文件的旧仓保持原行为；未启用实例的回执不参与提示。回执损坏或不是普通文件时退出 2，不回显原文。

这些是 notices，不是审计规则，不支持 waiver、不改变 ok 或退出码，也不屏蔽历史快照的既有发现。自动化必须检查本次 sync 的退出码；不能把 audit 的 ok=true 当作采集成功。回执匹配不代表清单完整或凭据拥有全量权限。

## 生命周期与自然身份

`orphan` 表示观测到的资产在台账中没有对应记录（包括归档记录）；target 为 `plugin/instance/` 加自然身份的文字表示，file 为快照路径。提示用 `import state` 生成待审草稿；导入仍按主键只建不覆盖，审计才按自然身份比对。

`zombie` 表示观测只匹配到归档记录；target 为 `Kind/name`，file 为该记录路径。若同一身份存在任意在用记录（含 Cloudflare zone 的同名 Domain 覆盖），不报 zombie，避免旧别名或复用地址误报。多个归档记录可以分别产生发现；不同实例的观测保留各自证据。同实例重复观测按规范化身份去重。

两条规则共用导入的资产映射：阿里云/腾讯云/百度云 domains、四家云 cdn、Cloudflare zones、WHOIS found=true、SSH host。天翼 domains 仍不支持；DNS/TLS、未知插件不参与生命周期。所选规则声明所需插件/服务，仅解析相应实例的业务字段；业务字段损坏时退出 2，不返回部分结果。只运行 stale_state 时不要求业务字段能映射成资产。

生命周期 evidence 包含 `plugin`、`instance`、`snapshot_file`、`observed_at`（未知为 null）、`identity`。identity 也是精确豁免的 match 格式：

| 资产 | identity / match | 规范化与匹配 |
| --- | --- | --- |
| Domain | `{kind: "Domain", name}` | 域名小写、去尾点 |
| Server | `{kind: "Server", address}` | IP 用 net/netip 规范化并去 IPv4 映射；主机名小写、去尾点；不查 DNS、不将主机名解析为 IP |
| Resource，有观测 ID | `{kind: "Resource", provider, resource_id}` | provider 和 ID 精确相等；不因 endpoint 相同而忽略 ID 差异 |
| Resource，无观测 ID | `{kind: "Resource", provider, type, endpoint}` | provider/type 精确相等，域名 endpoint 小写、去尾点；台账有 ID 仍可被这种观测按 endpoint 覆盖 |

Cloudflare zone 可匹配 dns-zone Resource 或同名 Domain，均包含归档记录；DNS/CDN 不因此被当作注册域名。Cloudflare 发现的 identity 仍是 Resource，条目级 match 应直接复制该 identity，不能换成覆盖它的 Domain 身份。

orphan / zombie 只说明快照中仍存在该资产，不推断进程运行状态、云端实时状态、所有权或最新同步是否成功。missing 的负向判断另有以下前提。

## missing：指定查询范围内不再可见

v0.3只检查 `infrafile.yaml` 的 `audit.watch`，不从 provider、account、control 或标签猜测检查范围。例如：

```yaml
audit:
  watch:
    - target: Domain/example.com
      plugin: aliyun
      instance: home
      service: domains
    - target: Resource/edge
      plugin: cloudflare
      instance: home
      service: zones
```

每个 target 只允许一条绑定，必须存在且未归档；实例须启用，服务须受支持且在当前 services 配置中启用（省略时沿用插件默认服务）。未启用、重复、归档目标、类型或 provider 不匹配均在台账校验时报错。归档或移除目标时同时移除 watch。

| 插件 / service | 允许的目标 |
| --- | --- |
| aliyun / tencent / baidu 的 domains | Domain |
| aliyun / tencent / baidu / ctyun 的 cdn | 同 provider、type 为 cdn 的 Resource |
| cloudflare 的 zones | 同名 Domain 或 provider 为 cloudflare、type 为 dns-zone 的 Resource |

Resource 至少有 resource_id 或 endpoint。匹配沿用自然身份：观测有 ID 时必须匹配 ID，不能用同名 endpoint 覆盖 ID 差异；无观测 ID 才按 endpoint 匹配。SSH、DNS、TLS、WHOIS 和外部插件不支持 missing。

sync 在 `.presence.yaml` 为真正出现过的目标建立正向基线。首次空清单不会建立基线，新录入记录也不会因旧快照中没有它而误报。目标自然身份（含 endpoint 后备身份）、watch 绑定或实例配置改变后必须重新出现过；备注和豁免不影响基线。sync 会清除移除或改变的绑定，失败采集不能建立新基线，空清单不推进 last_seen。只读 audit 不保存或迁移基线。

missing 必须提供非负 `--max-age`；仅当对应实例的成功回执与配置、快照字节、成功时间完全匹配，观测未过期且不在未来，声明服务返回完整集合，并有相同绑定且不晚于观测的正向基线时，才作缺失判断。恰好到阈值仍有效，超过一秒也算过期；`0d` 只接受与检查时刻相同的观测。

不满足前提时，输出 `missing_unknown` notice，包含 rule、target、记录 file 和不能判断的原因；不产生 missing finding，不改变 ok / 退出码，也不能通过 waiver 隐藏。损坏或符号链接基线、损坏的服务集合仍退出 2。无 watch 时这条规则不作资产判断。

finding 的 target / file 属于声明的台账记录；evidence 包含 `identity`（目标自然身份）、`plugin`、`instance`、`service`、`snapshot_file`、`observed_at`、`last_seen`、`max_age_days`。消息要求核对权限和查询范围：未找到只表示在该凭据本轮查询范围内不可见，不能断言已删除。凭据引用背后的令牌或权限变化不会自动改变配置指纹；若 API 静默缩小可见范围，工具无法将其与删除区分。

使用目标记录的 `metadata.waivers: [{rule: missing, reason: ...}]` 精确豁免；可附 until，过期后重新报告。不支持 missing 的 `audit.ignore`，同名或同身份的其他记录豁免不影响该目标。CLI 与 MCP 使用同一判断和证据。

## 源站暴露

`origin_exposure` 检查在用 Domain 中显式绑定在用 Server 的主机名（根域名写 `@`）。A/AAAA 地址与 Server.spec.address 规范化后相等时报告：台账 DNS 记录没有 `proxied: true`、Cloudflare 快照明确为 `proxied: false`，或 DNS 探测直接解析到该地址。IPv4 映射 IPv6 会先归一化；私有、回环、链路本地地址跳过。Resource 绑定优先跳过；显式 Server 绑定优先于 App。仅 App 绑定时展开该在用 App 的全部在用 Server 部署，共享部署及同来源重复证据去重，证据增加 `app` 主键。无在用部署或部署地址不是明确 IP 时提示 `origin_exposure_unknown`；部分部署可判断时同时保留发现和提示。归档 Domain/App/Server 不参与。Server 地址为主机名、CNAME 链均不展开，不访问网络。

target 为 `Domain/name`，file 为域名记录。evidence 包含 `hostname`、`server`、`address`、`record_type`、`source`（ledger/cloudflare/dns）、`source_file`、`observed_at`；快照证据另含 `plugin`、`instance`，明确声明代理状态时包含 `proxied`。台账证据的 observed_at 为 null。不同来源保留各自发现，同一来源重复地址去重。Cloudflare 未声明代理状态时不推断为直连。

豁免使用该 Domain 的 `metadata.waivers`；不接受实例级 audit.ignore。离线结果只覆盖已声明地址和已有证据，不能证明源站地址从未通过历史 DNS、证书或其他渠道泄漏。

## CDN 停用漂移

`cdn_drift` 的停用部分只检查在用、`spec.type: cdn` 的 Resource。与生命周期规则使用同一自然身份：观测有 ID 时按 provider/resource_id，否则按 provider/type/endpoint；不要求台账主键与导入生成名相同。归档资源、其他 provider/type、未启用实例或无 CDN 观测时不判断。台账中的「在用」是指未归档，不能由 notes 推断停用意图。

下表于 2026-10-28 按官方 API 文档核实；仅明确停用或删除的终态产生发现，不将审核失败、配置失败或过渡状态推断为停用：

| 插件 | 报告漂移的原始 status | 已知但不报告的 status | 来源 |
| --- | --- | --- | --- |
| aliyun | `offline` | online、configuring、configure_failed、checking、check_failed、stopping、deleting | [DescribeUserDomains](https://www.alibabacloud.com/help/en/cdn/developer-reference/api-cdn-2018-05-10-describeuserdomains) |
| tencent | `offline` | online、rejected、processing、closing | [BriefDomain](https://cloud.tencent.com/document/api/228/30987#BriefDomain) |
| ctyun | `6`（已停止）、`8`（已删除） | 1–5、7、9–12 | [查询域名列表](https://www.ctyun.cn/document/10015932/10035101) |

天翼云插件已把 API 的整数状态转成快照字符串；审计不另作数字类型转换。百度云快照没有状态字段，本版不判断；Cloudflare zone 不参与停用判断。未知或缺失的 status 不产生发现，输出 `cdn_status_unknown` 提示并原样保留状态文本；不据此认定 CDN 正常。业务字段损坏或 status 类型错误时退出 2。

target 为 `Resource/name`，file 为资源记录；evidence 包含 `aspect: status`、`plugin`、`instance`、`snapshot_file`、`observed_at`、`identity`、原始 `status`，观测提供 area 时也保留。不同实例/区域保留各自证据，同一实例相同身份、状态、区域去重。豁免写在 Resource 的 `metadata.waivers`，不支持实例级 audit.ignore。不推断快照没有记录的资源已删除。停用检查保留 v0.2 的历史快照行为，不受下面新增回源判断的时效门槛影响。

## CDN 回源漂移（v0.3）

在 CDN Resource 或 Cloudflare dns-zone Resource 中声明期望，例如 `spec.origin: {hosts: [origin.example.com, 192.0.2.1], port: 8443, protocol: https}`。hosts 必填、非空，只接受 DNS 名称或 IP；拒绝 URL、端口后缀、通配符和 IPv6 zone ID。主机名小写、去尾点，IP 规范化并去 IPv4 映射；比较排序去重后的集合，不查 DNS。可选 port/protocol 表示所有配置源站的共同期望，省略时不检查；protocol 只支持 http/https/follow。不检查主备优先级、权重或运行时可达性。`import state` 不把观测回源写成期望。

必须用 `audit.watch` 显式绑定实例和 cdn/zones 服务，开启实例 `collect_origin: true`，并传非负 `--max-age`。只有成功回执与配置、快照字节及时间对应，且观测不在未来、不超过阈值时才判断。回源比较不需要 missing 的正向基线；改变 spec.origin 不要求重新同步。同一自然身份有多个区域/产品条目时不能合并成单一配置。

采集方输出 `origin: {scope, sources: [{host, port?, protocol?}]}`。scope 为 global 表示该详情中的无条件配置，conditional 表示条件/区域/规则配置，unknown 表示不支持或字段不足；这里的 global 不等于 CDN 覆盖全球。仅 global 且非空有效主机集合参与比较。Cloudflare 单条无条件 Origin Rule 的 origin.host/port 可比较，未返回传输协议时即为未知；Host header、SNI 和 DNS 默认源站不会被替代使用。

差异生成同一个 cdn_drift finding，evidence 包含 `aspect: origin`、`identity`、`plugin`、`instance`、`service`、`snapshot_file`、`observed_at`、`max_age_days`、规范化的 `expected`/`observed` 和字段名数组 `differences`。Resource 上的 cdn_drift waiver 同时覆盖停用与回源发现。

绑定缺失、采集未开启、没有 max-age、失败/不匹配/过期证据、未找到唯一条目、复杂作用范围或无法观测用户要求的字段，均产生 `cdn_origin_unknown` notice。未知字段不推断默认值；已有可确定的字段差异仍可单独报告。未知不改变 ok/退出码，不能用 waiver 隐藏，因此 ok=true 不能证明回源符合期望。云 API、权限及支持边界见[回源接口核对](../research/v0.3-cdn-origins.md)。

未启用实例的快照不读取内容，只输出 `instance_disabled` 提示；路径仍须合法。已启用快照格式、信封或敏感检查失败，`_sync.yaml` 损坏、目录不可读或存在 `.sync.lock` 时退出 2。台账校验失败同样退出 2。退出 2 时 stdout 为空，stderr 是人读的错误说明。

## 豁免

`waiver` 为 null 或 `{reason, until?, source}`；source 是豁免所在文件的相对路径。有效豁免保留发现，只把它从退出码计算中排除。所有发现均被豁免时 `ok: true`、退出 0。字段名为 `waiver`，不输出 `waived`。

`metadata.waivers` 按记录的 `Kind/name` 精确匹配；归档记录的 zombie 豁免也生效。`stale_state` 面向插件实例，使用兼容 M2 的 `infrafile.yaml` 配置：

```yaml
audit:
  ignore:
    - rule: stale_state
      plugin: cloudflare
      instance: home
      reason: 每季度手动同步一次
      until: 2026-12-31
```

`stale_state` 的 ignore 仍按整个实例匹配，禁止 match。`orphan` / `zombie` 的 ignore 必须额外带 match，只允许上表四种完整身份之一；缺字段、混用 ID 与 endpoint、空值和未知字段均被拒绝。不支持通配符、前缀或子串匹配。规则、插件、实例和规范化后的 identity 必须全部相等，才能抑制该发现；同实例其他条目保持未豁免。

```yaml
audit:
  ignore:
    - rule: orphan
      plugin: aliyun
      instance: home
      match: {kind: Domain, name: ignored.example.net}
      reason: 由另一个团队维护
      until: 2026-12-31
```

`until` 当天仍有效，按检查时刻所在时区的日历日判定；到下一天失效。只检查本次实际运行规则的豁免；归档记录仅参与 zombie 豁免。过期与未命中提示独立于发现输出：

| notices[].code | 含义 |
| --- | --- |
| instance_disabled | 实例未启用，忽略对应快照；无 rule 字段 |
| waiver_expired | 豁免已过期，不再抑制发现 |
| waiver_unused | 有效豁免没有抑制任何发现，可删除 |
| cdn_status_unknown | CDN 状态未纳入官方映射，未判断该条目的停用漂移 |
| cdn_origin_unknown | 回源的范围、字段或新鲜成功证据不足，不能判断相关期望 |
| sync_failed | 最近一次执行失败；旧快照仅是历史观测 |
| sync_incomplete | 最近执行没有结束回执，仍是 running |
| sync_status_unverified | 当前配置、成功时间、快照与回执不对应，或缺少该实例回执 |

每条提示另有 `file`、`target`、`message`；豁免提示还有 `rule`。提示本身不影响退出码；未豁免发现退出 1，其余可完成的审计退出 0。

MCP 的 `infrafile_audit` 复用同一结果对象，以 `rules` 数组和 `max_age_days` 整数替代 CLI 选项。偏差返回 `ok: false`、`isError: false`；参数或读取失败使用 [MCP 错误信封](mcp.md)。


存在显式保存的问题历史时，兼容增加可选 `history`，记录最近一次显式检查时间、输入摘要、保存状态和变化游标。缺历史时省略；查询不推进历史，损坏则拒绝。保存状态与本次观测结果分开，详情见 [findings.md](findings.md)。
