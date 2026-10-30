# infrafile React + Radix 三栏工作台 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将工作台迁移到 React + Radix Themes，实现“发现问题资产 → 连续选择 → 查看完整结论与依据”的 C 三栏流程，保留已有功能和单二进制分发。

**Architecture:** Go 继续负责认证、读取隔离、巡检语义和编辑事务；新增仅用于 Web 的批量资产摘要接口。React 消费现有契约，Vite 生成同源静态资源并嵌入 Go。先验证组件与 CSP，再完成摘要、页面迁移，最后统一切换资源入口，避免交付缺失功能的半套页面。

**Tech Stack:** Go 1.26（沿用 go.mod）、React、Radix Themes、TypeScript、Vite；Vitest 测纯逻辑，复用现有 Playwright 浏览器脚本。

**Spec:** [已批准的工作台设计](../specs/2026-10-30-workbench-macos-design.md)。用户于 2026-10-30 确认本计划并选择 Native 实施；不授权合并、发布或真实资产操作。

## Global Constraints

- 默认 TTL 为 7 天；保留 `/assets`、`/inspections`、`/changes` 及 `key/days/issue` fragment。名称/主键/标签搜索、六类资产与归档访问保持可用。
- 摘要每行等价于相同已发布输入代次、时间和 TTL 下的单资产 `inspect`；风险、未知、NA、豁免和执行资格不能混为健康状态。
- 沿用 64 KiB 请求、4 MiB JSON 响应、并发 4、30 秒查询上限及会话剩余时间约束；超限整份失败，不静默截断或保留旧绿状态。
- CSP 保持 `default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'`。不加通用 `unsafe-inline`、eval、CDN、远程字体。
- 保留配对、双设备会话、CSRF、退出/撤销、秘密过滤、默认只读、显式编辑、digest 审阅及恢复。无浏览器采集、云执行、自动重试写入或自动 push。
- 亮/暗主题、文字状态、键盘、Mac Safari/Chromium、窄屏都属于验收；不添加拖动分栏、健康评分、图表、通用组件平台、路由库或全局状态库。
- 使用虚构测试台账；本计划不读取或操作 infra-vault 实际资产。运行端不依赖 Node，开发依赖与 source map 不进入发行资源。
- 实施前检查 Git 状态，保留其他改动；任务完成后仅提交所属文件，使用 `git commit -s`。本轮仅保存设计和计划。

## Review Focus

1. 发布目录切换、同一准备结果被多个资产复用：不能混入下一代输入，也不能因修改某行而污染其他行。任务 2。
2. `Keys=nil` 与 `Keys=[]`、空选择、全部 NA、无配置资产：不能因合并路径而出现假通过或把全局问题分给每行。任务 2。
3. 用户主动筛掉选中项，与显式链接到筛选范围外的资产：前者换选并提示，后者保留目标并提示范围外。任务 4。
4. A→B 快速切换后 A 延迟成功/失败，以及会话在途失效：不能覆盖 B、遗留旧内容或继续显示已撤销会话数据。任务 3、4。
5. 大于安全整数的历史游标、缺少无损 JSON 支持的浏览器、提交回复丢失：不能取整、变成可提交的新数值或盲目重试。任务 3、5。

---

## 文件与责任

以下为目标文件；先读所属文件与直接调用方，再修改。没有列出的业务契约不因 UI 迁移扩展。

