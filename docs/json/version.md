# `infrafile version --json`

```json
{
  "version": "v1",
  "build": {
    "version": "v0.1.0",
    "commit": "abc1234",
    "date": "2026-10-24T00:00:00Z"
  }
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `version` | string | 输出结构版本，目前是 `v1`；不是程序版本 |
| `build.version` | string | 程序版本；本地构建是 `dev` |
| `build.commit` | string | 构建时的提交；本地构建是 `none` |
| `build.date` | string | 构建时间；本地构建是 `unknown` |
