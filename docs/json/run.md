# `infrafile run --json`

```sh
infrafile run --json --secret 'TOKEN=env://API_TOKEN' -- ./my-command
```

子进程结束后，stdout 返回：

```json
{
  "version": "v1",
  "exit_code": 0
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `version` | string | 输出结构版本，`v1` |
| `exit_code` | int | 子进程正常退出码；被信号终止、无正常退出码时为 1 |

CLI 退出码与 `exit_code` 相同。**子进程退出 2 时也会输出上述 JSON**；这不同于 infrafile 自身解析或启动失败的退出 2，后者 stdout 为空，stderr 为安全的文本错误。

JSON 模式下，子进程的 stdout 和 stderr 均经明文遮盖后发送到 infrafile 的 stderr，两个流的相对顺序不保证。普通模式保持两条流原来的去向，不额外打印成功摘要。

结果不包含凭据、引用、命令参数或完整环境。明文遮盖、环境继承和文件边界见[凭据指南](../secrets.md)。
