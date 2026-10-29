# `findings`：显式问题历史（v1.0 开发）

`findings refresh <目录> --max-age 7d` 离线刷新**完整台账**的问题历史。配置须已显式声明 `storageVersion: v1`，命令不自动迁移配置、不采集、不解密、不通知。成功返回 0，包括仍有活动问题或没有新变化；参数、损坏、并发、输入冲突、容量或保存失败返回 2。旧 `audit` 的 0/1/2 含义保持。

```sh
infrafile findings refresh ./my-ledger --max-age 7d --json
infrafile findings list ./my-ledger --json
infrafile findings changes ./my-ledger --after 0 --json
```

`list` / `changes` / `export` 不创建目录、锁或历史，不推进检查时间，不要求当前 inventory 有效，便于单独救援历史。没有历史时 list 返回 `present: false, history: null`，changes 返回 `present: false, changes: []`；缺历史不表示已恢复。非零游标在缺历史时拒绝。

## 保存与输入

固定位置为 `state/.findings.json`，沿用[管理记录信封](../compatibility.md)：

```json
{"version":"v1","kind":"FindingHistory","data":{}}
```

空 data 仅说明信封，不能作为有效历史。载荷由固定 Go 结构、严格解码和语义校验冻结：

| data 字段 | 含义 |
| --- | --- |
| identity_version | `v1`，身份算法版本 |
| input_hash | `{files: inputs, max_age_days}` 的规范 Go JSON SHA-256；不包含本次时间 |
| inputs | 按 path 排序、无重复的 `{path, hash}`；hash 为原始字节 SHA-256；目录 path 以 `/` 结尾，hash 为零字节 SHA-256 |
| max_age_days | 本次显式非负 TTL；改变 TTL 可改变问题适用范围 |
| checked_at | 最近显式求值的 UTC RFC3339 秒时间 |
| issues | 按 fingerprint/scope_hash 排序的状态；字段和恢复规则见 [issues.md](issues.md) |
| changes | 连续序号的变化数组，每项是 `{sequence, identity, type, at}` |
| last_sequence | 单调递增的变化尾游标，清理后也不重置 |
| pruned_through | 已明确清理的变化前缀尾序号，初始 0 |

输入仅包含 infrafile.yaml、inventory 的非隐藏文件/目录、state 的非隐藏观测/同步时间以及 `.sync-status.yaml`、`.presence.yaml`；不读取凭据文件。隐藏历史、临时文件和锁不进入输入摘要。清单用于核对，读取历史不会按其中路径打开文件。输入最多 1024 个文件/目录、累计 64 MiB。

刷新持有既有 `state/.sync.lock`，与 sync 及其他刷新/清理互斥；在 0700 临时目录复制固定输入，以完整校验后的副本求值。文件均为 0600，副本结束后删除。写前重新读取原始输入、核对原历史字节、仓库/state 身份及所持锁；任何变化拒绝覆盖。锁只能协调遵循它的工具，外部编辑器在最终复核后仍有极短竞争窗口，文件系统与外部编辑不是原子事务。

历史先完整编码、严格回读、写入同目录 0600 临时文件并 fsync，复核后原子 rename 单文件，再同步目录。替换前错误保留旧历史；替换后目录同步失败会明确报告“结果未确认”，需核对现存历史，不冒充成功。强制终止可留锁和隐藏临时文件；确认原进程已退出后才人工处理其锁，不能删除有效历史来恢复正常。

读取拒绝符号链接、非普通文件、重复键、多文档、超深/超大数据、未知信封/身份版本、未知/缺失/非规范字段、错误输入摘要、重复/矛盾状态、时间错误或不连续序号。取消有效；损坏历史阻止刷新及 audit/inspect，不默认为空历史。

## 结果与变化

refresh / prune 的 JSON：

```json
{"version":"v1","written":true,"history":{"version":"v1","kind":"FindingHistory","data":{}},"changes":[]}
```

示例中空 data 仍仅作结构说明。written 表示本次是否发布；changes 只含本次新增事件，完整保留事件在 history.data.changes。相同输入和秒时间重复运行，written=false、不追加事件；时间推进会更新 last_checked，只有状态/资格发生变化才产生新事件，单纯过期产生 unknown，不恢复旧问题。

list 返回 `{version,present,history}`；changes 返回 `{version,present,last_sequence,pruned_through,changes}`。序号在同一历史中连续递增，类型为 opened / unconfirmed / confirmed / recovered / reopened / out_of_scope / returned / unknown / clear / waived / unwaived。`--after N` 只返回 sequence>N；N 小于 pruned_through 或大于 last_sequence 时拒绝，避免通知消费者悄悄遗漏变化。游标不提供接收方送达保证。

存在有效历史时，audit/inspect 兼容增加可选 history：`{checked_at,input_hash,issues,last_sequence,pruned_through}`。它是**最近一次显式保存的状态**，与本次当前观测报告分开；查询不推进历史。inspect 选择资产时仅返回所选目标/对应检查的历史。缺历史省略该字段；没有改变 audit 的 findings 数量、ok 或退出码，inspect 的 ok 仍由当前执行/覆盖/风险计算。CLI 与 MCP 共用查询。

## 容量、导出与显式清理

默认上限为 **512 个问题范围、1024 个变化、4 MiB 文件**，同时受共享解析器的 100 层、100000 节点及 16 MiB 展开预算限制；先达到任一上限就拒绝刷新、保留原历史。没有自动裁剪。512 条问题/1024 条变化的虚构身份夹具测量见 history 测试；该规模用于单用户台账，并非任意复杂身份的容量承诺。范围退出的问题也计容量，不能因超龄当作已解决。

完整导出始终写原始 JSON 到 stdout，`--json` 不套新信封；历史可能含私有资产信息。以下在**使用方私有位置**创建独立副本，再显式清理：

```sh
(umask 077; set -C; infrafile findings export ./my-ledger > ./findings-backup.json)
infrafile findings prune ./my-ledger --export ./findings-backup.json --json
```

prune 必须确认导出文件是独立普通文件，且与当前历史**完整字节相同**；导出旧了、文件/目录异常或用历史自身代替导出均拒绝。默认界限为 180 天前，可用 `--before 2026-01-01T00:00:00Z` 显式选择过去时间。只删除界限前已恢复的状态以及变化时间前缀，所有 open / out_of_scope 状态保留。清理不重置游标，不产生“恢复”事件。被明确裁掉的恢复条目以后复发会从新记录开始；原首次发现、发生次数和事件保留在完整导出中。清理后要再次清理，须重新导出当前完整文件。

以上验证是离线文件/进程与虚构台账验收，不代表真实调度、通知送达、云端修复或正式发布。
