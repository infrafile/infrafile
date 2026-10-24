# `--json` 输出

每个命令都可以加全局选项 `--json`，把结果以 JSON 输出到标准输出。这些结构是给脚本和 agent 用的稳定契约：

- 顶层都有 `"version": "v1"`，表示输出结构的版本。新增字段不升版本；删除、改名或改变字段含义时升版本。
- 字段的确切输出由 `cmd/infrafile/testdata/golden/` 下的黄金测试锁定。
- 退出码与文本模式相同：0 成功，1 发现问题（目前只有 `validate`），2 用法错误或命令无法执行。
- 退出码为 2 时标准输出为空，错误说明在标准错误里，是给人看的文本，不是 JSON。

| 命令 | 文档 |
| --- | --- |
| `infrafile validate` | [validate.md](validate.md) |
| `infrafile init` | [init.md](init.md) |
| `infrafile schema export` | [schema.md](schema.md) |
| `infrafile version` | [version.md](version.md) |
