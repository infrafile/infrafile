# `infrafile change`（v1.0 开发）

CLI、经过人工修改的 ImportDraft 和显式启用编辑的 Web 共用候选校验与事务服务。只修改当前数据仓的 inventory 记录并创建本地 Git 提交；工具不调用 push 或云端操作。当前正式 v0.3.0 尚无本接口。

本次写入保证限 Linux/macOS；Windows 保持只读能力，变更预览/提交/恢复在落盘前明确拒绝。Windows 私有目录 ACL、文件锁和原生中断恢复须另行实现与实测，不能仅从可交叉构建推断可用。

## 前提与预览

数据仓须是已存在初始提交的普通 Git 分支根目录，工作树和索引干净，无 merge/rebase、sparse/assume-unchanged/split-index。Git worktree 支持独立分支及索引。显式在 infrafile.yaml 启用 `storageVersion: v1`，加入 `/.infrafile/` 忽略规则并按现有 Git 流程提交；工具不自动改配置或忽略文件。先建立并提交各 kind 目录；`infrafile init` 已创建六种目录和新的忽略文件，已有忽略文件保持。

请求为完整记录替换，不是字段 patch；创建、更新、归档和引用调整可放在同批。省略的字段会被移除，主键变化表示创建另一条记录，不自动删除旧记录。停用旧主键须显式填写 metadata.archived 并修正其他在用记录的引用。

```json
{"version":"v1","records":[{"apiVersion":"infrafile/v1alpha1","kind":"Server","metadata":{"name":"web"},"spec":{"address":"192.0.2.10"}}]}
```

```sh
infrafile change preview /path/to/ledger --request ./request.json --out ./review.json --json
# 核对输出中的前后字节与 digest，再显式提供已审阅摘要
infrafile change apply ./review.json /path/to/ledger --digest <sha256> --json
infrafile change status /path/to/ledger --json
```

预览只写仓外临时副本，`--out` 可另存新的 0600 审阅文件，不覆盖既有文件，禁止 inventory/state 等受保护位置。请求/预览文件放在数据仓外，避免形成未跟踪脏文件。普通预览不在仓内创建管理目录、Git 对象或修改索引。完整记录会重新序列化为 YAML，变更文件的注释/排版会变化；CLI 展示每个变更文件的完整 `-`/`+` 字节行，原字节同时进入事务恢复记录。

预览结果包含 version=v1、kind=EditPreview、base、preserve_secrets、request、ok、problems、files、digest。base 绑定规范仓库/Git/common 目录、分支、HEAD、索引字节、有效 Git 配置、infrafile.yaml 与完整 inventory 输入的哈希和权限。files 按路径排序，每项含 key/path/before/after/before_hash/after_hash/existed/mode；路径只能由合法 kind/name 推导。digest 是清空 digest 字段后整个规范 JSON 的 SHA-256，不是身份认证或审批证明。apply 会重新生成并比对完整预览，不信任输入文件的路径或 ok。

整个原台账须校验通过，完整候选也须通过现有 Schema、外键、归档引用、watch 和明文凭据规则；同批可以相互引用。无效候选只输出问题代码，清空 request/files，避免回显错误秘密值。CLI 可编辑合法秘密引用，不解密；Web 拒绝秘密字段并由服务端保留已有 spec.secrets，返回省略 secrets 的字段差异，详见[工作台](../serve.md)。

每批最多 64 条记录；单请求、预览、事务记录分别最多 4 MiB，先验证完整事务可编码再改业务文件。固定输入最多 1024 项、64 MiB，并沿用共享 YAML 的深度/展开预算。Web 另限 64 KiB 请求。拒绝未知/缺失/大小写变体字段、重复键、坏版本、符号链接、特殊文件和越界路径。超限须缩小批次，不自动拆成多个提交。

## 导入草稿

```sh
infrafile change preview /path/to/ledger --draft ./draft.json --out ./review.json --json
# SSH 草稿必须由操作者再次明确选择原始输入
infrafile change preview /path/to/ledger --draft ./ssh-draft.json \
  --source-file ./ssh.conf --out ./ssh-review.json --json
```

