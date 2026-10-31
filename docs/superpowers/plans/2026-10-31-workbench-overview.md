# 工作台总览实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 完成 issue #36 PR3b：默认总览、服务端资产分类、登记到期、按应用证书、登记服务器配置与数据新鲜度。

**Architecture:** 基于 PR #43 的自动重读结果应用机制。复用现有 Web `asset-summaries`、`expiry`、`certificates`；摘要服务端增加类别及 Server.hardware 四个登记字段的限定投影，避免逐服务器查询。Go 仍负责判断与安全边界，React 负责展示和导航；不新建通用查询平台。

**Tech Stack:** 现有 Go、React、Radix Themes、TypeScript、Vitest、Playwright；无新依赖。

**Spec:** [issue #36 PR3 B 总览](https://github.com/infrafile/infrafile/issues/36)；[工作台设计](../specs/2026-10-30-workbench-macos-design.md)。已合并 PR1 #37 / PR2 #40，依赖 PR3a #43；本分支从 #43 的 decf805 起步，不修改其审阅范围。

## Global Constraints

- 总览为默认落地页；保留 `/assets#key=…`、`/inspections`、`/changes` 及原 fragment 含义。
- 在用资产四类：有风险、需处理、暂无检查、范围内通过；每条只归一类，四类总数等于在用总数。类别由服务端提供，前端不猜 reason.code；未知代码保守需处理。
- 分类不改 inspect/audit/context 的既有判断或 CLI/MCP、存储契约；暂无检查明确不代表通过。
- 到期使用服务端日历日结果。Domain 缺失日期计分母；Resource/Server 列已登记并注明未登记数，不把缺失当零或健康。
- 证书只消费已保存 TLS 结果，按应用、无应用、未关联、歧义、未采集分别表达；无快照不代表正常，失败/过期不展示正常态。
- 服务器配置只投影 `hardware.cpu/memory/disk/os` 原始字符串，标“台账登记值 · 非实时”；缺失为未登记，无使用率、换算、合计或实时探测。
- 页面读取时间和数据采集时间分开；展示保存观测的最新/最旧时间，过期/缺失明确，不能把新页面时间当新观测。
- 自动读取复用 #43 的失败/401/待应用语义。请求数不随资产数增长；各区块独立失败，不互相遮蔽。
- 不新增采集/插件/凭据解析、云控制、通知、用户角色或自动写入；不改变私有 patrol 范围。只用虚构夹具、中文 Conventional Commits 与 DCO。

## Review Focus

- 混合结构性不支持与真实采集失败：只要仍有应处理项就不能归为“暂无检查”，即使首要 reason.code 为 unsupported。
- 同时存在风险/unknown、全部 NA、豁免、无配置、未知代码及归档：分类完整、优先级确定，不修改原巡检结论。
- Server.hardware 投影不带 address、spec、secrets、x-扩展；旧 CLI/MCP 原样，Web 旧字段含义不变。
- 新总览的认证、CSRF、Host/Origin、CSP、取消和根身份保持；旧深链、窄屏、键盘和手动编辑不被默认路由变化破坏。
- 单块失败、TLS 过期/歧义/缺失、没有观测、未来时间和自动迟到结果：各自明确状态，不产生假绿或覆盖用户范围。

---

### Task 1: 服务端类别与服务器登记投影

**Files:** `internal/audit/asset_summaries.go`、其测试、`cmd/infrafile/serve_summaries_test.go`；相应 Web contracts/assetState 测试。

**Interfaces:** Web 摘要行追加 `category` 和仅 Server 的可选 `hardware`；枚举、原因码对照和优先级由完整现有检查代码推导后固定。现有 summaryReason 保持原语义；分类基于完整报告，避免首个原因掩盖其他需处理项。

- [x] 先列全当前检查/原因代码，写分类和限定字段投影失败测试；增加混合 unknown、不支持及未知代码反例。
- [x] 最小实现服务端类别与硬件字段白名单，验证摘要逐行 inspect 等价性及边界。
- [x] 更新前端类型和四类筛选，兼容已有状态、归档及深链；仅运行直接相关检查并提交。

### Task 2: 总览页面与默认路由

**Files:** 新 `web/src/OverviewPage.tsx` 与必要展示/测试；`App.tsx`、`scope.ts`、`api.ts`、`contracts.ts`、`styles.css`、`cmd/infrafile/serve_http.go`。

**Interfaces:** `/` 和 `/overview` 展示总览，已有三页保留。总览按固定数量调用摘要、到期与证书接口，复用 AutoReadStatus；服务端类别直接驱动统计和资产筛选链接，服务器配置从摘要限定字段读取。

- [x] 先写总览统计/空态/单块失败/证书边界和浏览器默认路由、深链与请求数量失败测试。
- [x] 实现资产分类、到期、按应用证书、服务器登记配置和新鲜度；更新状态导航与无障碍标签，保持各块独立失败。
- [x] 验证窄屏、主题、键盘、请求数不随资产增长、自动失败/401/取消和旧三页流程；提交。

### Task 3: 契约说明与交付

**Files:** `docs/serve.md`、工作台设计、相应 JSON/Web 说明、本计划。

- [x] 写明类别/原因对照、缺失分母、硬件白名单、采集时间来源和各块失败规则。
- [x] 一次完整 `make check build`，Chromium/WebKit 最终浏览器流程；复用未变输入的结果。
- [x] 一次全分支独立代码审查，修复阻断问题，最终 diff 审查和独立 PR；正式发布与真实台账验收继续单列。
