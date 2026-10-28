# `--json` 输出

CLI 命令可以加全局选项 `--json`，把结果以 JSON 输出到标准输出。`mcp` 本身使用逐行 JSON-RPC，`--json` 不改变协议。下述 CLI 结构是给脚本和 agent 用的稳定契约：

- 顶层都有 `"version": "v1"`，表示输出结构的版本。新增字段不升版本；删除、改名或改变字段含义时升版本。
- 字段的确切输出由 `cmd/infrafile/testdata/golden/` 下的黄金测试锁定。
- 退出码与文本模式相同：0 成功，1 发现问题（`validate`、`expiry`、`audit`、`secret check`、`sync`、`apply`），2 用法错误或命令无法执行。`run` 保留子进程退出码。
- infrafile 自身退出 2 时标准输出为空，错误说明在标准错误里，是给人看的文本，不是 JSON。`run --json` 的子进程退出 2 是例外，仍返回执行结果。

| 命令 | 文档 |
| --- | --- |
| `infrafile validate` | [validate.md](validate.md) |
| `infrafile init` | [init.md](init.md) |
| `infrafile schema export` | [schema.md](schema.md) |
| `infrafile version` | [version.md](version.md) |
| `infrafile show` | [show.md](show.md) |
| `infrafile expiry` | [expiry.md](expiry.md) |
| `infrafile audit` | [audit.md](audit.md) |
| `infrafile ssh-config` | [ssh-config.md](ssh-config.md) |
| `infrafile secret check` | [secret.md](secret.md) |
| `infrafile run` | [run.md](run.md) |
| `infrafile sync` | [sync.md](sync.md) |
| `infrafile import ssh-config` / `import state` | [import.md](import.md) |
| `infrafile plan` / `plan show` | [plan.md](plan.md) |
| `infrafile apply` | [apply.md](apply.md) |
| `infrafile dnscontrol` | [dnscontrol.md](dnscontrol.md) |
| `infrafile gatus` | [gatus.md](gatus.md) |
| `infrafile agent guide` / `agent install` | [agent.md](agent.md) |
| `infrafile mcp`（协议工具结果） | [mcp.md](mcp.md) |

`show`、`expiry`、`audit`、`ssh-config` 先校验整个数据仓。校验未通过时退出 2 且 stdout 为空；请用 `validate` 获取具体问题。查询结果只包含凭据引用，不解析或解密引用。
