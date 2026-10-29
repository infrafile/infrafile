---
id: infrafile-audit
type: module-design
status: draft
title: 离线审计与证据资格
parent: infrafile-architecture
depends-on: [infrafile-ledger, infrafile-observation, infrafile-plugin, infrafile-snapshot]
tags: [audit]
---

# 离线审计与证据资格

## 职责

将台账期望与已落盘观测对照，返回偏差、证据和提示，并应用精确豁免。规则仅求值准备好的 Input；准备过程读取文件，不调用插件、不联网、不解密、不修改文件。

## 规则与结果

支持 stale_state、orphan、zombie、origin_exposure、cdn_drift、missing。默认执行已就绪规则；stale_state 与 missing 需要非负 max-age，显式选择未就绪规则报错。只解析所选规则需要的业务观测。

生命周期按共享自然身份匹配，DNS/TLS 不作为资产清单。源站暴露按显式绑定比较地址；仅 App 绑定时展开全部在用 Server 部署，地址不明确保留 unknown，不扫描端口或追踪 CNAME。CDN 停用漂移保留既有状态规则；回源漂移另需 watch、显式期望、启用详情采集及新鲜匹配的成功回执，不猜供应商默认值。

missing 仅检查显式 watch。当前回执、实例配置、成功时间、快照哈希必须对应，观测不超龄/不在未来、对应服务返回完整集合，且同一绑定有历史实际出现的基线，才可报告本次不再可见。否则返回 missing_unknown；格式损坏仍是执行错误。未找到只表示指定凭据的查询范围，不能断言删除，静默权限收缩无法自动区分。

sync_failed、sync_incomplete 和 sync_status_unverified 说明本轮证据问题，不抹掉旧规则的历史发现。只有未豁免发现影响 audit 的 ok/退出码；unknown 等提示不能据此理解为“全部检查通过”。记录级豁免保留发现及理由，过期恢复报告；orphan/zombie 的配置忽略须限定实例和完整自然身份。

## inspect 的执行、覆盖与风险

Inspect 与 audit 共用规则、自然身份、missing 的资格判断和 CDN 回源 unknown 结果，不改变旧 audit 的输出。按配置/显式范围和固定检查契约生成分母，资产查询支持选定主键或标签集合及直接关系；未配置、账号不明、缺关系、未基线、失败、过期、不支持独立显示。快照为空或本轮失败不删除应检项。

覆盖检查的六维身份包括 rule/target/plugin/instance/service/aspect，排除观测值/消息/时间/位置。NA 需精确 id、理由、负责人和复核期限，启用 v1 标记后才可声明；分母保留，过期恢复原检查，风险及失败执行不消失。未适配的旧能力明确保留，不以基础 SSH/关系读取冒充完整健康。

历史发现保留为 unknown，只有同账号/服务/方面的有效证据才能确认当前观测风险。当前台账中的直连声明可独立确认；豁免不改变覆盖资格。成功生成报告沿用已冻结的 inspect 退出码 0，即使 ok=false；CLI/MCP 共用结果和敏感值扫描。完整字段/分母/限制由 docs/json/inspect.md 及状态表、禁网/哈希、CLI/MCP/stdio 回归约束。T08/T09 继续问题身份和显式历史推进，本实现不写历史。

## 边界

不隐式刷新观测或自动修复偏差。对 plugin 的当前依赖仅用于无 I/O 的哈希；依赖边界及其待确认的长期约束见 `infrafile-architecture`。字段和提示码的详细输出仍由 `docs/json/audit.md` 与 CLI/MCP 黄金测试约束。
