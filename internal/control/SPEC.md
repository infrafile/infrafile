---
id: infrafile-control
type: module-design
status: draft
title: 受控计划与执行回读
parent: infrafile-architecture
depends-on: [infrafile-ledger, infrafile-plugin, infrafile-protocol]
tags: [control]
---

# 受控计划与执行回读

## 职责

把线上单个动作表示为可审阅、可保存的计划，并在显式 apply 时核对台账、实例配置和远端状态，再执行与回读。它不负责修改台账期望，也不把观测快照当作实时前置条件。

## 计划与执行契约

控制目标为未归档、显式绑定已启用插件和远端标识的 cdn/dns-zone Resource。支持暂停、恢复及修改回源主机。计划记录单个动作、台账/配置哈希、变更前观测与哈希、预期结果；不保存实例配置或凭据值。

生成计划只实时 probe，不调用 control。读取计划拒绝目标符号链接、非普通文件、打开时已被替换的文件、未知字段、重复键、非规范字段大小写、错误哈希、动作与期望不一致和敏感数据；生成与读取共用 4 MiB 和共享解析器限制。计划哈希不是审批签名，持有文件不证明人工已审阅。

apply 核对当前记录与配置，重新 probe；观测与计划不符则 stale 并拒绝控制。目标状态已满足返回 unchanged，否则调用 control 后单独回读，匹配才返回 applied，不匹配为 mismatch，调用或后置回读失败可能已变更，返回 uncertain。

Cloudflare 暂停/恢复作用于整个 zone；改回源仅修改已有 Origin Rule 的 host。插件 PATCH 前也检查 expected_hash，但 GET 与 PATCH 间仍无服务端原子 CAS。没有自动写重试、回滚或跨动作事务。

## 边界与限制

调用者先校验台账并提供整次插件调用的超时；模块不写 inventory/state、不提交 Git。DNSControl 配置生成属于 CLI，线上 DNS 不经过本执行器。依赖见 `infrafile-architecture`，Cloudflare 权限和能力限制详见原有 `docs/control.md`。真实只读计划已记录验收；线上 apply、Origin Rules 写权限与 Enterprise 授权不能由离线测试宣称完成。
