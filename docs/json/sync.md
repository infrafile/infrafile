# `infrafile sync [插件] --json`

可选 `--dir <数据仓>`（默认当前目录）、`--timeout 30s`。只运行 `infrafile.yaml` 中启用的实例，先校验整个数据仓。协议与存储规则见[插件指南](../plugins.md)。

```json
{
  "version": "v1",
  "ok": true,
  "results": [
    {
      "plugin": "example",
      "instance": "home",
      "file": "state/example/home.yaml",
      "ok": true,
      "changed": true
    }
  ]
}
```

- `results` 按插件名、实例名排序；空配置为 `[]`。
- `file` 是预期快照的仓内相对路径；失败时文件可能不存在，也可能保留上次成功版本。
- `changed` 表示本次是否替换了快照字节，不包含 `_sync.yaml` 或 `.sync-status.yaml` 的变化。
- 成功实例没有 `code`、`message`。失败实例有固定的错误代码和说明，不携带插件日志、RPC 错误原文或快照内容。
- `code`：`not-found` 找不到程序、`start` 启动失败、`timeout` 超时／取消、`protocol` 消息不合法／进程中断、`capability` 未声明 sync、`plugin` 执行错误、`sensitive` 敏感数据被拒、`write` 快照或时间写入失败。
- 第一方插件还可能返回：`config` 配置字段／服务范围不支持，`credential` 引用无法解析或为空，`http` 请求失败／HTTP 状态错误／响应过大，`provider` 云 API 返回业务错误，`probe` DNS／TLS／WHOIS／SSH 探测失败。错误不包含凭据、原始响应或子进程日志。
- 全部成功时退出 0；部分或全部实例失败时退出 1，仍返回完整的已执行结果。取消后不启动剩余实例，返回已执行结果、`ok: false` 和 `cancelled: true`，退出 1；未取消时省略 `cancelled`。
- 参数、数据仓、锁、公共状态文件无法使用，或实例开始回执无法保存时退出 2，stdout 为空，错误文本在 stderr。此前已完成的实例可能已保存快照，应核对 state 文件。
- 不返回快照内容；快照由 YAML 文件承载。若快照成功保存而同步时间保存失败，该项 `ok: false`，`changed` 仍如实反映已发生的快照替换。
- v0.3 开发版为已执行实例保存 `.sync-status.yaml`（running / succeeded / failed），旧成功时间格式不变。结束回执保存失败时当前项为 `code: write`，保留真实 changed，并停止后续实例；结果只列已执行的实例。取消时仍尝试保存当前失败回执，未执行实例不记成功。执行回执不证明权限范围或资源完整，详见[状态文件](../plugins.md#状态文件)。
