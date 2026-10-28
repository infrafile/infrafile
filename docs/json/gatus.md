# gatus JSON 契约（v1）

`infrafile gatus [目录] --json` 返回：

```json
{"version":"v1","config":"endpoints:\n...","endpoints":["example.com"]}
```

config 是 Gatus YAML 的 `endpoints` 片段；endpoints 是按 Domain 主键、主机名排序的端点名称。只有显式配置 `Domain.spec.bindings[].monitor` 的绑定参与。根域名使用 `@`，输出 name 为实际主机名，group 为 Domain 主键，url 固定 HTTPS。monitor 可填写 path（默认 `/`）、interval（默认 `1m`）、status（默认 `200`）；空对象启用全部默认值。

interval 接受 Go 正时长（如 `30s`、`5m`、`1h`，不支持 `7d`）；status 必须是 100–599 的整数；path 必须是以单个 `/` 开始的有效请求路径，可含查询参数，不允许片段、控制字符或非法转义。主机名必须是合法 DNS 名称，不接受通配符。字段类型及范围由 schema 校验，时长、请求路径和重复主机名在生成时进一步校验。

每个端点包含 `[STATUS] == <status>` 和 `[CERTIFICATE_EXPIRATION] > 240h` 两个条件；不生成 WHOIS 检查、凭据、alerting 或 storage。归档 Domain 和归档 Server 不生成端点；整个数据仓仍须先通过校验，在用记录引用归档 Server 会被校验拦下。未声明 monitor 的绑定不参与。没有符合项时 config 为 `endpoints: []\n`，endpoints 为 `[]`；此空片段需与其他端点配置合用，不能单独作为 Gatus 的完整配置。

退出 0 成功，2 表示数据仓或生成参数有误；失败 stdout 为空。此命令不读 state、不联网、不解析凭据、不写文件。把文本输出保存到 Gatus 配置目录后，告警、存储等配置仍由用户维护；目录合并行为见 [Gatus v5.37.0 官方文档](https://github.com/TwiN/gatus/blob/v5.37.0/README.md#configuration)。实际探测由 Gatus 启动后执行，生成成功不代表服务健康。

## 配置加载验收

2026-10-28，构建开发版二进制，用 `infrafile gatus examples/homelab` 生成临时 YAML；在仓外临时 Go 模块中固定依赖 `github.com/TwiN/gatus/v5 v5.37.0`，调用官方 `config.LoadConfiguration` 加载该文件，并断言得到一个名称为 example.com 的端点。加载与全部端点配置校验通过，未调用 watchdog、启动监听或执行网络探测。Gatus 的依赖只存在于临时模块，未加入 infrafile 的 go.mod；验收使用满足 Gatus 要求的 Go 1.26.3 以上版本。

验收入口的核心调用如下，可在独立临时模块中复现；file 是生成的 YAML 绝对路径：

```go
cfg, err := config.LoadConfiguration(file)
if err != nil || len(cfg.Endpoints) != 1 || cfg.Endpoints[0].Name != "example.com" {
    panic("generated Gatus configuration did not load")
}
```
