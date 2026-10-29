# 单用户工作台（v1.0 开发）

`infrafile serve` 固定一个数据仓，提供配对登录、资产/巡检/变更三个页面和十个离线读取 API，默认只读；显式 `--allow-edit` 可启用共享的台账预览、Git 提交和恢复。页面、样式和浏览器脚本嵌入二进制，无需 Node。云计划/执行回执索引仍待 T15，T12 相应集成验收保持开放。当前正式 v0.3.0 尚无此命令。

## 本机启动

```sh
infrafile serve --dir /absolute/path/to/ledger
```

默认仅监听 `127.0.0.1:8080`。在本机终端读取配对码，浏览器打开启动时打印的地址，在表单中粘贴。配对码随机生成，10 分钟内一次有效；成功配对后终端给出下一枚配对码，供同一用户的其他设备使用。未使用的过期码在下次打开登录页时更新。令牌不能放 URL 或普通日志。

后台启动必须显式指定仓外、尚不存在的配对文件；已有文件或符号链接拒绝覆盖。父目录应限制为私有访问：

```sh
pair_dir=$(mktemp -d)
infrafile serve --dir /absolute/path/to/ledger --pair-file "$pair_dir/code"
# 在另一终端读取 "$pair_dir/code" 后输入登录表单；不要把内容写进日志或 Git。
```

文件为 0600，配对更新时覆盖，正常退出后删除；文件身份或权限改变会拒绝发布新码。异常终止可能留下一枚失效码，确认进程已停止后可自行移除文件。不要将配对文件放入备份/同步目录。非交互输出且未指定文件时直接拒绝启动。

会话只保存在进程内，默认固定有效期 8 小时，可用 `--session-ttl` 设置大于 0、最长 24 小时。最多保留 8 个设备会话；到期后可以重新配对，超限拒绝新会话。退出撤销当前设备，页面还可撤销所有设备；重启使所有会话失效。Cookie 为 HttpOnly、SameSite=Strict、Path=/，HTTPS 模式另加 Secure。不做多用户、角色或持久登录。

## HTTPS 反向代理

非回环监听必须同时提供 HTTPS 外部地址和单个可信代理 IP；缺一、HTTP 地址、URL 中含用户信息/非根路径/查询参数均拒绝启动。监听地址必须使用 IP，不通过 DNS 推断信任边界。即使后端监听回环，远程访问也须显式启用代理模式：

```sh
infrafile serve --dir /absolute/path/to/ledger \
  --listen 127.0.0.1:8080 \
  --external-url https://infra.example.com \
  --trusted-proxy 127.0.0.1
```

代理应终止 TLS，将请求 Host 保持为精确配置的外部 Host（含已配置端口），覆盖 `X-Forwarded-Proto: https`；可提供同值的 `X-Forwarded-Host`。删除客户端提供的 Forwarded、X-Real-IP 及其余 X-Forwarded-* 头。后端仅接受指定代理 IP 的连接，并拒绝多值/冲突代理头；其他设备不得直连后端端口。代理 IP 代表受信任的整个代理，同一 OS 用户或可操纵该代理的进程不在隔离边界内。

本机模式拒绝所有代理头及非回环连接。两种模式都验证精确 Host、浏览器 Origin 和 Sec-Fetch-Site；不提供 CORS，不接受 URL 查询参数或绝对形式请求 URI。HTTPS 实际证书、代理部署和网络隔离需在使用方另行验证。

## 三页只读使用

登录后默认进入资产页。`/assets` 按主键、类型、在用/归档状态和标签搜索，点击关系可以跳到相关资产；详情并列登记期望与最近保存的观测。观测只从该资产明确绑定的实例和服务提取，以自然身份、SSH host 或绑定 hostname 匹配；身份重复、不匹配或服务不完整时不展示为该资产的事实。Cloudflare DNS 仅限 watch 对应 zone，不借用同账号其他 zone 的同名记录。匹配到保存值不代表本轮新鲜覆盖，旁列检查状态、代码、来源与原始时间。

