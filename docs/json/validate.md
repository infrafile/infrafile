# `infrafile validate --json`

```json
{
  "version": "v1",
  "ok": false,
  "records": 4,
  "problems": [
    {
      "code": "ref",
      "file": "inventory/apps/blog.yaml",
      "line": 9,
      "path": "spec.deployments[2].server",
      "message": "引用的 Server web9 不存在"
    }
  ]
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `version` | string | 输出结构版本，目前是 `v1` |
| `ok` | bool | 没有发现问题时为 `true`，此时退出码为 0；否则退出码为 1 |
| `records` | int | 检查的记录数，不含 `infrafile.yaml` |
| `problems` | array | 发现的问题，没有时是空数组 `[]`；按 `file`、`line`、`code`、`path` 排序 |

每个问题：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `code` | string | 问题代码，见下表 |
| `file` | string | 相对数据仓根目录、以 `/` 分隔的路径 |
| `line` | int | 行号，从 1 开始；无法定位到行时省略 |
| `path` | string | 字段路径，如 `spec.deployments[0].server`；含特殊字符的键写成 `metadata.labels["a b"]`；不针对某个字段时省略 |
| `message` | string | 中文说明，给人看的，措辞可能变化，脚本请用 `code` 判断 |

问题代码：

| `code` | 含义 |
| --- | --- |
| `parse` | YAML 无法解析，或文件内容不是一条记录 |
| `layout` | 文件或目录放错了位置，或记录文件、kind 目录是符号链接（不跟随，免得读到仓外文件） |
| `kind` | `kind` 与所在目录不符 |
| `schema` | 不符合 JSON Schema |
| `name` | `metadata.name` 与文件名不一致 |
| `ref` | 外键指向不存在的记录 |
| `ref-archived` | 在用的记录引用了已归档的记录 |
| `plaintext` | 疑似明文凭据；`message` 不会回显凭据内容 |
| `deleted` | 带 `--base` 时，与基线相比有记录文件被删除 |

数据仓不存在、`--base` 引用无效等无法校验的情况退出码为 2，标准输出为空。
