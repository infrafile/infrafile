---
id: infrafile-control
type: module-design
status: draft
title: 受控计划与执行回读
parent: infrafile-architecture
depends-on: [infrafile-ledger, infrafile-plugin, infrafile-protocol, infrafile-validate]
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

## v2 审阅绑定与持久执行

旧无存储标记仓继续生成/执行 v1；显式 storageVersion: v1 仓生成 v2，拒绝 v1 执行。v2 digest 对省略自身后的 Plan 稳定 JSON 做 SHA-256，expires_at 固定 generated_at 后30分钟。Execute 接受已审阅摘要、适用授权来源和主体说明；说明不证明身份或审批。开始和发送 control 前核对时效，控制前重新加载/校验当前目标和实例配置。

ExecutionReceipt v1 信封固定在 history/executions/<digest>.json，保存完整计划、说明、阶段、前提及回读哈希。每次原子保存均文件/目录 fsync，新父目录也持久化；prepared 和 write_started 阶段只能为 uncertain，后者须在控制请求前持久。后置保存失败返回 uncertain。进程崩溃后本机 advisory lock 自动释放，已有同摘要只 probe，不再次 control，也不改写原证据或推断历史成功。不同副本、同 OS 用户的恶意操作、云端请求及其他执行器不受本机锁保证。Windows v2 执行拒绝，读/审阅不受此限制。

读回执不依赖当前 inventory，有界严格解析且不写锁/历史、不联网。CLI list/show 和 Web 只读卡片共享读取器；没有未执行计划文件自动扫描或云执行 Web API。每文件4MiB、256回执/64MiB及目录条目/共享展开预算，损坏/超限整份失败，不自动裁剪。详细冻结字段见 docs/json/executions.md。

## 边界与限制

调用者先校验台账并提供整次插件调用的超时；模块仅在显式新存储的 v2 Execute 写执行历史，不写 inventory/state、不提交 Git。DNSControl 配置生成属于 CLI，线上 DNS 不经过本执行器。依赖见 `infrafile-architecture`，Cloudflare 权限和能力限制详见原有 `docs/control.md`。真实只读计划已记录验收；线上 apply、Origin Rules 写权限与 Enterprise 授权不能由离线测试宣称完成。
