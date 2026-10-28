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
PATH="$PWD/examples/plugins:$PATH" infrafile sync example-python --dir examples/homelab --json
infrafile audit examples/homelab --max-age 7d
```

内容：

配置中启用了内置 `example` 和外部 `example-python`，两者只返回虚构域名，不访问网络或凭据。同步生成 `state/example/home.yaml`、`state/example-python/home.yaml`，并在集中时间文件 `state/_sync.yaml` 记下观测时间；再次运行时，观测不变的快照不重写。外部插件需要把示例目录加入 PATH，否则报告外部程序缺失。

配置还启用了 `aliyun.home` 和 `cloudflare.home`，凭据是虚构的 `env://INFRAFILE_EXAMPLE_*` 引用，只为让 `state/` 下对应的手写快照参与审计。不加插件名运行 `sync` 会因这两个实例的凭据引用无法解析而退出 1，不会访问网络，已有快照和观测时间保持不变。协议与安全边界见[插件指南](../../docs/plugins.md)。

[first-party.example.yaml](first-party.example.yaml) 提供九个第一方插件的完整配置示例，不会被默认加载。把需要的实例复制到自己的 `infrafile.yaml`，替换目标和凭据引用，再执行指定插件；云同步及探测会访问配置的服务。权限、字段和限制见[第一方插件指南](../../docs/first-party-plugins.md)。

| 类型 | 记录 |
| --- | --- |
| 账号 `accounts/` | `example-cloud-main`（云主机、CDN）、`cloudflare-main`（域名和 DNS） |
| 服务器 `servers/` | `web1`（云主机）、`db1`（家里的 NAS）、`old-vps`（已归档，演示 `metadata.archived`） |
| 域名 `domains/` | `example.com`、`example.net`（直连源站，演示带 `until` 的 `metadata.waivers`） |
| 应用 `apps/` | `blog`、`wiki` |
| 云资源 `resources/` | `blog-db`（自建在 db1 上的 PostgreSQL）、`assets-cdn` |
| 代码仓 `repos/` | `blog`、`wiki` |

凭据只写引用（`sops://`、`op://`、`env://`），示例里没有 `secrets.enc.yaml`、真实密钥或 1Password 账号。`wiki` 演示 `op://Example/wiki/api_token`。直接执行 `infrafile secret check examples/homelab` 会报告引用不可访问，这是预期结果；普通 `validate` 仍通过。

需要实际注入时，在自己的数据仓准备密文或外部引用，按[凭据指南](../../docs/secrets.md)运行。自动化测试使用临时生成的 age 密钥、合成凭据和模拟 `op` 验证全链路，示例仓不提供可复用私钥。

`blog` 和 `wiki` 同时存在于 App 与 Repo，查询时用 `App/blog` 或 `Repo/blog` 消除歧义。`old-vps` 可用 `show` 查看历史，但不会进入到期报告或 SSH 配置。`assets-cdn`、`web1` 和两个域名分别演示 Resource、Server、Domain 的到期日；`db1` 没有到期日，会被到期检查忽略。

到期输出随运行日期变化；黄金测试固定检查日期为 2026-12-01，此时 `assets-cdn` 已过期一天，`example.net` 恰好在 30 天边界，命令退出 1。

M6 的 `imports/ssh.conf`、`state/` 内四份快照和 `state/_sync.yaml` 里的观测时间也是手写的虚构输入，不是真实服务观测。可以先预览：

```sh
infrafile import ssh-config --dir examples/homelab --file examples/homelab/imports/ssh.conf
infrafile import state --dir examples/homelab --json
infrafile import state --dir examples/homelab --output /tmp/infrafile-review
```

SSH 的 `web1` 和快照里的 `example.com` 已有记录，会跳过；其余会生成待审草稿。DNS 观测只报告跳过，Cloudflare zone 生成 Resource。要体验写入，请先把整个 homelab 复制到临时目录，再对副本执行 `import ... --write` 和 `validate`；第二次导入不会改动已有文件。细节见[导入指南](../../docs/import.md)。

审计对照台账和已启用实例的快照，只读、不访问网络：

