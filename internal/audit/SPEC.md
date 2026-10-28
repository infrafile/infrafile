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

## 边界

不隐式刷新观测或自动修复偏差。对 plugin 的当前依赖仅用于无 I/O 的哈希；依赖边界及其待确认的长期约束见 `infrafile-architecture`。字段和提示码的详细输出仍由 `docs/json/audit.md` 与 CLI/MCP 黄金测试约束。
