# examples/homelab

一份完整的**虚构**台账，同时用作上手演示、文档截图，以及端到端 / 黄金测试的输入。CI 会用它跑全量校验。

```bash
infrafile validate examples/homelab
infrafile show web1 examples/homelab
infrafile show App/blog examples/homelab --json
infrafile expiry examples/homelab --within 30d
infrafile ssh-config examples/homelab
infrafile sync example --dir examples/homelab
# 从本项目根目录执行；需要 python3
PATH="$PWD/examples/plugins:$PATH" infrafile sync --dir examples/homelab --json
```

内容：

配置中启用了内置 `example` 和外部 `example-python`，两者只返回虚构域名，不访问网络或凭据。同步生成 `state/example/home.yaml`、`state/example-python/home.yaml` 和集中时间文件 `state/_sync.yaml`；再次运行时，观测不变的快照不重写。未把示例目录加入 PATH 时，指定 `sync example` 仍可运行，运行全部插件则报告外部程序缺失。协议与安全边界见[插件指南](../../docs/plugins.md)。

[first-party.example.yaml](first-party.example.yaml) 提供九个第一方插件的完整配置示例，不会被默认加载。把需要的实例复制到自己的 `infrafile.yaml`，替换目标和凭据引用，再执行指定插件；云同步及探测会访问配置的服务。权限、字段和限制见[第一方插件指南](../../docs/first-party-plugins.md)。

| 类型 | 记录 |
| --- | --- |
| 账号 `accounts/` | `example-cloud-main`（云主机、CDN）、`cloudflare-main`（域名和 DNS） |
| 服务器 `servers/` | `web1`（云主机）、`db1`（家里的 NAS）、`old-vps`（已归档，演示 `metadata.archived`） |
| 域名 `domains/` | `example.com`、`example.net` |
| 应用 `apps/` | `blog`、`wiki` |
| 云资源 `resources/` | `blog-db`（自建在 db1 上的 PostgreSQL）、`assets-cdn` |
| 代码仓 `repos/` | `blog`、`wiki` |

凭据只写引用（`sops://`、`op://`、`env://`），示例里没有 `secrets.enc.yaml`、真实密钥或 1Password 账号。`wiki` 演示 `op://Example/wiki/api_token`。直接执行 `infrafile secret check examples/homelab` 会报告引用不可访问，这是预期结果；普通 `validate` 仍通过。

需要实际注入时，在自己的数据仓准备密文或外部引用，按[凭据指南](../../docs/secrets.md)运行。自动化测试使用临时生成的 age 密钥、合成凭据和模拟 `op` 验证全链路，示例仓不提供可复用私钥。

`blog` 和 `wiki` 同时存在于 App 与 Repo，查询时用 `App/blog` 或 `Repo/blog` 消除歧义。`old-vps` 可用 `show` 查看历史，但不会进入到期报告或 SSH 配置。`assets-cdn`、`web1` 和两个域名分别演示 Resource、Server、Domain 的到期日；`db1` 没有到期日，会被到期检查忽略。

到期输出随运行日期变化；黄金测试固定检查日期为 2026-12-01，此时 `assets-cdn` 已过期一天，`example.net` 恰好在 30 天边界，命令退出 1。

M6 的 `imports/ssh.conf` 和 `state/` 内四份快照也是手写的虚构输入，不是真实服务观测。可以先预览：

```sh
infrafile import ssh-config --dir examples/homelab --file examples/homelab/imports/ssh.conf
infrafile import state --dir examples/homelab --json
infrafile import state --dir examples/homelab --output /tmp/infrafile-review
```

SSH 的 `web1` 和快照里的 `example.com` 已有记录，会跳过；其余会生成待审草稿。DNS 观测只报告跳过，Cloudflare zone 生成 Resource。要体验写入，请先把整个 homelab 复制到临时目录，再对副本执行 `import ... --write` 和 `validate`；第二次导入不会改动已有文件。细节见[导入指南](../../docs/import.md)。

约定：只用文档保留地址段（`192.0.2.0/24`、`198.51.100.0/24`、`203.0.113.0/24`）、私有地址和 `example.com` 系列域名。
