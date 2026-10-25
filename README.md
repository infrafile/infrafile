# infrafile

> ⚠️ **早期开发中，尚不可用。** 接口、数据格式随时会变，请勿用于真实台账。

infrafile 是面向个人和小团队的基础设施资产台账：**以 git 为后端**，服务器、应用、域名、云账号、云资源、代码仓都写成明文 YAML 存进 git。另外几项能力：

- **凭据只存引用**：记录里只写 `sops://`、`op://`、`env://` 这样的引用，不存值；需要用时注入到子进程，明文不落盘。
- **多云只读同步**：通过插件把云端和实测状态拉下来，写进 `state/`，`git diff` 就能看出变化。
- **受控写**：线上变更先 `plan`，审过后再 `apply`，执行后回读确认。
- **对 AI agent 友好**：CLI 带稳定的 `--json` 契约，提供只读 MCP 服务，并内嵌 agent 使用指南。

Go 单二进制，sops 和 age 以库的方式内置，不用另装。

## 不做的事

- 不做重型 CMDB。
- 不做通用云资产查询（可通过外部插件接 Steampipe / CloudQuery）。
- 不直接写 DNS（生成 [DNSControl](https://github.com/StackExchange/dnscontrol) 配置）。
- 不做监控引擎（计划生成 [Gatus](https://github.com/TwiN/gatus) 配置）。
- 不做密码管理器。

## 文档

目前已完成 M0–M2：数据仓初始化、schema 导出、校验、单条查询与关系、到期检查、SSH config 生成。可先用[虚构示例](examples/homelab/README.md)体验；凭据解析、云同步和受控写等后续能力仍按计划推进。

- [设计](docs/design.md)：定位、数据模型、插件、agent 接口、受控写
- [v0.1 实现计划](docs/plan/v0.1.md)
- [参与贡献](CONTRIBUTING.md)

## 许可证

[Apache-2.0](LICENSE)