`/inspections` 分开展示最近执行、固定覆盖分母、当前风险和最近一次显式保存的问题历史。可筛选已检/未知/不适用，不将零 findings、豁免或空历史显示为健康。点击问题可以在 `/changes` 查看同一 fingerprint/scope_hash 的保留变化；退出范围和 unknown 不显示为恢复。

`/changes` 交付问题变化时间线和台账修改。已清理前缀显式标注，返回现存保留事件；大于 JavaScript 安全整数的游标按原始数字文本显示，不取整。缺历史与损坏分别显示空态/失败，不保留上一次成功内容冒充当前结果。云计划/执行回执卡片待 T15，这部分完成后才关闭 T12。

页面支持键盘操作、明确标签、跳过导航、状态文字和窄屏。筛选范围与 TTL 放在 URL fragment（key/days/issue），配对/会话令牌始终不进 URL。默认视图证据有效期 7 天，可显式调整；切换页面保留范围，切换资产会取消旧查询。失效会话或读取失败时清空当前数据并显示重连/重选入口。

## 只读 API

登录后 `GET /api/session` 返回 `{version, csrf, expires_at}`，读取不延长会话。查询使用同源 `POST /api/<name>`、`Content-Type: application/json`、会话 Cookie 以及 `X-CSRF-Token`；Origin 必须精确匹配启动地址。请求体为 JSON 对象；字段/选择条件复用 [MCP 契约](json/mcp.md)，禁止额外字段、重复 JSON 键、原始地址/路径/命令。

| name | 输入 | 成功结果 |
| --- | --- | --- |
| list | 可选 key / labels / kind | MCP list 的 v1 记录摘要 |
| show | key 或唯一匹配的 labels，可选 kind | CLI show JSON，递归省略 secrets |
| expiry | 可选 key / labels / kind / within_days | 选定记录的到期报告 |
| audit | 可选 rules / max_age_days | CLI audit JSON，含显式保存的历史 |
| inspect | 可选 key / labels / kind / max_age_days | CLI inspect JSON；key 与 labels 互斥 |
| validate | `{}` | 记录数、问题数与代码计数，无敏感诊断原文 |
| schema | `{}` | 内嵌 JSON Schema bundle |
| capabilities | `{}` | 离线支持能力目录 |
| context | 必须 key，可选 max_age_days | `{version,inspection,observations}`；inspection 与相同输入 CLI inspect 一致，observations 含 check/plugin/instance/service/file/observed_at/match/values |
| changes | `{}` | `{version,present,last_sequence,pruned_through,changes}`；相当于 findings changes 从 pruned_through 读取全部保留事件，不接受任意游标或路径 |

原八个查询结果与 MCP structuredContent 一致；context/changes 为工作台新增只读入口，MCP 九工具保持。context 的 match 为 matched / not_observed / incomplete / ambiguous / identity_mismatch / unsupported，只有 matched 的 values 用作此资产的保存事实；当前证据资格仍由 inspection 决定。成功生成报告返回 HTTP 200，即使 `ok=false` 或存在 unknown；不能把 HTTP 成功当作健康通过。资产与巡检查询重新校验当前台账；changes 沿用历史救援契约，只严格读取已保存历史，不要求当前 inventory 有效。查询不运行插件、联网、解析凭据或写台账/历史。没有云端 plan/apply、sync、import、run、secret 或任意文件下载 API。

## 台账编辑（显式启用）

先按 [change 契约](json/change.md)启用 `storageVersion: v1`、提交 `/.infrafile/` 忽略规则，并让数据 Git 仓处于干净普通分支；然后启动 `infrafile serve --dir /absolute/path/to/ledger --allow-edit`。重启仍需重新配对。未完成事务不阻止重启工作台，但会阻止台账查询；变更页显示恢复入口。

选中资产后进入变更页，会载入省略 secrets 的完整记录 JSON；也可填写新记录或记录数组，同批调整引用。先校验并查看修改前后字段，勾选已审阅后再确认提交。服务端绑定完整内容/HEAD/索引/配置摘要，双设备使用旧预览时拒绝保存。保存重新序列化完整记录，注释与排版会变化；Web 展示字段差异和绑定摘要，需看原始字节 diff 时使用 CLI。超出浏览器安全整数精度的编辑须用 CLI。

