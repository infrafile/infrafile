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

目前已完成 M0–M7：数据仓初始化、schema 导出、校验、查询、到期检查、SSH config 生成、凭据引用、插件宿主、九个第一方同步插件、SSH config / state 草稿导入，以及 plan / apply 受控写和 DNSControl 配置生成。导入默认只输出草稿，`--write` 只创建不存在的记录；Cloudflare 控制先生成计划，apply 前后回读确认。可先用[虚构示例](examples/homelab/README.md)体验；MCP 等后续能力仍按计划推进。

- [设计](docs/design.md)：定位、数据模型、插件、agent 接口、受控写
- [v0.1 实现计划](docs/plan/v0.1.md)
- [凭据引用与运行命令](docs/secrets.md)
- [插件与观测快照](docs/plugins.md)
- [第一方插件配置、观测字段与限制](docs/first-party-plugins.md)
- [导入草稿与只建不覆盖](docs/import.md)
- [受控写与 DNSControl](docs/control.md)
- [参与贡献](CONTRIBUTING.md)

## 第一方插件与最小权限

全部通过 `infrafile sync [插件] --dir <数据仓>` 执行。凭据只写引用；按需配置 `services: [domains]` 或 `[cdn]` 可减少请求和授权范围。

| 插件 | 只读范围 | 所需权限 |
|---|---|---|
| `cloudflare` | zone、DNS 记录、代理状态 | API Token 的 Zone:Zone:Read、Zone:DNS:Read，限定所需 zone；见 [zones](https://developers.cloudflare.com/api/resources/zones/methods/list/) 和 [DNS](https://developers.cloudflare.com/api/resources/dns/subresources/records/methods/list/) |
| `aliyun` | 注册域名、CDN 域名 | RAM `domain:QueryCommonInfo`、`cdn:DescribeUserDomains`；见[域名 API](https://help.aliyun.com/zh/dws/developer-reference/api-domain-2018-01-29-querydomainlist)、[CDN API](https://www.alibabacloud.com/help/en/cdn/developer-reference/api-cdn-2018-05-10-describeuserdomains) |
| `tencent` | 注册域名、CDN 域名 | CAM `domain:DescribeDomainNameList`、`cdn:DescribeDomains`；见[域名 API](https://cloud.tencent.com/document/product/242/48941)、[CDN API](https://cloud.tencent.com/document/product/228/41118) |
| `baidu` | 注册域名、CDN 域名 | BCD 只读权限（可能需要申请 API 白名单）；CDN `QueryDomainList`，资源 `domain/*`；见 [BCD API](https://cloud.baidu.com/doc/BCD/s/4jwvymhs7)、[CDN IAM](https://cloud.baidu.com/doc/CDN/s/zk04vd3kw) |
| `ctyun` | CDN 国内、海外、全球清单 | 需能调用只读 `query-domain-list` 的 EOP AK/SK；公开文档未确认精确 IAM action，最小 API 授权需向厂商核实，不能把控制台菜单权限当作 API 权限；见 [CDN API](https://www.ctyun.cn/document/10015932/10035101) |
| `dns` | A、AAAA、CNAME | 无云账号权限，使用系统 DNS 解析器 |
| `tls` | 服务证书、到期时间、信任验证结果 | 无云账号权限，只需目标端口可达 |
| `whois` | 域名、注册商、到期日、NS、状态 | 无账号权限，只需 WHOIS TCP 43 可达 |
| `ssh` | 系统、内核版本、架构 | 能执行固定 `uname -srm` 的普通 SSH 用户；已信任主机密钥与非交互认证，不需 sudo |

天翼注册域名暂不支持；快照明确返回 `unsupported: [registered-domains]`。云 API 签名和分页由官方样例改写的虚构响应及合成边界夹具离线验证，未进行真实账号调用或 IAM 授权验收；BCD 与天翼的权限确认缺口见[调研记录](docs/research/m5-baidu-ctyun.md)。

## 许可证

[Apache-2.0](LICENSE)
