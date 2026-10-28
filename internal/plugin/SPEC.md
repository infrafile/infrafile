---
id: infrafile-plugin
type: module-design
status: draft
title: 插件宿主、采集与状态发布
parent: infrafile-architecture
depends-on: [infrafile-ledger, infrafile-observation, infrafile-secret, infrafile-snapshot, infrafile-validate, infrafile-protocol]
tags: [plugins, sync]
---

# 插件宿主、采集与状态发布

## 职责

通过同一 Client 契约运行内置和外部插件，执行显式采集、观测与已授权的控制调用，并负责同步状态的唯一发布路径。通信信封由 `infrafile-protocol` 定义；受控计划编排不在本包。

## 执行与发布

sync 接受已校验的 Repo，按插件/实例排序，只运行配置启用的实例；内置名称优先，其余直接在 PATH 查找可执行文件。每实例独立超时与外部进程，完成握手后才调用能力。外部错误原文和 stderr 不转发。

持有 `.sync.lock` 后先清除失效 watch 基线，保存 running 回执，再调用采集。成功依次发布快照、成功时间、正向存在基线和结束回执；相同观测不重写快照。文件分别原子替换，整组不是事务。采集失败保留旧观测；发布中途失败可能已替换部分文件，须留下 failed/running 证据。开始回执失败不调用插件，结束回执失败停止后续实例。同步只写 state，不写 inventory。

## 采集与信任

云清单仅覆盖凭据可见范围。第一方云插件在内存中解析认证引用，固定 API 地址、拒绝重定向、裁剪字段并稳定排序；常见敏感字段及本次完整凭据反射在发布前被拒绝。外部配置原样传递引用，不隐式解密；外部程序继承用户环境与权限，不是沙箱。

回源详情必须显式 `collect_origin: true`，每实例最多 200 个条目，任一请求失败不发布部分采集结果；复杂规则或不可观测字段保留 unknown。天翼注册域名不支持。DNS/TLS/WHOIS/SSH 的探测范围与权限见原有第一方插件指南；SSH 只执行固定 uname，但继承可信用户配置。

## 边界与验证范围

本包提供网络与状态发布能力，不能被离线调用者当作隐式刷新入口。依赖和 audit 仅使用哈希辅助函数的限制见 `infrafile-architecture`。

现有记录表明部分真实只读清单和回源调用已验收；Cloudflare 回源存在 403，部分字段仍未知。最小 IAM、注册域名列表及线上 apply 未完成全部真实验收；回放测试不能替代这些结论。验收证据以 `docs/plan/v0.3.md` 为准，`docs/first-party-plugins.md` 中“v0.3 回源尚未真实验收”的概括已滞后。