| 文件 | 责任 / 所属任务 |
| --- | --- |
| 新增 `web/package.json`、`package-lock.json`、`tsconfig.json`、`vite.config.ts` | 锁定依赖、类型检查、构建和测试脚本；1、6 |
| 新增 `web/tests/csp.tsx`、`web/tests/csp.css` | 仅测试使用的真实组件交互夹具，不进入正常产物；1 |
| 修改 `scripts/test_serve_browser.cjs` | 复用临时台账、配对、双会话和事务夹具，增加 CSP/三栏/主题用例；1、4–6 |
| 新增 `internal/audit/inspect_input.go`、`asset_summaries.go`、`asset_summaries_test.go` | 一次准备、共享范围计算、紧凑汇总及等价测试；2 |
| 修改 `internal/audit/inspect.go`、`inspect_test.go`；必要时修改 `inspect_checks.go` 的输入访问 | 原算法迁移；保持 `context.go`、`issue_evaluate.go` 调用兼容；2 |
| 修改 `cmd/infrafile/serve_queries.go`；新增 `serve_summaries_test.go` | Web 专用查询注册、选择、固定目录代次和边界测试；2 |
| 新增 `web/src/contracts.ts`、`api.ts`、`api.test.ts`、`scope.ts`、`scope.test.ts`、`useQuery.ts` | 用到的 DTO、无损读取、取消、401、既有 fragment；3 |
| 新增 `web/src/main.tsx`、`App.tsx`、`styles.css`、`AssetsPage.tsx`、`assetState.ts`、`assetState.test.ts`、`AssetDetail.tsx`、`InspectionSections.tsx` | 三栏、筛选与选择、主题、完整详情及共用巡检展示；4 |
| 新增 `web/src/InspectionsPage.tsx`、`ChangesPage.tsx`、`InventoryEditor.tsx` | 迁移巡检、问题变化、执行回执与编辑恢复；5 |
| 修改 `cmd/infrafile/serve_http.go`、`web/serve.html`、`serve_views_test.go`、`serve_test.go`；删除旧 `cmd/infrafile/web/app.js`、`app.css` | Go 模板、受限内嵌资源、移除旧页面实现；6 |
| 修改 `.gitignore`、`Makefile`、`.github/workflows/ci.yml`、`release.yml`、`.goreleaser.yaml`、`CONTRIBUTING.md`、`docs/serve.md` | 构建先后顺序、浏览器门禁、使用与维护说明；1、2、6 |

## 固定依赖与构建约定

2026-10-30 已查询 npm 官方注册表的版本和 peer/engine 条件；以下是计划采用的版本，不表示已安装或已验证兼容。任务 1 实际安装并生成锁文件，不使用 `latest`、`^` 或 `~`。

| 用途 | 固定版本 |
| --- | --- |
| 运行依赖 | `react@19.3.1`、`react-dom@19.3.1`、`@radix-ui/themes@3.3.0` |
| 构建 | `typescript@7.0.2`、`vite@8.3.5`、`@vitejs/plugin-react@6.1.2` |
| 测试 | `vitest@5.0.5`、`playwright@1.64.1` |
| 类型 | `@types/react@19.3.0`、`@types/react-dom@19.3.0`、`@types/node@22.20.5` |

