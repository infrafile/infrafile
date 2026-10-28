---
id: infrafile-ledger
type: module-design
status: draft
title: 台账加载与数据定义
parent: infrafile-architecture
tags: [inventory, schema]
---

# 台账加载与数据定义

## 职责

加载数据仓的配置与资产记录，保留字段位置和可定位问题；持有六种资产的 Schema、kind 目录映射和外键声明。它是文件表示到内存模型的入口，不把加载成功等同于业务校验通过。

## 数据契约

记录身份为 `Kind/metadata.name`，文件布局为 `inventory/<kind 复数>/<name>.yaml`。加载器拒绝配置、inventory、kind 目录及记录文件的符号链接；数据问题作为 Problem 汇总，无法继续读取才返回 error。

共享 YAML 解析器接受受限锚点复用，拒绝重复键、多文档、循环别名；限制 100 层嵌套、100,000 个展开节点及 16 MiB 展开路径与标量。解析响应取消，日期保持字符串，防止导入、计划和状态读取使用不同解释。

内嵌 JSON Schema 是字段与 `x-ref` 外键声明的来源，运行时编译和导出复用同一份定义；Schema 编译使用虚拟文件地址，不请求网络。归档由 metadata.archived 是否存在判断。

Config 可显式声明 storageVersion: v1，为后续管理记录启用最低存储契约；缺省保持旧布局，未知值拒绝。该标记由 Schema 校验，不由 Load 自动迁移或写入；旧版完整业务校验会因未知字段拒绝。公共接口及恢复策略见 `docs/compatibility.md`。

## 边界

本包不写资产、不解析凭据、不调用插件。加载不执行跨记录外键、明文或 Git 删除检查。当前项目依赖及长期边界的待确认说明见 `infrafile-architecture`。
