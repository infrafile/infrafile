# `infrafile expiry --json`

用法：`infrafile expiry [目录] [--within 30d] --json`，目录默认为当前目录。

例如以 2026-12-01 为检查日期，示例台账返回：

```json
{
  "version": "v1",
  "ok": false,
  "as_of": "2026-12-01",
  "within_days": 30,
  "entries": [
    {
      "kind": "Resource",
      "name": "assets-cdn",
      "file": "inventory/resources/assets-cdn.yaml",
      "expires_at": "2026-11-30",
      "days_left": -1
    },
    {
      "kind": "Domain",
      "name": "example.net",
      "file": "inventory/domains/example.net.yaml",
      "expires_at": "2026-12-31",
      "days_left": 30
    }
  ]
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `version` | string | 输出结构版本，`v1` |
| `ok` | bool | `entries` 为空时为 `true` |
| `as_of` | string | 进程本地时区的检查日期，`YYYY-MM-DD` |
| `within_days` | int | 阈值天数，默认 30 |
| `entries` | array | 到期项；没有时为 `[]`；按 `days_left`、`file` 排序 |

每项的 `kind`、`name`、`file` 标识记录；`expires_at` 是 `spec.expires_at`；`days_left` 是两个日历日的差，负数为已过期，0 为今天到期。

`--within` 只接受非负整数加 `d`。已过期、今天到期和恰好在阈值当天到期的记录均报告；`0d` 仅报告已过期和今天到期。按日历日计算，夏令时不改变天数。

忽略归档记录和未填写 `spec.expires_at` 的记录，不猜测缺失的到期日，也不读取 `state/` 或云端；因此 `ok: true` 仅说明已登记的到期日未命中阈值。

退出码：0 没有到期项；1 发现到期项；2 参数错误、数据仓校验失败等无法检查的情况（stdout 为空，stderr 为文本说明）。
