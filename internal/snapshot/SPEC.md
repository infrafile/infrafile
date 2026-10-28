---
id: infrafile-snapshot
type: module-design
status: draft
title: 快照与执行证据读取
parent: infrafile-architecture
depends-on: [infrafile-ledger, infrafile-validate, infrafile-protocol]
tags: [state]
---

# 快照与执行证据读取

## 职责

为离线调用者读取并严格解析快照、成功同步时间、最近执行回执和正向存在基线。它解释落盘证据的格式，不承担采集、发布或业务规则判断。

## 存储契约

`state/<plugin>/<instance>.yaml` 是带 Snapshot 信封的观测；文件名必须对应实例名。`_sync.yaml` 记录每实例最近成功同步的命令开始时间。`.sync-status.yaml` 记录最近 attempted_at、running/succeeded/failed、配置哈希以及成功时的快照字节哈希或失败时的固定错误码。`.presence.yaml` 只保存目标的 binding_hash 与 last_seen。

隐藏回执和基线文件让旧快照读取方式跳过新增元数据，同时保留成功时间文件的既有含义；这不代表新增 watch、waiver 和 origin 配置也能被旧版校验。

读取拒绝符号链接、不合法信封和损坏数据；存在 `.sync.lock` 时拒绝读取，避免使用正在发布的文件组合。回执与基线最多 4 MiB，复用受限 YAML 解析，并拒绝未知字段和非法组合。缺少 state 返回空快照；缺少回执或基线保持缺少，不合成为成功。

## 边界

不写文件，不清除锁，不打开插件。哈希用于对应性检查，不证明签名、云端授权或清单完整；是否满足当前规则的证据资格由消费者判断。依赖与长期边界的待确认说明见 `infrafile-architecture`。