`/api/change/status` 接受 `{}`，返回 `{version,enabled,transaction}`；默认 `enabled=false`。启用后另有同源 POST `preview`（v1 records 请求）、`apply`（`{request,digest}`）、`recover`（`{id}`），共享认证、CSRF、64 KiB 输入、超时和并发限制。预览返回 `{version,ok,head,digest,problems,files}`，每文件含 key/path/existed、前后内容 SHA-256 及已省略 secrets 的 before/after 对象；不回传原始字节或本机绝对路径。浏览器禁止提交秘密字段，已有引用由服务端保留，修改引用须走 CLI。

保存与恢复返回 [change 结果](json/change.md)。HTTP 200 可能是 rejected 或 recovery_required，必须核对 status。请求中断或超时后先检查事务状态，不盲目重试；恢复仅触及匹配本事务哈希的文件，外部修改会使恢复停止。工作台不做云端执行或自动 push，仓库原有提交钩子仍运行。

`POST /logout` 和 `/revoke-all` 使用页面表单的会话 CSRF，分别撤销当前/全部设备。`POST /pair` 使用登录表单独立的 CSRF 与配对 Cookie；两者都要求同源 Origin。配对码每分钟最多尝试 5 次，不按 IP 建立无限增长状态。Cookie 或表单过期后重新打开登录页。

所有响应 no-store，HTML 由 html/template 转义；浏览器动态内容仅用 textContent/DOM 构造，不执行台账文字。CSP 仅允许同源分发的脚本、样式和请求，禁止行内脚本、嵌入框架及外部资源。请求体最多 64 KiB、响应 JSON 最多 4 MiB、并发最多 4；查询最多 30 秒且不超过会话剩余时间。HTTP 请求头上限配置为 8 KiB，读取头/请求分别限 5/10 秒，写入限 35 秒。根目录身份变化、符号链接记录/状态及不安全动态结果会被拒绝，不暴露失败原文。

错误只返回 `{version:"v1", ok:false, error:<固定状态文本>}`：400 参数/表单错误，401 未登录或会话失效，403 来源/CSRF/配对拒绝，404 未提供入口，413 请求体过大，415 媒体类型错误，422 台账/查询/结果拒绝，429 尝试/并发/会话上限，503 根目录或配对发布失败，504 查询超时/取消。不返回请求、路径、配对码、Cookie 或云端错误原文；退出 0 表示服务正常停止，不代表台账健康。

## 浏览器验收

HTTP/会话/路径/秘密边界随 `make check` 运行。`scripts/test_serve_browser.cjs` 另用真实二进制、虚构示例和 Chromium 验证表单 Origin、两设备独立会话、单独/全部撤销、过期、重启及 HTTPS 代理 Cookie；只读数据逐文件不变。另建虚构 Git 仓验收 CLI/Web 字节哈希一致、审阅后提交、第二设备旧预览拒绝、钩子失败后的重启恢复及窄屏键盘操作。两部分均核对日志无令牌和配对文件清理。

验收机需预先安装 Node 22+、Playwright/Chromium 和 OpenSSL；脚本不下载依赖，不访问云账号。默认读取 `bin/infrafile` 和可解析的 `playwright` 包；可分别用 `INFRAFILE_BINARY`、`INFRAFILE_PLAYWRIGHT_MODULE`（绝对模块路径）、`INFRAFILE_BROWSER_EXECUTABLE` 显式指定。直接运行 `node scripts/test_serve_browser.cjs`，回执和截图保留在新建的 0700 临时目录，可用 `INFRAFILE_BROWSER_OUTPUT_PARENT` 指定已有私有父目录；失败材料保留，测试 TLS 私钥和配对文件在退出时清理。回环 HTTP 表单使用 `Referrer-Policy: same-origin`，避免 no-referrer 导致浏览器发送 Origin:null；严格 Origin 校验保持。
