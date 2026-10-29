---
id: infrafile-validate
type: module-design
status: draft
title: 台账校验与敏感数据检查
parent: infrafile-architecture
depends-on: [infrafile-ledger, infrafile-observation]
tags: [validation]
---

# 台账校验与敏感数据检查

## 职责

在同一次加载的台账上检查 Schema、文件名与主键、外键、归档引用、显式观测范围和常见明文凭据，并向调用者返回可复用的 Repo 和稳定排序的问题。校验结果不自动修复数据。

## 校验契约

在用记录的引用必须存在且未归档；归档记录可以保留到归档目标的引用。`audit.watch` 只在基础记录合法后求值，Resource 的期望源站字段另有域名/IP 语义检查。

`--base` 是删除保护的前提：仅在指定 Git commit 基线时比较记录文件是否被删除。没有基线不能证明历史保留完整，坏基线是执行错误，不能当作没有删除。

面向快照的 `SnapshotSafe` 在普通明文检测之外递归拒绝敏感字段名，即使值为空或是引用也拒绝。检测用于阻止常见误写，不能保证识别任意秘密；错误路径不得回显敏感值。

## 边界

Run 与内存 Check 共用 Schema、主键、外键、归档引用、watch、源站和明文检测。Check 要求完整配置并拒绝重复主键，供导入审阅构造完整候选；不代替加载器的文件/路径布局检查。PlaintextProblems 复用同一检测，使未选中/有冲突的候选也不反射疑似秘密。

不联网、不解析引用、不执行云动作、不写 inventory 或 state。Git 基线检查可以调用本机 git。台账合法不代表引用可解密、远端可用或审计无偏差。依赖关系与待确认的长期限制见 `infrafile-architecture`。
