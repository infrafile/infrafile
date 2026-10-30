# 工作台自动重读实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 完成 issue #36 PR3a：界面自动重读已保存结果，保持用户正在查看和编辑的内容。

**Architecture:** 扩展现有 `useQuery` 的可选自动读取能力，沿用取消、请求序号和 401 处理。三页各自只有一个自动读取周期；资产页串行读取摘要和到期报告，详情保留打开时的内容。新结果改变内容时先提示更新，用户显式应用后再替换。

**Tech Stack:** 现有 React、Radix Themes、TypeScript、Playwright；不新增依赖。

**Spec:** [issue #36 的 PR3 A 自动重读验收](https://github.com/infrafile/infrafile/issues/36)，及 [工作台设计](../specs/2026-10-30-workbench-macos-design.md)。本计划不含 PR3b 总览与分类字段。

## Global Constraints

- 默认每 60 秒重读；可见/焦点恢复且距上次尝试超过 30 秒时重读；后台暂停；开关仅存在内存。
- 同页至多一个自动读取周期，自动周期内串行调用只读接口；范围/页面切换取消旧请求。
- 不调用 sync、插件、凭据解析、云控制或台账写入，不延长会话。
- 初次加载、手动读取及范围切换失败清空旧内容；仅自动失败保留旧成功内容，标记最后成功时间；连续三次失败增加过期提示。
- 401（包括旧请求后到的 401）清空整个业务界面。
- 不自动替换资产详情、列表排序、焦点、滚动和编辑草稿；内容变化显示待应用更新，时间变化不算内容变化。
- 保持 CLI/MCP、inspect/audit/context、存储格式、CSP、HTTP 安全边界和原有地址。
- 沿用中文 Conventional Commits、DCO；只用虚构台账与快照。

## Review Focus

- 自动响应晚于切换范围：旧成功/失败均不能污染新范围，旧 401 仍撤销显示。
- 自动读取超过一个周期：不重叠请求，关闭或隐藏时终止自动调用。
- 连续失败后恢复：清除错误计数，不把失败旧内容标为新鲜。
- 资产新增、移除、状态重排：显示待应用数量，保持当前列表和详情；自动读取不重建编辑器。
- 外部发布/写入过程中：单请求固定目录代次；各接口的读取时间分开，不宣称跨请求事务一致。

---

### Task 1: 共享自动读取与三页接线

**Files:**
- Modify: `web/src/useQuery.ts`, `web/src/AssetsPage.tsx`, `web/src/AssetDetail.tsx`, `web/src/InspectionsPage.tsx`, `web/src/ChangesPage.tsx`, `web/src/assetState.ts`
- Create: `web/src/AutoReadStatus.tsx`, `web/tests/auto-read-browser.cjs`
- Modify tests: `scripts/test_serve_browser.cjs`, `web/src/assetState.test.ts`

**Interfaces:**
- `useQuery` 保持三参数调用兼容，新增可选 `{ automatic: true, changes?: (previous, next) => number }`；结果附 `auto` 状态与内存开关/应用更新入口。
- `AutoReadStatus` 只消费 `auto` 状态，不发请求；三页复用。
- 资产加载返回目录及到期结果；手动/首次可分别显示失败，自动周期任一读取失败保留整批旧结果。

- [ ] 先增加实际浏览器 `auto-read` suite，断言默认启用、60秒、焦点/后台、失败保留/三次升级/恢复、401、串行不重叠、切换取消、列表与详情稳定及编辑草稿保留。
- [ ] 运行 `INFRAFILE_BROWSER_SUITE=auto-read make browser-test`，确认因尚无自动重读入口失败。
- [ ] 扩展共享 hook，完成三页与更新提示接线；资产变更计数增加小型 Vitest 回归。
- [ ] 运行前端类型/单测、构建与实际浏览器专用 suite，修正失败。
- [ ] 提交本任务，使用 `git commit -s`。

### Task 2: 契约说明与发布前验证

**Files:** `docs/serve.md`, `docs/superpowers/specs/2026-10-30-workbench-macos-design.md`, 本计划。

**Interfaces:** 保留三页与默认资产页；说明只有自动重读失败保留旧内容，详情和编辑草稿不被后台替换，不承诺跨请求原子视图。

- [ ] 更新现行说明，明确界面重读和外部采集的区别、开关、失败和更新应用规则。
- [ ] 检查后端固定目录读取路径，增加或复用目录变化/请求一致性证据，记录限制。
- [ ] 运行一次 `make check build` 和 Chromium/WebKit 完整浏览器回归；同输入通过结果复用，不重复宽泛测试。
- [ ] 最终 diff 审阅；按 requesting-code-review 技能完成一次全分支独立审查，修复实际阻断问题。
- [ ] 提交文档和验收结果，交付独立 PR；总览另行推进，不关闭 #36。
