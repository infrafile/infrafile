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
