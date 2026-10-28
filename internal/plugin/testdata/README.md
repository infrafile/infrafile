# 第一方插件离线回放夹具

这些文件**不是实际账号抓包**。云响应根据官方公开示例结构改写为 example.com / example.net 等虚构数据，并添加分页、额外敏感字段和异常边界；测试通过内存 HTTP transport 回放，不连接真实云服务。合成私密字段专门验证白名单投影不会把原始字段写入快照。

| 文件 | 来源与改写范围 |
|---|---|
| `cloudflare-*.json` | 官方 [zones](https://developers.cloudflare.com/api/resources/zones/methods/list/) 与 [DNS records](https://developers.cloudflare.com/api/resources/dns/subresources/records/methods/list/) 响应结构；zone / record ID、域名和记录全部虚构 |
| `cloudflare-origin-rules.json`（M7） | 官方 [Origin Rule 示例](https://developers.cloudflare.com/rules/origin-rules/examples/change-http-host-header/) 和[更新规则](https://developers.cloudflare.com/ruleset-engine/rulesets-api/update-rule/)结构；虚构两条规则，覆盖匹配条件、端口、Host header、SNI 及其他规则保留；zone 暂停响应在测试内合成 |
| `aliyun-*.json` | [QueryDomainList](https://help.aliyun.com/zh/dws/developer-reference/api-domain-2018-01-29-querydomainlist)、[DescribeUserDomains](https://www.alibabacloud.com/help/en/cdn/developer-reference/api-cdn-2018-05-10-describeuserdomains)；合成两页域名及一页 CDN，覆盖两种到期格式与不一致状态类型 |
| `tencent-*.json` | [DescribeDomainNameList](https://cloud.tencent.com/document/product/242/48941)、[DescribeDomains](https://cloud.tencent.com/document/product/228/41118)；合成 Offset 0 / 1 与额外鉴权字段 |
| `baidu-*.json` | [BCD 列表](https://cloud.baidu.com/doc/BCD/s/4jwvymhs7)、[CDN 列表](https://cloud.baidu.com/doc/CDN/s/sjwvyewt1)；合成含 `+/=` 的 opaque marker、联系信息及未截断末页 |
| `ctyun-*.json` | [CDN 域名列表](https://www.ctyun.cn/document/10015932/10035101)；合成国内两页、其他范围空页，保留官方可选分页字段 |
| `dns.json` | 合成 A / AAAA / CNAME；测试转成 DNS wire 响应，使用保留地址段 |
| `whois.txt` | 合成常见 WHOIS 标签、虚构联系人和采集信息；只验证保留业务字段 |
| `ssh.txt` | 合成 `uname -srm` 三字段输出，由测试辅助进程回放 |

TLS 测试在运行时生成或使用 Go httptest 的测试证书，含过期和不受信任场景，不读取本机私钥。API 失败、凭据反射、Cloudflare 多页等响应直接在测试内合成。

v0.3 M2 的 `aliyun-origin*.json`、`tencent-origin.json`、`baidu-origin*.json`、`ctyun-origin.json`、`cloudflare-origin.json` 分别依据[回源接口核对](../../../docs/research/v0.3-cdn-origins.md)中链接的官方详情结构改写。均为虚构主备源，包含显式端口、协议及应剔除的鉴权对象；Cloudflare 只含单条无条件 Origin Rule。测试派生条件/区域、缺字段、畸形地址、API 错误、超大响应和取消场景，不读取真实账号。

`../signing_test.go` 的 ACS3、BCEv1 使用官方公开签名向量；TC3 使用官方请求形状与合成 testid / testsecret，EOP 使用全合成向量并以独立 Python / Node 计算交叉核对。来源和计算规则见 `docs/research/`，这些字符串不是可用凭据。