Node 下限定为 **22.12.0**，CI 使用 Node 22；运行程序不需要 Node。仅使用必要依赖，不额外加入图标库、DOM 模拟环境、组件测试库或数据请求库。来源：[Radix 安装](https://www.radix-ui.com/themes/docs/overview/getting-started)、[Vite](https://vite.dev/guide/)、[Vitest](https://vitest.dev/guide/)，精确版本由 npm 官方注册表核对；Node 类型选择其 22 系列的最新稳定版本。

统一脚本：`npm --prefix web ci`；`npm --prefix web run typecheck`；`npm --prefix web test -- <test-file>`（`vitest run`）；`npm --prefix web run build`；`npm --prefix web run build:csp`；`npm --prefix web run test:browser`（调用仓库已有脚本）。脚本定位 `web/node_modules/playwright`，保留已有 `INFRAFILE_PLAYWRIGHT_MODULE`、`INFRAFILE_BINARY` 覆盖入口。

正常 Vite 构建输出 `cmd/infrafile/web/dist/app.js` 和 `app.css`，单入口、无动态分包、无 source map；测试构建输出 `web/.csp/app.js` 和 `app.css`。只允许这两个固定资源，不增加 manifest 解析和任意静态目录服务。忽略 `web/node_modules/`、`web/.csp/`、`cmd/infrafile/web/dist/`；产物由构建生成，不提交生成文件。

### Task 1：验证 Radix 在正式 CSP 下的可用组件

**Files:** 文件表中的前端配置、CSP 夹具、浏览器脚本和 `.gitignore`。

**Interfaces:** 新增浏览器脚本 `INFRAFILE_BROWSER_SUITE=csp|views|all`（默认 `all`）、`INFRAFILE_BROWSER_ENGINE=chromium|webkit`（默认 `chromium`）；`async cspViews(page, entry)` 复用脚本现有服务与配对夹具，返回通过项。任务 4、5 暂用显式 `INFRAFILE_BROWSER_PREVIEW=1` 加载新 bundle，任务 6 删除该开关及桥接；默认测试始终访问实际产品。正常应用入口留到任务 4，不新增生产测试路由。

- [ ] **1. 写交互失败测试。** 捕获 `securitypolicyviolation`、`pageerror` 和非同源请求；对 Theme、Button、TextField、Badge、Checkbox，以及候选 Select、DropdownMenu、Dialog/Portal 实际打开、选择、Escape 关闭、恢复焦点和切换主题。

  ```js
  assert(violations.length === 0, 'adopted controls violate CSP');
  assert(await page.getByRole('combobox').inputValue() === 'risk', 'selection lost');
  assert(await page.getByRole('button', { name: '打开详情' }).evaluate(el => el === document.activeElement), 'focus not restored');
  assert(externalRequests.length === 0, 'external asset dependency');
  ```

  候选 Radix Select 若非原生控件，改用其可访问选中值断言；最终断言针对实际采用的控件，不锁 DOM 结构。夹具通过 Playwright 测试拦截替换已配对 `/assets` 的 HTML/JS/CSS，完整保留真实 Go 响应的 CSP/安全头；这只验证组件兼容，任务 6 再验证真正内嵌资源。
- [ ] **2. 执行红灯。** `make build`，安装锁定依赖与 Chromium/WebKit，运行 `INFRAFILE_BROWSER_SUITE=csp npm --prefix web run test:browser`；预期因夹具/交互尚未实现而失败，不能把缺少浏览器安装当作行为红灯。
- [ ] **3. 完成夹具和构建。** 按官方方式导入 Themes 外部 CSS，使用系统字体与静态主题类；不兼容的候选优先换外部 CSS 或原生 select/details/dialog，并保留键盘行为。仅测试需要采用的弹层行为；不为库中无关组件扩大 CSP。确需改策略时给出具体违规证据，回到已批准设计的 CSP 决策点，不继续页面迁移。
- [ ] **4. 执行绿灯。** `npm --prefix web run build:csp` 后分别用 `INFRAFILE_BROWSER_ENGINE=chromium`、`webkit` 运行上述 csp suite；要求两套通过、零脚本错误/外部请求，明确记录最终采用的控件。WebKit 不冒充真实 Safari 验收。
- [ ] **5. 提交。** 仅暂存本任务文件，`git commit -s -m "test(web): verify Radix controls under workbench CSP"`。

### Task 2：提供与单资产巡检一致的批量摘要

**Files:** 文件表中的 audit 输入/摘要文件、现有巡检与测试、`serve_queries.go`、`serve_summaries_test.go`、`docs/serve.md`。不新增 MCP 工具。

**Interfaces:**

- 保留 `Inspect(ctx context.Context, repo *ledger.Repo, opts InspectOptions) (*Inspection, error)`、`Context(...)`、内部 `inspect(...) (*inspector, error)` 和 `inspectWithHistory(...)` 原签名。
- 新增内部 `prepareInspection(ctx context.Context, repo *ledger.Repo, opts InspectOptions) (*inspectionInput, error)` 与 `inspectPrepared(ctx context.Context, in *inspectionInput, keys []string) (*inspector, error)`；前者不读取问题历史。`inspectionInput` 拥有一次读取/运行得到的 Input、findings、notices、快照/执行/基线/watch/config 及按 key 索引的记录/关系；每次投影新建可变报告与检查列表。
- 新增 `SummarizeAssets(ctx context.Context, repo *ledger.Repo, opts InspectOptions) (*AssetSummaries, error)`。`AssetSummaries` 字段为 `Version string`、`AsOf string`、`MaxAgeDays *int`、`Rows []AssetSummary`，JSON 为 `version/as_of/max_age_days/rows`。
- `AssetSummary` JSON：`key/kind/name: string`、`labels: map[string]string`、`archived/ok: bool`；`coverage` 仅含 `expected/checked/unknown/not_applicable: int`；`risk` 仅含 `confirmed/unknown/waived: int`；`execution: []InspectExecution`；`reason: {code,message: string}`；`evidence: []InspectEvidence`（按完整字段去重）。用专用计数结构，不能把完整 checks/findings/spec 混入行。
- `POST /api/asset-summaries` 直接复用 `inspect` 输入 schema，`key` 与 `labels` 互斥。在 `workbenchCall` 内先 `filepath.EvalSymlinks` 固定目录，再 `loadQueryRepo`、`mcpSelect(..., false)`，明确传递 key 列表；没有匹配时传空切片，显式不存在 key 保持错误。输出走 `queryValue` 及原有 HTTP 安全/大小检查。

- [ ] **1. 写等价与隔离测试。** 复用 `inspect_test.go` 的虚构夹具，新增 `TestAssetSummariesMatchSingleKey`、`TestPreparedInspectionIsolation`、`TestAssetSummariesFailures`、`TestWorkbenchAssetSummariesBoundary`、`TestWorkbenchAssetSummariesGeneration`。等价表覆盖风险+未知、错误账号、共享实例失败、stale/TTL 边界、归档、豁免、全 NA、无配置与不支持。

  ```go
  if row.OK != single.OK || row.Coverage.Unknown != single.Coverage.Unknown ||
      row.Risk.Confirmed != single.Risk.Confirmed || row.Risk.Unknown != single.Risk.Unknown {
      t.Fatal("summary changed single-key semantics")
  }
  if !reflect.DeepEqual(row.Execution, single.Execution) { t.Fatal("qualification differs") }
  if empty.OK || len(empty.Assets) != 0 { t.Fatal("empty keys became full scope") }
  if !reflect.DeepEqual(beforeFiles, afterFiles) { t.Fatal("read-only summary wrote files") }
  ```

  比较所有计数和证据字段，不仅示例三项；已有直接巡检期望测试继续独立验证算法。先保存 A 的结果、再求 B，断言 A 不变；准备后切换/移除源夹具，投影仍只读准备值。复用 `TestMCPPinsDirectoryGenerationPerCall` 的代次夹具，验证新 Web 分支没有绕过固定路径。
- [ ] **2. 执行红灯。** `go test ./internal/audit ./cmd/infrafile -run 'Test(AssetSummaries|PreparedInspection|WorkbenchAssetSummaries)' -count=1`；预期新接口缺失或断言失败。
- [ ] **3. 实现共享准备和投影。** 只移动现有规则与资格算法，不从全局报告过滤后重算。一次执行快照读取、Select/Prepare/Run、状态/基线/watch 准备和关系索引；按单 key 计算保留 `nil` 全局、非 nil 空范围的差别。保持 NA、豁免、notices 作用域、全部 execution 资格与 OK 原表达式。
- [ ] **4. 实现汇总与 Web 边界。** 汇总开始时严格 `ReadHistory` 一次，延续 `Inspect` 对损坏历史的拒绝；不在 `prepareInspection` 或内部 `inspect` 加入历史依赖，避免影响 `EvaluateIssues` 保存新历史。按 key 排序逐项投影，循环检查取消；任一错误整份失败。reason 取未豁免确认风险、未知检查/发现、执行资格不足的首要原因，否则解释通过/无检查/全部 NA；只用于摘要，不改变计数与 OK，完整问题仍由详情提供。
- [ ] **5. 执行绿灯。** 重跑本任务测试及 `go test ./internal/audit ./cmd/infrafile -run 'Test(Inspect|Context|Issue|History|MCP|Workbench)' -count=1`；加测重复 JSON 键、key+labels、任意路径/命令、401/403、秘密反射、4 MiB 超限、取消与损坏状态。测试确认 MCP 工具表不变，未进行 N 次全量规则运行。记录 100/1000 个虚构资产的汇总耗时作诊断，不引入机器相关的固定性能阈值。
- [ ] **6. 提交。** 仅暂存本任务文件，`git commit -s -m "feat(web): add scoped asset inspection summaries"`。

### Task 3：建立无损、可取消的前端读取与范围状态

**Files:** `web/src/contracts.ts`、`api.ts`、`api.test.ts`、`scope.ts`、`scope.test.ts`、`useQuery.ts`。

**Interfaces:**

- `contracts.ts` 导出任务 2 同名 `AssetSummaries/AssetSummary` DTO，以及当前 Go 契约的 `Inspection`、`AssetContext`、`ChangesResult`、`ExecutionsResult`、`ExecutionResult`、`ChangeStatus/ChangePreview/ChangeResult`；不改字段名。`AssetDirectoryEntry = Pick<AssetSummary, 'key'|'kind'|'name'|'labels'|'archived'>`；`ListResult = {version: string; records: AssetDirectoryEntry[]}` 用于摘要失败后的可选目录。labels 接受 `Record<string, string> | null`，搜索按空标签处理 null。历史 sequence/cursor 类型 `number | string`，动态记录用递归 `JsonValue`，不把计数与编辑数值强行转字符串。
- `Scope = { key: string; days: number; issue: string }`；`WorkbenchPage = 'assets' | 'inspections' | 'changes'`；`readScope(hash: string): Scope`、`pageURL(page: WorkbenchPage, scope: Scope): string`。默认 key/issue 为空、days=7，沿用现有跨页 key/issue 过滤规则。
- `parseReadJSON(text: string): JsonValue`；`query<T>(name: QueryName, body: object, signal: AbortSignal): Promise<T>`；`ApiError extends Error` 含 `status: number`。`QueryName` 是原工作台查询/编辑入口加 `asset-summaries` 的字面量联合，无任意 URL 参数。
- `useQuery<T>(requestKey: string | null, load: (signal: AbortSignal) => Promise<T>, onUnauthorized: () => void): QueryState<T>`；`QueryState<T>` 为 idle/loading/error/ready 判别联合，仅 ready 有 data。调用者用稳定 callback；requestKey 含查询名、范围和刷新序号。取消加请求序号，401 通知 App 清空整棵业务视图。
- 各页面使用 `PageProps = { scope: Scope; onScopeChange: (scope: Scope) => void; onUnauthorized: () => void }`。

- [ ] **1. 写失败测试。** `api.test.ts` 覆盖 CSRF、same-origin、redirect error、非 2xx、取消与无损数字；`scope.test.ts` 覆盖默认 7 天、0 天、非法/超安全整数 days、编码后的 key/issue 和跨页保留。

  ```ts
  expect(parseReadJSON('{"last_sequence":9007199254740993}')).toEqual({ last_sequence: '9007199254740993' });
  expect(() => readScope('#days=-1')).toThrow();
  expect(readScope('').days).toBe(7);
  await expect(query('context', { key: 'Server/a' }, abortedSignal)).rejects.toThrow();
  ```

  模拟缺少 reviver `context.source` 时，大整数必须报“无法无损显示”，普通数值仍工作；不要用已被取整的 Number 恢复字符串。
- [ ] **2. 执行红灯。** `npm --prefix web test -- src/api.test.ts src/scope.test.ts`；预期导入缺失或契约断言失败。
- [ ] **3. 实现接口。** 从旧 app.js 迁移现有安全数值处理与范围规则，补状态码错误和读取信号；CSRF 只读服务端 meta，不写 storage/URL/日志。`useQuery` 切换时同步使旧 data 不可见，卸载/切换取消；401 无论是否来自旧请求都触发会话失效，普通旧失败不能覆盖新结果。
- [ ] **4. 执行绿灯。** 重跑两文件测试及 `npm --prefix web run typecheck`；hook 的真实切换、旧成功/失败与 401 由任务 4 浏览器测试验证，不增加模拟 DOM 测试框架。
- [ ] **5. 提交。** `git commit -s -m "feat(web): add typed cancellable workbench queries"`，只暂存本任务文件。

### Task 4：实现三栏资产页与完整详情

**Files:** 文件表的 App/资产/共用巡检展示文件、浏览器脚本；消费任务 2、3 接口。

**Interfaces:** `App(): React.JSX.Element`；`AssetsPage(props: PageProps): React.JSX.Element`；`AssetDetail({assetKey, days, onNavigate, onUnauthorized})`，参数依次为 string、number、`(key: string) => void`、`() => void`；`InspectionSections({inspection, scope})` 消费 `Inspection/Scope`。`assetState.ts` 定义 `AssetFilters = { search: string; kind: string; archived: 'active'|'archived'|'all'; status: 'all'|'risk'|'pending'|'passed' }`、`filterAssets(rows, filters): AssetSummary[]`、`selectAsset(rows, selectedKey: string, cause: 'initial'|'filter'|'link'): { key: string; outside: boolean; changed: boolean }`。过滤结果的 rows 为有序数组，显式链接选择与用户筛选动作必须区分。

- [ ] **1. 写失败测试。** `assetState.test.ts` 验证默认在用、组合搜索、无标签/null、风险/待核实/通过顺序和同组 key 稳定；浏览器 `views` suite 加资产摘要请求计数、详情全部问题、关系跳转、取消/401、主题和窄屏返回。

  ```ts
  expect(selectAsset(filtered, 'Server/outside', 'link')).toEqual({ key: 'Server/outside', outside: true, changed: false });
  expect(selectAsset(filtered, 'Server/outside', 'filter').key).toBe(filtered[0].key);
  expect(selectAsset([], 'Server/a', 'filter').key).toBe('');
  // browser：先拦截 A，选中 B，再释放 A 的成功及失败各一次。
  assert(await page.getByRole('heading', { name: 'Server/b', exact: true }).count() === 1, 'late A replaced B');
  assert(summaryCalls === 1 && contextCalls === 1, 'initial view made per-asset requests');
  ```

  请求计数按一次已稳定导航计算；夹具资产数量增加时 context 次数不增长。401 后资产、详情、预览全部清空；摘要失败后旧状态消失，fallback 目录只标“状态未读取”且状态筛选不可用。
- [ ] **2. 执行红灯。** `npm --prefix web test -- src/assetState.test.ts`；构建新入口后运行 `INFRAFILE_BROWSER_PREVIEW=1 INFRAFILE_BROWSER_SUITE=views npm --prefix web run test:browser`。切换 Go 入口前沿用任务 1 测试拦截载入新 bundle，仅替换页面壳与静态资源、保留真实 API/会话/响应头；预期新三栏断言失败。
- [ ] **3. 实现 App 与列表。** 入口从 pathname 与服务端数据读取页面/台账/会话信息；组件局部状态组织，无通用路由。默认全部类型/在用，180/300/剩余宽度为起点；独立滚动、文字状态、风险与未知并列。首次无 key 才自动选第一项；filter 排除当前项时宣布换选；link/关系/归档深链保留目标并提示范围外。选中 key 只限制详情，不能顺带把摘要列表缩成一行；摘要使用独立的类型/标签范围，搜索不逐键发请求。手动刷新与 TTL 变更重读保存结果；响应上限提示用户缩小类型/标签范围。
- [ ] **4. 实现详情。** context 只读选中 key，完整展示未豁免风险与未知、checks、匹配观测、每条原始时间/时区/TTL/来源、完整脱敏登记、直接关系、历史与证据展开。只把 matched 的 values 显示为该资产观测事实；未匹配说明原因。context 超限显示缩小范围/CLI 入口，不自动无限拆请求或静默截断；列表和详情各自标明 as_of。
- [ ] **5. 实现适配与状态。** 普通上下键连续选择时焦点留列表，Enter/明确按钮进入详情，提供跳到内容与焦点返回；窄屏列表→详情→返回恢复位置和选择。系统/浅/深外观仅持久化主题，存储不可用时仍跟随系统。401 清空业务根并提供重连。缺少摘要、读取失败、空结果、范围外资产均有独立状态，不复用“通过”样式；未知检查与未知发现分开显示，不相加冒充唯一问题数。
- [ ] **6. 执行绿灯。** 本任务单测、typecheck、views suite 通过；检查 1440×900、1080×800、390×844 的亮/暗截图、键盘连续选择、长 key/JSON 不撑破布局。浏览器断言需覆盖 Review Focus 3、4；视觉检查记下实际浏览器，不以通过快照匹配替代人工可读性检查。
- [ ] **7. 提交。** `git commit -s -m "feat(web): add three-pane asset browser"`，只暂存本任务文件。

### Task 5：迁移巡检、变更与编辑恢复

**Files:** `InspectionsPage.tsx`、`ChangesPage.tsx`、`InventoryEditor.tsx`，连接 `App.tsx`，按需补 `contracts.ts`，扩展已有浏览器用例。

**Interfaces:** `InspectionsPage(props: PageProps)`、`ChangesPage(props: PageProps)` 返回 `React.JSX.Element`；`InventoryEditor({scope, onUnauthorized})` 使用 `Scope` 与 `() => void`。只调用既有 `inspect/changes/executions/execution/change/status/preview/apply/recover/show` 契约；`parseEditJSON(text: string): JsonValue` 放 `api.ts`，拒绝任何超安全范围整数字面量，沿用记录/数组与秘密字段限制。`InspectionSections` 复用任务 4 展示规则。

- [ ] **1. 写失败测试。** 保留原 `views/editViews/executionViews` 的行为断言，只调整可访问定位方式。新增“预览后改内容取消审阅”“提交回复丢失先查状态”与原始大整数精度断言。

  ```js
  assert(text.includes('9007199254740993'), 'history cursor rounded');
  assert(!await page.getByRole('checkbox', { name: '我已核对修改前后的完整内容' }).isChecked(), 'changed preview stayed reviewed');
  assert(applyCalls === 1 && statusCallsAfterDisconnect > 0, 'write was blindly retried');
  assert(await page.getByText('uncertain', { exact: false }).count() > 0, 'uncertain execution hidden');
  ```

  对不支持无损 JSON 的实际引擎，大整数场景应显示明确拒绝且没有取整数值，不能为了测试通过降低安全行为；支持的引擎必须完整显示。测试新空历史与损坏历史区别、pruned_through、陈旧预览拒绝和失败恢复。
- [ ] **2. 执行红灯。** `INFRAFILE_BROWSER_PREVIEW=1 INFRAFILE_BROWSER_SUITE=all npm --prefix web run test:browser` 使用新 bundle 测试桥接；预期未迁移页面/编辑流程断言失败。
- [ ] **3. 迁移展示。** 巡检保留 execution/coverage/risk/waiver/NA/notices/已保存历史，全局问题单列；变更保留完整留存边界、issue 过滤和回执/绑定计划。沿用原跨页链接，主题和侧栏复用 App，页面不重复创造健康判定。
- [ ] **4. 迁移编辑。** 按 status.enabled 显示，变更内容即清除旧预览/digest/审阅；预览、勾选、提交和恢复分别处理业务 status，不能只看 HTTP 200。回复不确定进入“先检查事务状态”的状态，恢复只使用服务端返回的事务 id；不重试 apply、不增云执行按钮、不把只读解析后的大整数字符串提交为新记录。
- [ ] **5. 执行绿灯。** typecheck、api 单测和 all suite 通过；保留双设备陈旧预览、Git hook 失败/重启恢复、秘密省略、只读文件不变及回执 uncertain 断言。验证所有原可用功能都有对应入口，删除任务 4 临时占位页面。
- [ ] **6. 提交。** `git commit -s -m "feat(web): migrate inspections and change workflows"`，只暂存本任务文件。

### Task 6：切换 Go 内嵌资源并完成分发验收

**Files:** 文件表中的 Go HTTP/模板/测试、旧 JS/CSS、Makefile/CI/release/GoReleaser、CONTRIBUTING、serve 文档和浏览器脚本。

**Interfaces:** `//go:embed web/serve.html web/dist/app.js web/dist/app.css`；HTTP 仍只服务 `/app.js`、`/app.css`，映射到内嵌 dist 固定路径。模板保留服务端配对/退出/revoke-all 表单和 CSRF meta，logged-in 根传递已转义的台账/会话元数据；由 React 布置导航与内容，无内联可执行 JSON。`make frontend` 执行锁文件安装+构建；`build/test/vet/check` 在 Go 编译前依赖 frontend；`check` 加 typecheck/单测，`browser-test` 使用已构建二进制运行浏览器脚本。

- [ ] **1. 写失败测试。** 更新 Go 页面测试为新 mount 与 module script 契约，增加 `TestWorkbenchEmbeddedAssets`；断言未登录仍可配对，静态资源 MIME 正确、CSP 原值不变、任意文件/source map/测试夹具不可取、业务 JSON 不混入模板。

  ```go
  if out.Header().Get("Content-Security-Policy") != wantCSP { t.Fatal("CSP expanded") }
  if !strings.Contains(html, `type="module"`) { t.Fatal("compiled entry missing") }
  if code := get("/app.js.map").Code; code != 401 && code != 404 { t.Fatal("source map exposed") }
  if get("/app.css").Header().Get("Content-Type") != "text/css; charset=utf-8" { t.Fatal("wrong MIME") }
  ```

- [ ] **2. 执行红灯。** 构建前端后运行 `go test ./cmd/infrafile -run 'TestWorkbench(EmbeddedAssets|Pages|HTML)' -count=1`；预期当前模板/资源路径不符合新契约。
- [ ] **3. 切换资源。** 修改 embed、固定映射和模板，正常构建仅 app.js/app.css；删除旧 app.js/app.css 及正常浏览器用例中的测试桥接，CSP 专用 fixture 仍不进入发行资源。Go 表单继续负责令牌处理；配对页用同一外部 CSS 统一视觉，不引入 React 登录认证实现。
- [ ] **4. 接入构建入口。** make targets 共用 frontend 前置条件；CI/发布先 setup Node 22 再构建。CI 安装锁定 Playwright 的 Chromium/WebKit 并跑真实 Go all suite；保留原 Go race、schema、Homebrew 检查。GoReleaser `before.hooks` 调用 `make frontend`，确保单独运行也不会缺资源。安装用 `npm ci`，缓存 key 包含锁文件；不把 node_modules/source map/CSP fixture 加入 archive。
- [ ] **5. 更新使用说明。** CONTRIBUTING 说明 Node 22.12+、首次锁文件安装、先 frontend 再直接 go test，以及浏览器安装/运行入口；serve.md 记录三栏、状态/范围外/失败含义、摘要契约、主题与既有编辑边界。写清未支持浏览器的大整数拒绝，不承诺新增检查能力。
- [ ] **6. 运行一次完整门禁。** `make check build`（同次 make 共享 frontend 前置步骤）、`INFRAFILE_BROWSER_ENGINE=chromium make browser-test`、`INFRAFILE_BROWSER_ENGINE=webkit make browser-test`；browser-test 复用刚构建的二进制。在完整 suite 中实际验证生产 CSP，而不是只验证早期夹具；禁止为通过测试删除原安全、恢复、只读和回执断言。
- [ ] **7. Mac 人工验收。** 使用当前 Mac 的真实 Safari 和 Chromium 检查亮/暗、长文本、键盘/焦点、菜单/对话框、窄屏返回、完整时间和重连；记录具体版本。WebKit 自动化结果与 Safari 分开记录。修复范围内失败后审查 diff；未跑的浏览器/辅助技术测试如实标注。
- [ ] **8. 本地提交。** `git commit -s -m "build(web): embed React workbench and verify distribution"`，只暂存本任务文件，供下一步从确切内容验证干净构建。本分支不自动合并、推送或发布。
- [ ] **9. 验证干净构建与包并交付。** 从含任务 6 的 HEAD 创建临时源码归档，无 node_modules/dist 起步执行锁定安装与 `make build`；将生成二进制复制到仅含虚构台账的临时目录启动。浏览器阻止所有非同源网络后验证配对和三页；检查二进制运行不启动 Node。已有 GoReleaser 可用时运行 `goreleaser release --snapshot --clean --skip=sign,publish` 并检查归档；否则明确列为未验证，不借此发布正式版本。出现失败则修复、验证受影响检查、追加签署提交，再验证该提交；完成后审查最终 diff，确认没有无关改动。

## 执行顺序与完成定义

按 1 → 2 → 3 → 4 → 5 → 6 执行。任务 1 是技术可行性门槛；失败时先在已批准范围内选择可用控件。任务 2 必须先证明摘要语义，再给列表上色。任务 6 才切换正式入口，之前的测试桥接不可进入正常验收。

推荐 **Native（同一会话逐项实施，结束时独立审查整个分支）**：六项任务紧密共享巡检契约、请求状态与构建入口，集中实施可减少重复阅读与接口偏差。也可选择逐任务独立实现/审查的 Subagent-driven，代价是更多上下文和审查轮次。

完成需同时满足已批准设计的七项验收、现有工作台功能回归、真实内嵌构建和最终 diff 审查。交付报告区分自动测试、真实浏览器人工检查、打包验证及未执行项目；计划完成不等于这些检查已经通过。
