# 问题身份与恢复规则（v1.0 开发）

T08 为现行六条审计规则提供稳定身份和纯状态转换。`audit` / `inspect` / MCP 查询仍然只读，不创建或更新问题历史；T09 负责显式 `findings refresh`、锁、保存与变化查询。旧 findings 的数量、原字段、豁免及退出码保持。

## findings 的兼容扩展

每条 finding 增加 `issues` 数组，其元素包含：

```json
{
  "version": "v1",
  "fingerprint": "64 位小写十六进制",
  "scope_hash": "64 位小写十六进制",
  "key": {
    "rule": "cdn_drift",
    "target": "Resource/edge",
    "plugin": "aliyun",
    "instance": "example",
    "service": "cdn",
    "aspect": "origin/hosts",
    "natural": {"kind": "Resource", "provider": "aliyun", "resource_id": "example-id"}
  }
}
```

此处摘要是格式说明，实际摘要由程序计算。`fingerprint` 为 Go JSON 序列化 `{version:"v1",key}` 的 SHA-256。key 字段顺序为 rule、target、plugin、instance、service、aspect、variant、natural、related；空 variant / natural / related 省略，related 按主键排序。自然身份使用共享规范化，Resource 的 ID 身份和 endpoint 身份保持类型差别，不对展示文字做哈希。

指纹不包含消息、检查/观测时间、文件位置、观测状态/地址/端口/协议、豁免或 NA。不同账号实例、服务、方面、区域和关联源站分别标识。域名大小写、尾点、IPv4 映射及阿里 CDN 泛域名沿用共享身份规则。未知身份版本、非法摘要、非规范自然身份、混用 ID/endpoint 或未知方面拒绝。

| 规则 | 问题 key 的语义 |
| --- | --- |
| stale_state | target = plugin/instance，aspect = freshness，service 为空 |
| orphan | target 保留插件/实例/自然身份的展示路径；natural 是观测资产的结构化身份，service 是对应清单服务，aspect = inventory |
| zombie | target 是归档记录主键；natural 是该记录的声明身份，插件/实例/清单服务分别保留，aspect = inventory |
| missing | target 和 natural 是显式 watch 的声明目标/身份，plugin/instance/service 是唯一绑定，aspect = presence |
| cdn_drift 状态 | 声明的 Resource 身份、对应实例和服务，aspect = status；有 area 时另记 variant，两个区域不合并 |
| cdn_drift 回源 | 同一旧 finding 按实际差异分别给出 origin/hosts、origin/port、origin/protocol 身份；消息和观测值变化不改身份 |
| origin_exposure | target 是 Domain 主键，natural 是绑定 hostname 的 Domain 身份；related 是 Server 和可选 App 主键；A / AAAA 分别为 address/A、address/AAAA。desired_dns、DNS addresses 与 Cloudflare zones.dns_records 是不同服务/来源 |

回源仍保留原有一条 finding 和 differences 数组；新增 issues 可含多个方面，不把旧调用者的 finding 数量改成历史问题方面数。精确字段及九方面摘要由 `internal/audit/testdata/issue-identities.json` 锁定。

区域 area 如出现必须是字符串；损坏类型拒绝，不默认为“无区域”。指纹算法、字段语义/顺序或规范化改变时必须使用新的身份版本，不能只重写黄金数据来接受漂移。

## 范围与资格独立

同一 fingerprint 可以有不同 scope_hash。历史条目的完整键是二者的组合；新范围不覆盖旧范围。scope_hash 只绑定会影响该检查的声明：实例配置摘要、TTL、目标自然身份/账号引用、watch 绑定、规范化期望回源，或绑定及相关部署/Server 身份。规则、scope 格式均绑定 v1。

观察值、时间、快照字节、记录备注/标签、豁免和 NA 不进入 scope_hash。实例配置内的凭据引用或文件路径是配置的一部分；移动诊断文件不改身份，修改配置引用则需要新范围基线。不会解析引用背后的秘密；凭据值或厂商权限静默变化仍不能由配置哈希自动识别。

- 配置、身份、期望或绑定变化，使旧条目 out_of_scope；如果新范围仍发现问题，另建该范围条目，旧历史保留。
- orphan 在同范围得到登记或在完整新鲜清单中不再存在，可以 clear；登记动作不改变整个清单的 scope_hash。
- zombie 重新在用、删除归档记录或出现同身份的在用归属，属于范围变化；不伪造云端已清理的恢复。
- 观测缺失原有资源 ID、区域维度变化、原区域没有对应观测或映射不清，保持 unknown，不借其他对象/区域恢复。
- 当前声明中的直连风险可基于新的明确声明求值，单独使用 desired_dns 身份；不代表线上 DNS 已发布或清理。

`EvaluateIssues` 复用同一次 inspect 的规则、快照、回执、覆盖和基线，调用前由入口完整校验台账。当前问题与已知问题的并集逐项得到 `{identity,result,in_scope,found,code,waiver}`。result 为 confirmed / clear / unknown，found 表示原规则当前仍有该方面的 finding；没有 finding 本身不构成 clear。只有同一范围、账号、服务/区域/方面的完整新鲜证据才能清除观测问题。

## 状态转换

历史内存状态包含 identity、lifecycle、last_in_scope_state、assessment、waiver、first_seen、last_confirmed、last_checked、recovered_at、occurrences。`last_in_scope_state` 保存离开范围前的 open/recovered，便于返回原范围时保持正确发生次数。首次确认 occurrences=1；恢复后再次确认才增加次数。同一个问题持续确认不重复计次。

| 原 lifecycle / 离开前状态 | confirmed | clear，未豁免 | clear，仍有有效豁免 | unknown |
| --- | --- | --- | --- | --- |
| open | open，持续确认 | recovered | open | open |
| recovered | open，复发并递增次数 | recovered | recovered | recovered，保留既有恢复事实 |
| out_of_scope / open | 返回 open，保持次数 | 证据充分后 recovered | 返回 open | 返回 open，资格未知 |
| out_of_scope / recovered | 返回 open，复发并递增次数 | 返回 recovered | 返回 recovered | 返回 recovered，资格未知 |

任何已有状态遇到范围退出均为 out_of_scope，保留离开前状态和既有恢复事实；不创建 recovered 事件。缺失评估保留原 lifecycle、将 assessment 置 unknown；不会把没跑过当通过。首次 clear 或没有 finding 的 unknown 不创建问题。首次遇到资格不足的历史 finding 可保存未确认 open，occurrences=0；资格恢复后才记录首次确认。

豁免独立保留，不改变指纹或范围；仍有 finding 时沿用原 audit 的精确匹配结果。没有 finding 时仍存在的适用豁免不能单独导致恢复；移除/过期后须重新获得有效 clear 证据。NA 不删除当前已确认风险；没有正向发现时，NA 也不能当作恢复证据。

`recovered_at` 保存最近一次有证据的恢复时刻；复发或范围退出时不改写这一历史事实，当前状态由 lifecycle 判断。first_seen 保留，last_confirmed 仅在本次确认时更新，last_checked 记录本次显式状态推进。复发保留同一条范围历史；范围变化保留旧条目并单列新范围。

纯函数 `AdvanceIssues` 返回新状态及 opened / unconfirmed / confirmed / recovered / reopened / out_of_scope / returned / unknown / clear / waived / unwaived 变化，不写文件。相同评估和时刻幂等，正常持续确认不生成重复 opened；重复身份/评估、未知版本、矛盾结果、非法时间顺序、时钟倒退、发生次数溢出或取消均拒绝。记录 payload、容量、导出与磁盘失败保证由 T09 实现，不以本状态机代替持久化验收。
