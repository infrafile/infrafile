# `audit --json`

`infrafile audit [目录] [--rule <id>]... [--max-age 7d] --json` 先完整校验台账，再离线读取已启用插件实例的快照和 `state/_sync.yaml`。不访问网络、不解析凭据、不写文件。

当前实现 `cdn_drift`、`origin_exposure`、`orphan`、`zombie` 和 `stale_state`。未指定 `--rule` 时运行全部已就绪规则：前四条默认运行，`stale_state` 仅在提供 `--max-age` 时运行。显式指定 `stale_state` 必须同时提供 `--max-age`；未知规则退出 2。重复规则 ID 只运行一次。`ok: true` 只表示没有未豁免发现，不代表资产或快照完整、健康。

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

快照没有同步记录时，observed_at 和 age_days 均为 null；只要运行 `stale_state` 就报告。`state/` 不存在、或启用实例尚无快照时，不为缺失的快照生成发现，输出中也没有对应 snapshot。当前不检测资产缺失。`_sync.yaml` 只记录最近一次成功同步时间。时效规则不屏蔽生命周期发现，旧快照仍参与比对，应先刷新观测再决定修复。

v0.3 开发版额外离线读取 `state/.sync-status.yaml`。启用实例最近一次失败输出 sync_failed，running 输出 sync_incomplete；回执缺失或配置哈希、成功时间、快照字节不对应时输出 sync_status_unverified。首次失败即使没有快照也会提示。无回执文件的旧仓保持原行为；未启用实例的回执不参与提示。回执损坏或不是普通文件时退出 2，不回显原文。

这些是 notices，不是审计规则，不支持 waiver、不改变 ok 或退出码，也不屏蔽历史快照的既有发现。自动化必须检查本次 sync 的退出码；不能把 audit 的 ok=true 当作采集成功。回执匹配不代表清单完整或凭据拥有全量权限，missing 尚未实现。

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

生命周期只说明快照中仍存在该资产，不推断进程运行状态、云端实时状态、所有权或最新同步是否成功。`missing` 尚未实现。

## 源站暴露

`origin_exposure` 检查在用 Domain 中显式绑定在用 Server 的主机名（根域名写 `@`）。A/AAAA 地址与 Server.spec.address 规范化后相等时报告：台账 DNS 记录没有 `proxied: true`、Cloudflare 快照明确为 `proxied: false`，或 DNS 探测直接解析到该地址。IPv4 映射 IPv6 会先归一化；私有、回环、链路本地地址跳过。Resource 绑定优先跳过；仅 App 绑定、Server 地址为主机名、CNAME 链均不展开，不访问网络。

target 为 `Domain/name`，file 为域名记录。evidence 包含 `hostname`、`server`、`address`、`record_type`、`source`（ledger/cloudflare/dns）、`source_file`、`observed_at`；快照证据另含 `plugin`、`instance`，明确声明代理状态时包含 `proxied`。台账证据的 observed_at 为 null。不同来源保留各自发现，同一来源重复地址去重。Cloudflare 未声明代理状态时不推断为直连。

豁免使用该 Domain 的 `metadata.waivers`；不接受实例级 audit.ignore。离线结果只覆盖已声明地址和已有证据，不能证明源站地址从未通过历史 DNS、证书或其他渠道泄漏。

## CDN 停用漂移

`cdn_drift` 只检查在用、`spec.type: cdn` 的 Resource。与生命周期规则使用同一自然身份：观测有 ID 时按 provider/resource_id，否则按 provider/type/endpoint；不要求台账主键与导入生成名相同。归档资源、其他 provider/type、未启用实例或无 CDN 观测时不判断。台账中的「在用」是指未归档，不能由 notes 推断停用意图。

下表于 2026-10-28 按官方 API 文档核实；仅明确停用或删除的终态产生发现，不将审核失败、配置失败或过渡状态推断为停用：

| 插件 | 报告漂移的原始 status | 已知但不报告的 status | 来源 |
| --- | --- | --- | --- |
| aliyun | `offline` | online、configuring、configure_failed、checking、check_failed、stopping、deleting | [DescribeUserDomains](https://www.alibabacloud.com/help/en/cdn/developer-reference/api-cdn-2018-05-10-describeuserdomains) |
| tencent | `offline` | online、rejected、processing、closing | [BriefDomain](https://cloud.tencent.com/document/api/228/30987#BriefDomain) |
| ctyun | `6`（已停止）、`8`（已删除） | 1–5、7、9–12 | [查询域名列表](https://www.ctyun.cn/document/10015932/10035101) |

天翼云插件已把 API 的整数状态转成快照字符串；审计不另作数字类型转换。百度云快照没有状态字段，本版不判断；Cloudflare zone 不属于此规则。未知或缺失的 status 不产生发现，输出 `cdn_status_unknown` 提示并原样保留状态文本；不据此认定 CDN 正常。业务字段损坏或 status 类型错误时退出 2。

target 为 `Resource/name`，file 为资源记录；evidence 包含 `plugin`、`instance`、`snapshot_file`、`observed_at`、`identity`、原始 `status`，观测提供 area 时也保留。不同实例/区域保留各自证据，同一实例相同身份、状态、区域去重。豁免写在 Resource 的 `metadata.waivers`，不支持实例级 audit.ignore。本版不比较回源配置，不推断快照没有记录的资源已删除。

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
| sync_failed | 最近一次执行失败；旧快照仅是历史观测 |
| sync_incomplete | 最近执行没有结束回执，仍是 running |
| sync_status_unverified | 当前配置、成功时间、快照与回执不对应，或缺少该实例回执 |

每条提示另有 `file`、`target`、`message`；豁免提示还有 `rule`。提示本身不影响退出码；未豁免发现退出 1，其余可完成的审计退出 0。

MCP 的 `infrafile_audit` 复用同一结果对象，以 `rules` 数组和 `max_age_days` 整数替代 CLI 选项。偏差返回 `ok: false`、`isError: false`；参数或读取失败使用 [MCP 错误信封](mcp.md)。