```sh
infrafile audit examples/homelab
infrafile audit examples/homelab --max-age 45d --json
```

默认运行 `cdn_drift`、`origin_exposure`、`orphan`、`zombie`：根示例的 `example.org` 与阿里云 CDN 尚未登记，报告孤儿；Cloudflare 的 example.net 已有同名 Domain 覆盖，不报孤儿。example.net 的未代理地址与 web1 相同，报告源站暴露，由该 Domain 自带的有期豁免压下；阿里云 CDN 状态为 online，不报停用。`stale_state` 按 `state/_sync.yaml` 判断快照是否过旧：黄金测试的检查时刻是 2026-12-01，`--rule stale_state --max-age 45d` 下 `cloudflare/home` 已 91 天未更新，命令退出 1；`aliyun/home` 是 38 天前，未超限。`dns/blog` 和 `ssh/observed-web` 对应的实例未启用，只给出忽略提示。

[lifecycle/](lifecycle/) 是独立的虚构数据仓，演示两条生命周期规则的命中、不命中和精确豁免，不改变根示例的导入草稿：

```sh
infrafile validate examples/homelab/lifecycle
infrafile audit examples/homelab/lifecycle --json
infrafile audit examples/homelab/lifecycle --rule orphan
infrafile audit examples/homelab/lifecycle --rule zombie
```

example.com 在用，不报告；old.example.com 已归档仍被观测，报告 zombie；waived.example.com 同样命中但由归档记录的 metadata.waivers 豁免；orphan.example.net 未登记，报告 orphan；ignored.example.net 仅由 audit.ignore 的精确 Domain match 豁免。豁免到 2026-12-31 当天有效，之后仍报告发现但不再豁免。stale_state 的实例级 ignore 保持兼容，生命周期 ignore 必须指定完整 match；字段、身份及输出见[审计 JSON 契约](../../docs/json/audit.md)。

M7 在 example.com 的 `spec.dns` 中提供显式 DNS 期望值，可离线生成配置：

```sh
infrafile dnscontrol examples/homelab
infrafile dnscontrol examples/homelab --json
```

输出固定使用 NO_PURGE，不创建凭据文件、不访问 DNS。`control/edge.yaml` 是带虚构 Cloudflare ID 的控制目标草稿，不在 inventory 中，也不会启用真实插件。实际生成 plan 需要将草稿和实例配置放到自己的数据仓并填写自己的 ID；不要对示例 ID 执行云操作。自动化测试用模拟外部插件和 HTTP 回放验证 plan → apply → 回读，包括过期拒绝和回读不符。详见[受控写指南](../../docs/control.md)。

v0.2 的 `infrafile gatus examples/homelab` 为 example.com 根绑定生成一个 HTTPS 端点；其他绑定未声明 monitor，不会自动加入监控。输出只含 endpoints，告警与存储配置由使用者维护，详见 [Gatus 契约](../../docs/json/gatus.md)。

M8 可以让 MCP 客户端启动以下命令，离线体验查询；stdout 是协议流：

```sh
infrafile mcp --dir examples/homelab
infrafile agent guide
```

通过 `infrafile_list` 传 `{"kind":"Server","labels":{"role":"web"}}` 找到 Server/web1，再用 `infrafile_show` 传 `{"key":"Server/web1"}` 查看。缺少的示例密钥不会影响查询，返回值省略 secrets。Server/old-vps 可查询；plan 不接受归档目标。示例 web1 的 address 是 IP，不能作为 MCP 改回源的合法主机名。

`infrafile_audit` 传 `{"rules":["stale_state"],"max_age_days":45}` 可体验同一份离线审计；结果随检查日期变化。未豁免偏差返回 ok=false，工具调用仍成功。

安装体验请在临时项目目录运行 `infrafile agent install codex --dir <临时目录>` 或 `claude`；安装内容与 `agent guide` 一致，重复运行不改文件。具体接入与七个工具契约见[Agent 指南](../../docs/agent.md)。

约定：只用文档保留地址段（`192.0.2.0/24`、`198.51.100.0/24`、`203.0.113.0/24`）、私有地址和 `example.com` 系列域名。
