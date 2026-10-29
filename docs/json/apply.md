# apply JSON 契约（v1）

`infrafile apply <计划文件> --dir <目录> [--timeout 30s] --json`：

```json
{"version":"v1","ok":true,"target":"Resource/edge","status":"applied"}
```

| status | ok | 退出码 | 含义 |
|---|---|---|---|
| applied | true | 0 | 控制请求后回读与计划一致 |
| unchanged | true | 0 | 状态未漂移且已是期望值，没有发送控制请求 |
| stale | false | 1 | 执行前观测已改变，没有发送控制请求 |
| mismatch | false | 1 | 控制后回读不符 |
| uncertain | false | 1 | 控制请求或其后回读失败，可能已经发生变更 |

后三种状态带中文 message。计划、台账、实例配置、凭据或执行前观测错误退出 2，stdout 为空，stderr 为错误。不会自动重试、回滚、刷新 state 或修改 inventory；失败后先检查远端，再生成新计划。单动作、漂移范围与原子性边界见[指南](../control.md)。

显式新存储的 v2 计划必须执行：

```sh
infrafile apply pause.json --dir ./my-infra \
  --reviewed-digest <plan-show中已审阅的digest> \
  --authorization-ref chat:approved-message --actor operator --json
```

主体说明（128 字节以内）和授权引用（256 字节以内）必须非空、无控制字符且不含已识别凭据；它们是调用者填写的说明，不证明身份或审批。摘要不匹配、尚未生效、过期、目标/实例配置变化或存储标记移除均拒绝。新执行在开始及发送控制请求前重新核对有效期，控制前重新加载并校验目标绑定。

返回沿用 v1 结果信封，增加 `receipt: <digest>`。执行前提错误仍退出 2，stdout 为空；已经开始写回执时可能留下 `completed/rejected`，其含义是未发送控制请求。写前回执不能确认保存时不发送控制请求；保存状态本身无法确认则返回 `uncertain` / 退出 1，不能据此盲重试。后置记录失败也返回 uncertain，即使云请求可能已成功。完整持久化和读取契约见 [executions](executions.md)。

同一摘要已有回执时，本次仅实时 probe，结果增加 `replayed: true`、`verification: matches_expected | matches_before | different | unavailable`，成功回读另有 `observed_hash`。已有计划可以在到期后用于这次只读核实，但当前绑定仍须匹配。不会重发 control，也不改写原回执；历史 uncertain 不因当前状态匹配而变成 applied。若原结果成功且本次仍匹配，退出 0；原结果失败/不确定，或本次不能确认仍匹配，退出 1。要继续修改，先核实并另行生成、审阅和授权新计划。

旧无存储标记仓的 v1 执行路径与输出保持；v1 不接受这些新前提参数，也不写执行回执。Windows 当前仅提供 v2 的生成/审阅/回执读取；v2 持久执行拒绝，待原生权限与持久化验收后再启用。
