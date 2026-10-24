# `infrafile schema export`

不带 `-o` 时，无论有没有 `--json`，都把全部 schema 合成一个 JSON 输出：

```json
{
  "version": "v1",
  "schemas": {
    "account.schema.json": { "$schema": "https://json-schema.org/draft/2020-12/schema", "...": "..." },
    "common.schema.json": { "...": "..." }
  }
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `version` | string | 输出结构版本，目前是 `v1` |
| `schemas` | object | 键是文件名，值是 JSON Schema（2020-12）。schema 之间用相对 `$ref` 引用，如 `common.schema.json#/$defs/date`；外键字段带 `"x-ref": "<Kind>"` |

带 `-o <目录>` 时把同样的文件写进目录；加 `--json` 输出：

```json
{
  "version": "v1",
  "dir": "schemas",
  "written": ["account.schema.json", "app.schema.json", "..."]
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `dir` | string | 命令行给出的目录，原样输出 |
| `written` | array | 写入的文件名，按名称排序 |
