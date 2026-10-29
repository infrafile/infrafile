# inspect JSON 契约

v1.0 开发接口：`infrafile inspect [目录] --max-age 7d --json`。只读离线聚合，先校验整个台账；不调用采集器、不联网、不解析凭据、不刷新历史。命令退出 0 表示报告生成成功，`ok=false`、风险和 unknown 也返回 0；参数错误或无法读取所需输入退出 2，stdout 为空。不使用退出码 1。旧 `audit` 的 JSON、规则和退出码不变。

可选 `--key Kind/name`、`--kind Kind`、`--label env=prod,team=example`；key 与 label 互斥。标签精确匹配且全部满足。不存在的 key 拒绝；标签/类型未匹配时返回空选择，`ok=false`。不接受任意地址、URL 或命令作为检查目标。

结果顶层为 `{version, ok, as_of, max_age_days, assets, relations, execution, coverage, risk, notices}`。`as_of` 为 UTC RFC3339，未指定 TTL 时 `max_age_days=null`，不证明新鲜覆盖。天数不乘入 Duration，支持 Go int 范围和精确边界。

| 字段 | 含义 |
| --- | --- |
| assets | 选定记录的 `{key, file, archived}`；不返回原始 spec 或凭据引用 |
| relations | 与选定记录直接相连的 `{from, to, path}`；相关记录不自动扩大健康检查范围 |
| execution | 范围内配置实例的最近执行；包含 plugin / instance、status、code、attempted_at、observed_at、snapshot_file、receipt_file |
| coverage | 固定应检项的 expected / checked / unknown / not_applicable 数量和 checks；四种数量满足 expected = checked + unknown + not_applicable |
| risk | confirmed（当前证据确认的未豁免风险）、unknown（历史或资格不足的发现，含已豁免）、waived（已豁免发现）及 findings |
| notices | 复用 audit 提示及 na_expired / na_unused；unknown 不等于通过 |

`execution.status` 为 succeeded / failed / running / unknown。succeeded 表示回执与当前配置、成功时间和快照哈希匹配；code 还会标记 stale 或 ttl_unset。缺回执、配置/内容/时间不匹配、未来时间都不证明本轮成功。failed 保留已有快照为历史证据。没有快照的已配置实例仍在报告中。

`ok=true` 要求非空资产范围、有实际已检项、覆盖无 unknown、没有未豁免确认风险或历史/未知发现，且范围内实例都有完整新鲜的成功回执。全部 NA、没有 findings、只有旧快照或没有配置不能得到全通过。这里只证明所列检查的证据资格，不证明应用交易或完整运维健康。

## 分母与范围

检查项由台账、显式管理声明和下列固定契约生成，不能以快照返回条目数为分母。默认选择全部记录，并对每个已配置实例加入 freshness；资产清单插件的声明服务另有 orphan / zombie 集合检查。`services` 的显式选择保留，未声明时使用该插件已有服务契约。未知插件显示 unsupported；DNS/TLS 不冒充资产清单。

选定 key / labels / kind 时只列选定资产检查及其直接使用的实例，不顺带审计所有账号；范围和所有应检项仍完整返回。历史 finding 若来自另一个账号/服务，不能借本账号成功变成当前确认。

| 资产 | 应检契约 |
| --- | --- |
| 在用 Domain | missing/presence 需明确 watch；每条绑定的 origin_exposure 需明确 Server 或 App 部署、可比较公网 IP，以及 hostname 对应 DNS 实例或明确绑定 Cloudflare 的地址/代理证据 |
| 在用 Resource | missing/presence 需明确 watch；未支持的资源类型为 unsupported。CDN / Cloudflare dns-zone 另有 cdn_drift/status 与回源检查；未实现的状态对照为 unsupported，回源需显式 origin 与 collect_origin，按期望分别检查 origin/hosts、origin/port、origin/protocol |
| 在用 Server | 与 address 明确匹配的 SSH 实例；uname 实测仅证明本项，不表示应用健康；未配置保留 unknown |
| 在用 App / Repo / Account | 没有内置健康检查契约，coverage/checks 显示 unsupported；关系可查，原运维流程继续负责 |
| 归档记录 | 对适用的已配置清单服务检查 zombie；缺适用范围保留 unknown，不复活归档记录 |

每个 `checks[]` 包含 `{id, rule, target, plugin, instance, service, aspect, status, code, message, evidence, not_applicable}`。`coverage` 是报告内部的检查契约名，不新增 `audit --rule`。空实例字段表示尚无明确绑定，不猜账号。绑定方面使用 hostname 与引用的主键，不使用数组位置或观测值，重复声明同一检查只计一次。

id 是按顺序序列化 `{rule,target,plugin,instance,service,aspect}` 的 SHA-256 小写十六进制。没有时间、消息、文件路径或观测值；不同账号/方面不合并。它是覆盖检查项的身份。T08 的 finding 指纹另见[问题身份与恢复规则](issues.md)，不与覆盖检查 id 混用。

`status` 为 checked / unknown / not_applicable；`code` 说明资格，例如 checked、failed、running、unverified、future、stale、ttl_unset、incomplete、not_baselined、baseline_future、not_configured、ambiguous_scope、missing_relationship、address_unknown、identity_mismatch、not_observed、observation_unknown、unsupported。`evidence[]` 返回来源文件、原始观测时间与 TTL，不改写旧时间。

missing 复用 audit 的完整资格判断：同一身份/范围/配置的正向基线、完整服务集合、新鲜成功回执和正确证据顺序。CDN 回源复用 audit 的 unknown 结果；未返回的端口/协议不补默认值。源站检查不解析台账主机名、不追踪 CNAME 或端口，不把 Resource 路由自动展开为 Server。

风险复用全部现行 audit 规则和豁免。findings 在原字段上增加 `assessment: confirmed|unknown`；旧发现即使证据失效仍保留为 unknown，不据零 findings 或豁免认定完整覆盖。台账声明的直连源站是当前 desired 风险，可独立确认；观测风险需同账号、服务、方面的有效证据。未豁免 stale_state 在已验证的旧时间上可确认，缺时间仍是 unknown。

## 显式 NA

先读 inspect 得到精确检查 id，审阅后在 Config 声明；必须启用 v1 存储标记：

```yaml
storageVersion: v1
audit:
  not_applicable:
    - check: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
      reason: "该项由已确认的外部运维流程负责；此处为虚构示例"
      owner: "example-ops"
      review_by: "2027-01-31"
```

示例 id 必须换成报告实际 id。理由和负责人必填且不能全空白；复核日期为真实 YYYY-MM-DD；重复 id、未知字段和未启用存储标记拒绝。复核日当天有效，之后恢复原检查状态并提示 na_expired；保留声明详情供复核。

NA 单列数量，expected 分母不变；原 code、证据和风险仍保留。NA 不能消除执行失败、覆盖旧时间或豁免已确认风险。全范围下未命中项提示 na_unused；限定选择不会把范围外声明误报为可删除。NA 不是历史问题恢复，不修改任何文件。

动态报告经过敏感值扫描；拒绝反射疑似凭据。MCP 使用同一实现并保留现有 4 MiB 结果限制；取消或超时不返回部分成功结果。
