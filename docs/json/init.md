# `infrafile init --json`

```json
{
  "version": "v1",
  "dir": "ledger",
  "created": [
    "infrafile.yaml",
    "inventory/servers/.gitkeep",
    "inventory/apps/.gitkeep",
    "inventory/domains/.gitkeep",
    "inventory/accounts/.gitkeep",
    "inventory/resources/.gitkeep",
    "inventory/repos/.gitkeep"
  ]
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `version` | string | 输出结构版本，目前是 `v1` |
| `dir` | string | 命令行给出的目录，原样输出；没给时是 `.` |
| `created` | array | 新建的文件，相对 `dir`、以 `/` 分隔；已经存在的 `.gitkeep` 不列出 |

目录里已经有 `infrafile.yaml` 时不做任何改动，退出码为 2，标准输出为空。
