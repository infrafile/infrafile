---
id: infrafile-observation
type: module-design
status: draft
title: 共享观测映射与身份
parent: infrafile-architecture
depends-on: [infrafile-ledger]
tags: [identity, observations]
---

# 共享观测映射与身份

## 职责

把供应商快照解释成最小资产字段、自然身份和可比较的 DNS/源站事实，供导入、同步基线和审计共用。导入主键与审计自然身份是不同概念，不能用自动生成的记录名判断资产是否相同。

## 映射契约

注册域名/WHOIS 映射为 Domain，CDN/Cloudflare zone 映射为 Resource，SSH 实测映射为 Server。DNS/TLS 仅为观测；未知插件明确跳过，不猜资产类型。映射不复制整个快照、不补造账号或部署外键，也不将云清单解释为所有权证明。

域名大小写与尾点规范化，Resource 保留供应商、类型及远端 ID/endpoint 身份。阿里云 CDN 的点前缀泛域名统一为 `*.`，不与根域合并；这一兼容不扩散到注册域名或其他厂商。

`audit.watch` 每个在用目标只绑定一个显式插件、实例及支持的服务。绑定包括规范化身份、endpoint 后备身份和实例配置引用，不从 Account 或 provider 猜实例。服务集合缺失表示未知；明确返回空集合才可解释为本次集合为空。源站比较不推断省略的端口或协议默认值。

## 边界

只解释传入数据，不读写 state、不调用采集器、不解析凭据、不判断快照新鲜度。是否有足够证据报告偏差由审计决定。依赖及待确认的长期限制见 `infrafile-architecture`。
