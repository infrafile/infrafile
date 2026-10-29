# `infrafile secret check --json`

```json
{
  "version": "v1",
  "ok": false,
  "checked": 3,
  "problems": [
    {
      "code": "not-found",
      "file": "inventory/accounts/example.yaml",
      "line": 8,
      "path": "spec.secrets.env",
      "message": "环境变量不存在"
    }
  ]
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `version` | string | 输出结构版本，`v1` |
| `ok` | bool | 所有引用能定位时为 `true` |
| `checked` | int | 检查的 `spec.secrets` 条目数，包含归档记录；重复引用按出现次数计算 |
| `problems` | array | 问题列表，空时为 `[]`；与 validate 一样按文件、行号、代码、字段路径排序 |

问题使用 `code`、`file`、`line`、`path`、`message` 结构，不包含凭据值或后端原始错误。`message` 供人阅读，程序应判断 `code`：

| code | 含义 |
| --- | --- |
| `invalid-ref` | URI 语法或路径不合法 |
| `not-found` | 环境变量、字段不存在，或 op 没有返回字段 ID |
| `unavailable` | 文件无法读取、越界，或 op 未安装、未登录、无权访问 |
| `invalid-file` | YAML 无效、多文档、缺少 SOPS age/MAC 元数据，或密文不是最多 4 MiB 的普通文件 |
| `invalid-value` | 目标字段不是 SOPS 加密字符串 |
| `unsupported` | SOPS 文件使用了当前不支持的密钥后端或分组 |
| `cancelled` | 操作超时或取消 |

SOPS 检查与解析共用文件边界：拒绝符号链接及非普通文件，打开前后核对文件身份并限量读取。检查不证明密钥或 MAC 可用。doctor 的默认禁网检查与现有 secret check 的 op 元数据查询不同，见[分层预检](../doctor.md)。

退出码为 0（全部能定位）或 1（存在问题）。参数错误、数据仓不存在或结构校验失败时退出 2，stdout 为空，stderr 为文本。后端检查范围和解密边界见[凭据指南](../secrets.md)。
