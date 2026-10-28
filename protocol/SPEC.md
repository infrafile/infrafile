---
id: infrafile-protocol
type: module-design
status: draft
title: 公开插件协议与信封 Schema
parent: infrafile-architecture
tags: [protocol]
---

# 公开插件协议与信封 Schema

## 职责

提供内置与外部插件共用的 v1 类型和内嵌 JSON Schema，作为插件作者可复用的公开包。只校验通信/快照信封，不实现传输、供应商 API 或主程序策略。

## 协议契约

外部传输使用逐行 UTF-8 JSON-RPC 2.0：一次一个请求，正整数 ID，响应 ID 对应，result/error 恰有一个。无批量、通知或 Content-Length。宿主先 describe，核对版本和声明的 sync/probe/control/import 能力，才调用对应操作；能力声明不代表主程序已提供每个能力的完整入口。

sync 参数为实例与配置，结果 data 必须为对象。probe/control 共用显式 target、action、parameters，control 必须携带 expected_hash 并由插件在写前重检。快照以 Snapshot 信封保存结果；数字精度和对象稳定编码由宿主处理，数组稳定性由插件负责。

record Schema 只冻结独立管理记录的 version/kind/data 外层，限定 FindingHistory、EditTransaction、ExecutionReceipt；它不是新增 RPC 方法。data 载荷及读写器由 T08/T09/T13/T15 实现，未经对应校验不能持久化或恢复；布局和降级边界见 `docs/compatibility.md`。Validate 遇未知 Schema 名返回错误。

## 边界

本包不导入 internal 包，不运行程序、不解析凭据、不访问网络。Schema 只描述形状；调用顺序、单条响应小于 4 MiB、超时、回读和敏感数据检查由宿主执行，不能仅靠 Validate 推定协议会话安全。详细通信示例保留在原有 `docs/plugins.md`；长期依赖约束的待确认说明见 `infrafile-architecture`。
