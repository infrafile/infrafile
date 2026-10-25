# `infrafile show --json`

用法：`infrafile show <name|Kind/name> [目录] --json`，目录默认为当前目录。

可查询归档记录。名称在不同 kind 中重复时返回错误，需用 `App/blog`、`Repo/blog` 等完整名称区分；kind 大小写与 schema 一致。

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `version` | string | 输出结构版本，`v1` |
| `file` | string | 选中记录相对数据仓根目录的文件路径 |
| `record` | object | 完整记录：`apiVersion`、`kind`、`metadata`、`spec`，包括扩展字段 |
| `incoming` | array | 引用选中记录的入边；无引用时为 `[]` |
| `outgoing` | array | 选中记录引用其他记录的出边；无引用时为 `[]` |

每条边包含：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `kind` | string | 入边的来源类型，或出边的目标类型 |
| `name` | string | 入边的来源名称，或出边的目标名称 |
| `file` | string | 入边来源或出边目标的相对文件路径 |
| `path` | string | 引用字段在**来源记录**中的路径，例如 `spec.deployments[0].server` |

关系从 schema 的 `x-ref` 生成，与外键校验使用同一套提取逻辑。入边包括归档来源；同一来源通过不同字段引用时保留每一条边。入边按来源文件、schema 字段遍历顺序排列；出边按 schema 字段遍历顺序排列（对象键按字典序，数组下标递增）。

成功退出 0。名称不存在、名称歧义、参数错误或数据仓校验失败时退出 2，stdout 为空，stderr 为文本说明。文本模式打印重排后的 YAML 和两组关系，不保留源文件注释。
