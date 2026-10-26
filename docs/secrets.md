# 凭据引用与运行命令

凭据引用放在记录的 `spec.secrets` 中，台账不存明文值。M3 支持以下三种后端：

| 引用 | 定位方式 | `secret check` 检查内容 |
| --- | --- | --- |
| `sops://secrets.enc.yaml#database.password` | 数据仓内的 SOPS YAML 文件、以点分隔的对象字段路径 | 文件可读取、包含 age/MAC 元数据、目标字段是加密字符串；不加载私钥、不解密 |
| `op://Vault/Item/password` | 1Password vault、item、field；也支持 `op://Vault/Item/section/field` | 调用 `op read --no-newline <ref>?attribute=id`，只请求字段 ID，不请求凭据值 |
| `env://DATABASE_PASSWORD` | 当前进程的环境变量 | 变量存在；已设置为空字符串也算存在 |

SOPS 使用项目内嵌的 SOPS / age 库，无须安装 `sops` 可执行程序。当前依赖要求 Go 1.26.0 或更新版本构建。只支持单组 age 加密的 YAML 和字符串字段；不调用 KMS、PGP，不支持多组密钥。后端限制以 SOPS 实际解析出的密钥类型为准，不依赖元数据键名大小写。字段路径不支持数组下标或含点的键名。文件必须位于数据仓内，`..`、绝对路径和越界符号链接会被拒绝。

1Password 使用本机已安装并登录的 `op`，每次操作超时为 30 秒。名字可以包含空格，请在 shell 中引用整个 URI。M3 不接受 URI 查询参数或文件附件。检查不会把字段值交给 infrafile；1Password 内部如何处理 vault 加密由其 CLI 实现决定。字段属性和命令用法见 [1Password 官方文档](https://developer.1password.com/docs/cli/secret-references/)。

## 检查引用

```sh
infrafile secret check /path/to/my-infra
infrafile secret check /path/to/my-infra --json
```

先校验整个数据仓，再检查所有记录（含归档）的 `spec.secrets`。不扫描任意扩展字段。退出码：0 全部能定位；1 有缺失、不可访问或不支持的引用；2 参数错误或数据仓无法检查。

检查通过只证明引用能定位。SOPS 的密钥可用性、密文认证和 MAC 完整性在真正解析时检查，不能用 `secret check` 的成功代替解密验证。具体字段见 [JSON 契约](json/secret.md)。

## 注入子进程

```sh
infrafile run --dir /path/to/my-infra \
  --secret 'DATABASE_PASSWORD=sops://secrets.enc.yaml#database.password' \
  --secret 'API_TOKEN=op://Example/API/token' \
  -- ./my-command --mode check
```

`--secret` 可重复指定，变量名采用 `[A-Za-z_][A-Za-z0-9_]*`，同名变量不能重复。必须使用 `--` 分隔子命令；它后面的参数原样传递，不经过隐式 shell。`--dir` 只确定 SOPS 文件根目录，不改变子进程的工作目录。仅使用 env/op 时不需要数据仓。

所有凭据解析成功后才启动子进程。子进程继承当前环境，并覆盖显式指定的变量；父进程环境保持不变。SOPS age 密钥沿用上游配置，如 `SOPS_AGE_KEY_FILE`、`SOPS_AGE_KEY` 和标准配置目录。不要在 shell 命令字面量中放私钥或凭据值。

M5 将 age 密钥加载接入取消信号：支持原生 X25519 / Hybrid 身份、未加密的 SSH 身份，以及 `SOPS_AGE_KEY_CMD` / `SOPS_AGE_SSH_PRIVATE_KEY_CMD`。命令按上游 shlex 规则拆分参数、直接执行，收到 `SOPS_AGE_RECIPIENT`；不经过隐式 shell，不输出命令的 stdout / stderr。取消会终止并回收直接子进程；插件调用同时遵守实例超时。命令失败仍可尝试其他已配置静态密钥；取消则立即失败。身份文件的口令交互、加密身份文件和外部 age 插件不在非交互支持范围内，避免绕过取消边界。数据密钥解密、字段解密和 MAC 完整性验证仍由原 SOPS / age 库完成。

infrafile 不将明文放入命令参数、普通文件或自身诊断。子进程 stdout/stderr 中与已注入值完全匹配的内容会替换为 `[REDACTED]`，支持跨写入分块和多行值。输出仍是流式的；可能的凭据前缀会暂存到后续写入或进程结束。

遮盖只处理完整原值，不是子进程沙箱，也不保证遮盖编码、拆分到不同输出流或变换后的值。子命令本身仍可写文件或访问网络，应运行受信任的程序。为遮盖输出，子进程输出使用管道，不提供 PTY。

正常退出时保留子进程退出码；操作被取消或被信号终止且无法取得正常退出码时返回 1。`run` 在 macOS/Linux 使用独立进程组，在 Windows 使用 Job Object 管理子命令及其后代；取消或命令结束时清理仍存活的受管进程。直接子进程退出后，最多等待 1 秒让输出管道关闭；超时按通信失败返回 2，并清理后代。macOS/Linux 上若 stdin 是当前前台终端，会交给子命令并在结束后恢复；自行创建新会话或进程组的后台进程不在清理范围内。解析或启动失败返回 2。`--json` 模式把遮盖后的子进程日志送往 stderr，stdout 只输出执行结果，见 [run JSON 契约](json/run.md)。

测试会临时生成 age 私钥和 SOPS 密文，使用隔离的密钥发现目录；私钥和明文留在内存中，仓库内不保存可复用私钥。1Password 测试用模拟 `op` 验证命令和错误边界，不依赖真实账号。
