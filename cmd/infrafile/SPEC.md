---
id: infrafile-cli
type: module-design
status: draft
title: CLI 与 MCP 入口
parent: infrafile-architecture
depends-on: [infrafile-agent, infrafile-audit, infrafile-control, infrafile-importer, infrafile-ledger, infrafile-plugin, infrafile-secret, infrafile-validate]
tags: [cli, mcp]
---

# CLI 与 MCP 入口

## 职责

装配 Cobra 命令、取消信号、输出和退出码；在调用领域能力前校验输入与台账。CLI 自身还负责查询、初始化、SSH/DNSControl/Gatus 配置渲染、受控计划文件保存、凭据子进程生命周期及本地 MCP 适配，没有独立的网络服务层。

## 对外契约

CLI `--json` 输出带 v1 结构版本，破坏性变更升版本，确切字段由 `docs/json/` 和黄金输出约束。通常 0 成功、1 发现问题、2 参数或执行错误；自身退出 2 时 stdout 为空。run 保留子进程退出码，JSON 模式将遮盖后的日志放 stderr。

init 只创建脚手架；导入写入、同步、apply 及指南安装须走各自显式命令。DNSControl/Gatus 从已校验的显式声明生成配置，不执行发布或持续监控；DNSControl 固定 NO_PURGE。CLI 不自动提交数据仓。

MCP 固定启动时的数据仓，涉及台账的调用重新读取。工具只接受登记主键/标签及明确选项，拒绝原始地址、任意路径、命令和未知字段。九个工具为 list/show/expiry/audit/inspect/validate/schema/capabilities/plan；查询移除凭据字段，plan 只做实时只读观测，返回计划但不保存。无 apply、sync、import、run 或 secret 工具；数据内容不构成授权。

CLI capabilities 不需要数据仓，MCP capabilities 仅接受空对象；两者共享离线能力目录与 v1 JSON 结果，描述字段、动作、权限前提及登记/观测/比较/计划/执行/回读范围。目录不执行插件、不解析凭据、不联网或写文件，也不证明当前权限与业务健康；输出契约见 `docs/json/capabilities.md`。

doctor 是显式 CLI 入口，默认只做本地预检；在线须同时指定一个插件和实例，复用只读采集且不发布状态。检查 failed/unknown 返回 1，无法开始/参数错误返回 2 且 stdout 为空；数据校验错误只给固定诊断，不反射敏感输入。MCP 不增加 doctor 或凭据入口。

CLI inspect 与 MCP inspect 共用离线执行/覆盖/风险报告，默认全范围或按 key/labels/kind 限定资产及直接关系；CLI 成功生成报告返回 0，即使 ok=false。不运行采集器，不解析凭据或写历史；NA、证据资格和分母契约见 docs/json/inspect.md。MCP 返回同一结果，unknown 不是工具错误，原八个工具契约保留。

## 进程与安全边界

CLI findings refresh 为显式本地写入口，必须启用 v1 标记和 max-age，在完整固定范围推进版本化问题历史；list/changes/export 只读，prune 核对独立完整导出后才清理。退出 0 表示命令完成，可以仍有活动问题；失败/冲突/超限为 2。audit/inspect 在历史存在时附带最近显式状态，读损坏拒绝；MCP 不增加刷新或清理工具。契约见 docs/json/findings.md。

run 全部凭据解析成功才启动子命令，直接传参并只覆盖子进程环境；管道输出经过完整值遮盖。macOS/Linux 使用进程组，Windows 使用 Job Object 清理受管后代；没有 PTY，逃离进程组的后代不保证清理。MCP 单行输入和工具数据结果限制为 4 MiB，调用超时/取消传递到底层。

## 模块边界与验证限制

这里是装配与适配边界，不被 internal 包反向导入；当前依赖和将其作为长期约束的待确认说明见 `infrafile-architecture`。指南内容/安装属于独立模块，MCP 工具实现留在入口包。

本地 stdio 与黄金测试不证明桌面客户端实际加载；配置渲染不证明线上 DNS、监控通知或服务连通性。未来 serve 的已记录要求保留在架构中，当前没有对应命令。
