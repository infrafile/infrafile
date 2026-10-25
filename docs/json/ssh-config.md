# `infrafile ssh-config --json`

用法：`infrafile ssh-config [目录] --json`，目录默认为当前目录。

```json
{
  "version": "v1",
  "hosts": [
    {"name": "db1", "hostname": "198.51.100.20", "user": "admin", "port": 2222},
    {"name": "web1", "hostname": "203.0.113.10", "user": "deploy", "port": 22}
  ]
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `version` | string | 输出结构版本，`v1` |
| `hosts` | array | 在用 Server，按名称排序；没有时为 `[]` |
| `hosts[].name` | string | `metadata.name`，对应 SSH 的 `Host` |
| `hosts[].hostname` | string | `spec.address`，对应 `HostName` |
| `hosts[].user` | string | 可选，来自 `spec.ssh.user`，未填写时省略 |
| `hosts[].port` | int | 可选，来自 `spec.ssh.port`，未填写时省略 |

文本模式输出相同字段组成的 SSH config 片段；缺少 user、port 时不补默认值，由 SSH 自身配置决定。没有 Server 时文本输出为空。

仅接受可安全输出的字面量：地址为 ASCII 主机名、IPv4 或 IPv6（字符限字母、数字、点、连字符、冒号；首字符为字母、数字或冒号）；用户名使用字母、数字、下划线、点、`@`、连字符，可带末尾 `$`，首字符为字母、数字或下划线。空用户、换行、引号、通配符、SSH token 展开及其他字符会被拒绝。此限制同样适用于 JSON 模式，出错时不会输出部分配置。

成功退出 0。参数错误、数据仓校验失败或字段不能安全渲染时退出 2，stdout 为空，stderr 为文本说明。

命令不修改 `~/.ssh/config`，不连接服务器、不读取凭据。文本输出经审阅后可另存为 SSH Include 片段。
