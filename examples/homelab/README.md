# examples/homelab

一份完整的**虚构**台账，同时用作上手演示、文档截图，以及端到端 / 黄金测试的输入。CI 会用它跑全量校验。

```bash
infrafile validate examples/homelab
infrafile show web1 examples/homelab
infrafile show App/blog examples/homelab --json
infrafile expiry examples/homelab --within 30d
infrafile ssh-config examples/homelab
```

内容：

| 类型 | 记录 |
| --- | --- |
| 账号 `accounts/` | `example-cloud-main`（云主机、CDN）、`cloudflare-main`（域名和 DNS） |
| 服务器 `servers/` | `web1`（云主机）、`db1`（家里的 NAS）、`old-vps`（已归档，演示 `metadata.archived`） |
| 域名 `domains/` | `example.com`、`example.net` |
| 应用 `apps/` | `blog`、`wiki` |
| 云资源 `resources/` | `blog-db`（自建在 db1 上的 PostgreSQL）、`assets-cdn` |
| 代码仓 `repos/` | `blog`、`wiki` |

凭据只写引用（`sops://`、`env://`），示例里没有 `secrets.enc.yaml`，引用指向的值不存在。

`blog` 和 `wiki` 同时存在于 App 与 Repo，查询时用 `App/blog` 或 `Repo/blog` 消除歧义。`old-vps` 可用 `show` 查看历史，但不会进入到期报告或 SSH 配置。`assets-cdn`、`web1` 和两个域名分别演示 Resource、Server、Domain 的到期日；`db1` 没有到期日，会被到期检查忽略。

到期输出随运行日期变化；黄金测试固定检查日期为 2026-12-01，此时 `assets-cdn` 已过期一天，`example.net` 恰好在 30 天边界，命令退出 1。

约定：只用文档保留地址段（`192.0.2.0/24`、`198.51.100.0/24`、`203.0.113.0/24`）、私有地址和 `example.com` 系列域名。
