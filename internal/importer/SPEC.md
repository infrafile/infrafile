---
id: infrafile-importer
type: module-design
status: draft
title: 离线导入与只建不覆盖
parent: infrafile-architecture
depends-on: [infrafile-ledger, infrafile-observation, infrafile-snapshot, infrafile-validate]
tags: [import]
---

# 离线导入与只建不覆盖

## 职责

从 SSH 配置或已有 state 生成可审阅的最小资产草稿，并在显式要求时创建新文件。输入数据是历史事实，不作为期望状态或自动外键来源。

## 创建契约

CLI 先校验目标数据仓。所有候选在任何输出或写入前接受 Schema、名称长度及敏感数据检查。同一新主键的相同候选去重，不同内容冲突则整次预检失败；已存在主键始终优先，包括归档记录，不合并、不刷新、不恢复归档。

默认只输出草稿；待审目录与写入 inventory 互斥，待审目录不能通过路径别名落入 inventory。`--write` 重新读取原始来源，不读取手改后的待审草稿。发布以完整临时文件加硬链接实现不替换已有名字，目标文件系统需支持硬链接；多文件不是事务，I/O 中途失败保留已创建记录，重试只补缺项。

state 导入使用共享映射，不要求原插件仍启用，不推断新鲜度；正在同步时拒绝读取。SSH 配置只映射 HostName/User/Port，遵守受限 Include 和匹配规则，不执行 Match exec、ProxyCommand 或其他配置命令，不重建完整 SSH 配置。解析有累计文件、文本、展开工作量上限并响应取消。

## 边界

不连接服务器、请求云 API、解析凭据或执行 ssh；不改来源、配置、state 或已有 inventory。共享身份及依赖见 `infrafile-architecture`，具体 SSH 支持与限额见原有 `docs/import.md`。
