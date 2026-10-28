---
id: infrafile-agent
type: module-design
status: draft
title: 内嵌指南与项目级安装
parent: infrafile-architecture
tags: [agent]
---

# 内嵌指南与项目级安装

## 职责

随二进制内嵌 agent 指南，将构建版本与 commit 填入文本，并按显式指定的客户端安装到项目技能目录。安装内容与同一二进制的 guide 输出一致。

指南要求在判断支持范围时读取离线 capabilities 目录，并区分条件支持、未实现、未知与真实验收；工具实现与结果契约由 CLI/MCP 入口负责。

## 安装契约

仅支持项目内 Codex 的 `.agents/skills/infrafile/SKILL.md` 和 Claude Code 的 `.claude/skills/infrafile/SKILL.md`；项目目录须已存在，不必是台账。

相同内容返回 unchanged，不改修改时间；已有不同内容、符号链接或非普通文件拒绝并保留。新文件独占创建，写失败清理本次未完整写入的文件。升级已有指南需要使用者先审阅并移走旧文件。

## 边界

不加载台账、不启动 MCP、不注册客户端设置、不修改 AGENTS.md/CLAUDE.md，不自动写个人目录。写入成功不等于客户端已加载；MCP 工具集属于 CLI 入口。当前依赖及待确认的长期限制见 `infrafile-architecture`，用户操作契约见原有 `docs/agent.md`。
