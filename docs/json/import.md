# `infrafile import ssh-config|state --json`

共同选项：`--dir <数据仓>`（默认当前目录）、`--write`、`--output <待审目录>`。SSH 子命令另有 `--file`，默认当前用户 `~/.ssh/config`。`--write` 与 `--output` 互斥。

```json
{
  "version": "v1",
  "mode": "stdout",
  "records": [
    {
      "source": "ssh-config/web1",
      "kind": "Server",
      "name": "web1",
      "file": "servers/web1.yaml",
      "status": "draft",
      "record": {
        "apiVersion": "infrafile/v1alpha1",
        "kind": "Server",
        "metadata": {"name": "web1"},
        "spec": {"address": "192.0.2.10"}
      }
    }
  ],
  "skipped": []
}
```

- `mode` 为 `stdout`、`review` 或 `inventory`；后两者有 `directory`，是命令使用的目标目录（可能是相对路径）。
- `records` 按 file、source 排序，空列表为 `[]`。`file` 始终是相对目标根目录的 `<kind 复数>/<name>.yaml`；stdout 模式下是建议路径。
- `status` 为 `draft`（仅展示）、`created`（本次创建）、`exists`（台账主键或输出目标已存在）。`draft` / `created` 附完整 `record`；`exists` 不携带既有记录内容。
- `source` 为 `ssh-config/<原别名>` 或仓内相对快照路径。
- `skipped` 是 `{source, reason}` 列表；reason 有 `observation-only`、`not-found`、`unsupported-plugin`、`duplicate`。已有记录通过 records 的 exists 表示。
- 0 表示处理成功，包括全部跳过；2 表示命令无法完成，stdout 为空，stderr 是安全错误文本。未提供 `--write` / `--output` 时不写文件。
- 写入中途失败可能已有部分记录创建；逐文件只建不覆盖，可安全重试。不会输出原始解析错误、凭据值或整个原始快照。

规则与映射见[导入指南](../import.md)，输出由 CLI 黄金测试锁定。
