# 单用户工作台（v1.0 开发）

`infrafile serve` 固定读取一个数据仓，提供配对登录和八个离线只读 API；页面和样式嵌入发行二进制，无需 Node。资产、巡检、变更三页由 T12 继续交付，台账编辑由 T13 交付。当前正式 v0.3.0 尚无此命令。

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

非回环监听必须同时提供 HTTPS 外部地址和单个可信代理 IP；缺一、HTTP 地址、URL 中含用户信息/路径/查询参数均拒绝启动。监听地址必须使用 IP，不通过 DNS 推断信任边界。即使后端监听回环，远程访问也须显式启用代理模式：

```sh
infrafile serve --dir /absolute/path/to/ledger \
  --listen 127.0.0.1:8080 \
  --external-url https://infra.example.com \
  --trusted-proxy 127.0.0.1
```

代理应终止 TLS，将请求 Host 保持为精确配置的外部 Host（含已配置端口），覆盖 `X-Forwarded-Proto: https`；可提供同值的 `X-Forwarded-Host`。删除客户端提供的 Forwarded、X-Real-IP 及其余 X-Forwarded-* 头。后端仅接受指定代理 IP 的连接，并拒绝多值/冲突代理头；其他设备不得直连后端端口。代理 IP 代表受信任的整个代理，同一 OS 用户或可操纵该代理的进程不在隔离边界内。

本机模式拒绝所有代理头及非回环连接。两种模式都验证精确 Host、浏览器 Origin 和 Sec-Fetch-Site；不提供 CORS，不接受 URL 查询参数或绝对形式请求 URI。HTTPS 实际证书、代理部署和网络隔离需在使用方另行验证。

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

结果与 MCP structuredContent 一致。成功生成报告返回 HTTP 200，即使 `ok=false` 或存在 unknown；不能把 HTTP 成功当作健康通过。查询重新读取并校验固定仓库，不运行插件、联网、解析凭据或写台账/历史。没有 plan、apply、sync、import、run、secret 或任意文件下载 API；这不是远程云端执行入口。

`POST /logout` 和 `/revoke-all` 使用页面表单的会话 CSRF，分别撤销当前/全部设备。`POST /pair` 使用登录表单独立的 CSRF 与配对 Cookie；两者都要求同源 Origin。配对码每分钟最多尝试 5 次，不按 IP 建立无限增长状态。Cookie 或表单过期后重新打开登录页。

所有响应 no-store，HTML 由 html/template 转义；CSP 禁止脚本、嵌入框架及外部资源。请求体最多 64 KiB、响应 JSON 最多 4 MiB、并发最多 4；查询最多 30 秒且不超过会话剩余时间。HTTP 请求头上限配置为 8 KiB，读取头/请求分别限 5/10 秒，写入限 35 秒。根目录身份变化、符号链接记录/状态及不安全动态结果会被拒绝，不暴露失败原文。

错误只返回 `{version:"v1", ok:false, error:<固定状态文本>}`：400 参数/表单错误，401 未登录或会话失效，403 来源/CSRF/配对拒绝，404 未提供入口，413 请求体过大，415 媒体类型错误，422 台账/查询/结果拒绝，429 尝试/并发/会话上限，503 根目录或配对发布失败，504 查询超时/取消。不返回请求、路径、配对码、Cookie 或云端错误原文；退出 0 表示服务正常停止，不代表台账健康。
