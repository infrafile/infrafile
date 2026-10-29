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

## `--draft` / `import review` 的 v1 报告

这是显式选择的新流程，保留上文原有模式的 JSON 和退出码。两种调用返回同一个报告：

```json
{
  "version": "v1",
  "ok": true,
  "source_changed": false,
  "repo_changed": false,
  "records": [],
  "skipped": [],
  "problems": []
}
```

records 按 id 排序；每项有 `id`（64 位小写 SHA-256）、source、status，可选 key（Kind/name）、match（已有主键）、gaps（缺口代码）。非法或疑似含秘密的候选不回显内容或主键。报告不带 record 全文，人工编辑保存在草稿文件中。

| status | 含义 |
| --- | --- |
| draft | 当前可作为新记录候选；并未创建 |
| exists | 主键已存在且候选字段是现有内容的子集；不覆盖 |
| archived | 该主键已归档；保持归档、不创建 |
| duplicate | 同批相同新主键与内容重复，只保留首项 |
| excluded | 人工显式取消选择 |
| conflict | 同键异值、自然身份碰撞或账号范围不明；不能发布 |
| invalid | 候选 Schema、名称或明文凭据检测不通过 |

gaps 代码为 missing-expires_at、missing-registrar、missing-resource_id、missing-ssh.user、missing-ssh.port、identity-edited 或 identity-matches-archive。missing 不补值；gaps 不必然令 ok=false。skipped 使用原映射的 source/reason；problems 使用 source-changed、repository-changed 或共享校验的问题 code，不包含原始错误或敏感值。

退出码 0 表示本次候选与基准校验通过；1 表示可生成报告但被拒绝；2 表示用法或无法开始/完成，stdout 为空。`--draft` 的 1 可能已保存完整草稿，供人工处置冲突；保存不会覆盖已存在文件。结果不代表拥有写权限，也不表示资产迁移完成。

## ImportDraft v1 文件

文件顶层固定 `version: v1`、`kind: ImportDraft`、source（state/ssh-config）、files、repo_hash、candidates；所有字段必需。files 是 path/hash 对象数组，hash 为实际输入字节的 SHA-256；repo_hash 是配置及逐文件记录 JSON 语义的 SHA-256，不含 Git HEAD、注释或状态。清单中的路径只对照，不作为审阅时的读取入口。

文件包含来源路径和资产信息，保存在使用方私有环境；权限 0600 不代表它经过加密。新草稿的 state 来源另限累计 64 MiB/1024 个文件，超限不返回部分结果。

candidates 每项固定 id、source、identity、selected（必须显式 boolean）、record（完整资产对象）。identity 复用自然身份定义：Domain/name，Server/address，Resource 的 provider/resource_id 或 provider/type/endpoint；账号范围由候选及既有记录中显式的 spec.account 判断。id 是规范 JSON 对象 `{"identity": identity, "source": source}` 的 SHA-256；新生成相同来源/身份时不因观测值或时间变化而更换 id。版本 v1 绑定此算法，未知版本拒绝。

只编辑 record/selected。id、source、identity 与候选集合必须和当前来源一致；改来源文件或基准后重新生成，不能改哈希冒充重新审阅。文件无生成时钟，输入及仓库语义相同会产生相同内容。SSH 路径按当前本机规范路径记录，换位置或平台后应重新生成，不自动读取旧路径。

读取允许共享 YAML 解析器支持的 JSON/YAML 对象，字段名区分大小写且不接受未知/缺失字段、重复键或多文档。生成端输出规范 JSON；4 MiB、4096 个候选、1024 个来源文件与共享解析预算同时生效。它是预览文件，不是 FindingHistory/EditTransaction/ExecutionReceipt，不修改台账存储标记。
