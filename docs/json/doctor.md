# doctor JSON v1

```json
{
  "version": "v1",
  "ok": true,
  "online": false,
  "checks": [
    {"check":"repository","status":"passed","code":"valid","message":"布局、结构、外键及明文凭据检查通过"}
  ]
}
```

`ok` 表示所有请求项 status 都为 passed，不表示无业务风险。`online` 为调用模式，不保证每个实例发过请求。布局/校验失败时仅返回 repository failed，其余检查不执行。无法开始时退出 2、stdout 为空。

checks 有序：repository 在前，全部引用检查按记录顺序/字段名排序，实例按插件和实例名排序。`check` 为 repository、credential-reference 或 instance；凭据引用只显示所属 `subject`（Kind/name），不返回引用原文或字段值。实例使用 plugin/instance，services 与 required_permissions 为已配置服务及其能力目录权限说明；不验证完整 IAM 或输出配置。可省略字段不补 null，空服务/权限列表省略。

`status` 为 passed / failed / unknown；`code` 含义及预算见[分层预检](../doctor.md)。message 是可安全展示的固定说明，脚本按 code/status 判断。退出码沿用冻结的 0/1/2 契约；结果不含快照、云资源清单、密钥、签名或原始错误。