转换时重新执行 import review，核对来源字节/身份和台账语义基准，只提取 selected 且状态为 draft 的新记录；已存在、归档、冲突不会被覆盖或复活。仍受每批 64 条限制，可用 selected:false 缩小范围。转换后 review.json 是冻结的已审阅编辑意图；apply 复核台账/Git 基准，不按预览或草稿文件内的路径再读取 SSH 来源。如果需要使用更新来源，重新生成并审阅草稿及预览。原有 import --write 的只建不覆盖模式保持原契约。

## 提交、失败与恢复

工具按顺序持有私有编辑锁、现有采集的 state/.sync.lock、当前 Git 索引锁和 HEAD 锁；同进程/跨进程冲突都会拒绝。锁只协调配合的工具，不能锁住任意编辑器。锁内重新固定输入和完整校验，持久化 0600 事务记录后才激活 `.infrafile/active` 并写记录。普通读取发现 active 会拒绝展示半写台账；提交自身的钩子通过仅在子进程中传递的事务标记校验已写完候选，此标记不是安全凭据，也不隔离同一 OS 用户。

正常 Git commit 在私有 Git 目录和索引中执行，沿用原分支名称、有效配置和原有 hooks，并带 DCO 与事务 trailer。先计算仅含本次文件的目标树；钩子失败、改动树或产生额外工作树变化都会停止，保留现场。Git 配置仅经进程环境传给子 Git，不写入普通备份。自定义 filter/working-tree-encoding 的变更文件拒绝提交，以免字节转换与所审阅内容不符。原钩子的外部行为仍由仓库自身政策负责。

核对父提交、完整树和文件/索引/配置基准后，以旧 HEAD 为条件更新真实分支，随后在锁内原子替换真实索引。私有 Git 目录的 commondir 指向真实共享 refs；真实 HEAD 始终保留保护锁，更新写入分支 reflog，HEAD reflog 留在私有目录，恢复以分支提交及持久记录为准。工具不会强推、自动合并、重置分支或清理外部文件。

结果形如：

```json
{"version":"v1","status":"recovery_required","code":"transaction_interrupted","transaction":"<64 hex>"}
```

status 为 committed、unchanged、rejected、recovery_required；有实际提交时附 commit。status 命令干净时返回 clean；恢复还可返回 rolled_back/complete。code 为稳定的原因分类，不能将命令或 HTTP 成功当成提交成功。进入活动事务后任何 I/O、取消、钩子或 Git 失败都保留锁与恢复材料；请求失去回执时先运行 status，不盲目重试。

```sh
infrafile change recover <transaction-id> /path/to/ledger --json
```

恢复读取 `.infrafile/transactions/<id>.json` 的 `{version:"v1",kind:"EditTransaction",data:{...}}`，严格核对仓库身份、预览摘要、前后内容/索引哈希和提交。原始文件与原索引备份在该 0600 记录内，私有 Git 目录位于同名 0700 子目录，索引后版本另以哈希绑定。prepared/writing/ready 表示未结束；complete/rolled_back 为持久终态；active 与这些记录共同决定是否须恢复，不靠时间推断成功。

若 HEAD 仍为原提交，先核对整个批次，只有文件等于本事务 before 或 after 才能回滚；仅还原 after，保留其余外部内容。若 HEAD 是已持久记录且树/父提交/trailer 匹配的本次提交，核对全部 after，再完成索引与终态。发现外部文件、索引、配置、分支或 HEAD 改动时拒绝相关恢复，不 reset。损坏记录、外来锁、仍在运行的所有者也会阻断。恢复自身另持进程文件锁，支持重启后重试；PID 被其他活进程复用时保守拒绝，需要人工核对现场。事务材料不会自动裁剪或提交到 Git。

preview 退出码 0 有效（包括空变更），1 候选业务校验失败，2 参数/读取/前提错误；apply/status/recover 退出码 0 完成/无变化/干净，1 rejected 或 recovery_required，2 无法读取、损坏或恢复冲突。CLI 退出 2 不打印部分成功 JSON。发生恢复冲突仍保留全部未处理证据，需由操作者核对，不能删除 active 或锁来强行通过。
