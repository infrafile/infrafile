---
id: infrafile-secret
type: module-design
status: draft
title: 凭据引用解析与输出遮盖
parent: infrafile-architecture
tags: [secrets]
---

# 凭据引用解析与输出遮盖

## 职责

统一检查与解析 env、SOPS/age 和 1Password URI，提供可安全输出的固定错误，并为受信任子进程的流式输出遮盖已注入的完整值。

## 检查与解析

Check 不请求凭据值：env 检查是否存在，SOPS 检查加密字符串及 age/MAC 元数据，1Password 只请求字段 ID。检查通过不证明解密密钥可用或 MAC 完整；Resolve 才在内存中取得值并拒绝不能用于环境变量的 NUL。

CheckOffline 供 doctor 使用：env/SOPS 沿用本地检查；op 仅检查引用语法并返回 online-required，不执行 op 或身份命令，不联网、不解密。原 Check 的元数据查询行为保持不变。

SOPS 支持数据仓内单组 age 加密 YAML 的字符串字段；拒绝越界路径、其他密钥后端、多组、数组下标和带点键名。age 身份加载支持可取消的非交互命令，不提供加密身份的口令交互或外部 age 插件。1Password 使用本机可信 op，引用不支持查询参数或附件。

SOPS 密文读取拒绝符号链接、非普通文件及超过 4 MiB 的输入；打开前后核对文件身份并限量读取，离线预检与显式解析共用此边界。不隔离同一用户对文件的并发修改。

输出遮盖支持跨写入分块和多行完整值，但不识别编码、变换或跨 stdout/stderr 拆分的秘密。解析器和遮盖器不构成子进程沙箱。

## 边界

不加载资产、不选择插件、不修改父进程环境或写出明文；子进程创建、环境注入、进程组/Job Object 和退出码归 CLI 管理。本包可读受限加密文件并执行 op/身份命令，不能从“无项目内依赖”推断无外部副作用。长期依赖约束的待确认说明见 `infrafile-architecture`；详细后端边界见原有 `docs/secrets.md`。
