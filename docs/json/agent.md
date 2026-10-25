# `agent guide` / `agent install --json`

`agent guide --json`：

```json
{
  "version": "v1",
  "build": {"version": "dev", "commit": "none"},
  "guide": "---\nname: infrafile\n..."
}
```

`guide` 是完整 Markdown；文本模式直接输出同样的字节。build.version / commit 来自构建注入，与 guide 内嵌标识一致；顶层 version 是 JSON 契约版本。

`agent install codex --json`：

```json
{
  "version": "v1",
  "build": {"version": "dev", "commit": "none"},
  "client": "codex",
  "path": ".agents/skills/infrafile/SKILL.md",
  "status": "created"
}
```

client 只能是 codex / claude；claude 对应 `.claude/skills/infrafile/SKILL.md`。path 相对于传入的 `--dir` 形式构造；传绝对目录时返回绝对路径。status 为 created / unchanged，后者表示内容一致且没有重写。

成功退出 0。参数错误、目录不可写或已有内容不同退出 2，stdout 为空，stderr 是文本诊断；不会替换已有文件。黄金测试锁定文本、两个客户端 JSON 及重复安装输出。安装边界见[Agent 指南](../agent.md)。
