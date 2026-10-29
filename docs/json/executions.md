# 执行计划与回执（v1 信封）

```sh
infrafile executions list --dir ./my-infra --json
infrafile executions list --dir ./my-infra --key Resource/edge --json
infrafile executions show <64位回执ID> --dir ./my-infra --json
```

list 返回 `{version:"v1",present,executions:[...]}`。每项为 `id,target,operation,actor,authorization_ref,created_at,updated_at,expires_at,phase,status`，按 created_at 降序、同时间按 id 升序。只索引已尝试执行的计划，未执行 JSON 文件仍由 `plan show` 审阅。show 返回完整管理记录：

```json
{
  "version":"v1",
  "kind":"ExecutionReceipt",
  "data":{
    "id":"<v2-plan-digest>",
    "plan":{"version":"v2","generated_at":"...","expires_at":"...","digest":"...","actions":[]},
    "actor":"operator",
    "authorization_ref":"chat:approved-message",
    "created_at":"2026-12-01T12:00:00Z",
    "updated_at":"2026-12-01T12:00:01Z",
    "phase":"write_started",
    "write_started_at":"2026-12-01T12:00:01Z",
    "before_hash":"<已核对的观测哈希>",
    "result":{"version":"v1","ok":false,"target":"Resource/edge","status":"uncertain","receipt":"<v2-plan-digest>"}
  }
}
```

示例省略计划内容和哈希，不能作为输入。固定文件为 `history/executions/<digest>.json`，不接收任意文件路径。data 内保存完整审阅计划、来源说明和目标/实例绑定的前提哈希，最终回读只记录 `readback_hash`；不复制实例配置、凭据或原始错误。

写入顺序为 `prepared` → `write_started` → `completed`。前两阶段 result 始终为 uncertain；每次使用 0600 临时文件、文件 fsync、原子替换和目录 fsync，新建父目录也先持久化。history 和 executions 写入时须为 0700 普通目录；不会悄悄修改已有权限。写前回执落盘失败不发送控制请求。进程在写后崩溃留下 write_started/uncertain，单凭本地记录不能推断远端未写或成功。

同仓执行由持久锁文件上的本机 advisory lock 互斥，进程退出释放；文件保留，不按 PID 猜测/删除活锁。读回执不取执行锁，能看到完整的上一个阶段。同摘要重复执行仅 probe，核实结果返回给调用者且不改写原回执。它不是云端幂等键、exactly-once、服务端 CAS 或跨机器互斥；不同副本/旧执行器/直接云操作不受本机锁保护。目录/锁/原文件被替换会拒绝对应保存；同一 OS 用户的恶意操作不在隔离保证内。

每回执 4 MiB，复用 100 层/100,000 节点/16 MiB 展开预算；目录最多 256 回执、64 MiB 内容，额外受目录条目预算限制。损坏、未知结构/版本、重复键、敏感内容、文件名/摘要不符、符号链接或非普通文件均拒绝，不以空列表掩盖错误。没有自动清理。确需腾出容量，先独立校验并保留完整归档，只移出已到期且结果已核实的记录；未到期或未解决的 uncertain 继续保留，不能删回执绕过重复保护。

读取不创建目录、锁、历史，也不联网、解析凭据或重新验证云状态。无需当前 inventory 有效，以便救援查证；丢失历史不表示云端没有操作。成功读取退出 0，错误退出 2 且 stdout 为空。Web 的 executions/execution API 复用同一读取器和有界输出，超限整份失败，不返回截断前缀。
