# M5：阿里云、腾讯云官方 API 核查

核查日期：2026-10-25。范围依据 `docs/plan/v0.1.md`；只研究公开官方文档和官方 SDK，不访问云账号、凭据或私人资产，不调用云业务 API，不修改实现或 Go 依赖。
`Domain` 在 `internal/ledger/schemas/domain.schema.json` 中明确表示注册域名。两家均有真正的注册域名列表 API；CDN 加速域名应单列，DNS 托管列表不能替代注册清单。
下文“建议”是据接口契约推导的实现选择；JSON 是官方公开响应示例的脱敏改写或明确标注的字段子集，均非现场 HTTP 录制。HTTP 状态和响应头没有录制证据，不把它们伪造为官方抓包。
所有业务示例使用 `example.com` 及虚构值；签名向量保留官方公开的测试值以便复算，它们不是有效凭据。正文中的 `\n` 表示单个 LF 字节。

## 阿里云

### 接口、分页、权限

| 用途 | HTTPS endpoint / method | Action / Version | 最小业务参数与解析路径 |
| --- | --- | --- | --- |
| 注册域名 | `POST https://domain.aliyuncs.com/` | `QueryDomainList` / `2018-01-29` | Query：`PageNum=1&PageSize=10`；`Data.Domain[]`。[A1] [A2] [A3] |
| CDN | `POST https://cdn.aliyuncs.com/` | `DescribeUserDomains` / `2018-05-10` | Query：`PageNumber=1&PageSize=100&CheckDomainShow=true`；`Domains.PageData[]`。[A4] [A5] [A6] |

- 两个官方 SDK 方法都设 `POST`、路径 `/`，业务参数放 `Query`，没有设置请求 body；`ReqBodyType=formData` 不是把这些 Query 参数移到 body 的理由。采用下述 ACS3 时公共 Action/Version 放请求头，`Accept: application/json`，空 body。[A3] [A6] [A7]
- 注册域名：`PageNum`、`PageSize` 必填；文档给出从 1 开始的请求例子，但**未公布 PageSize 最大值**，不能写成 100/500。全量查询不设 `QueryType`（其 1/2 仅查续费/赎回）、域名、日期、分组等过滤器。[A1] [A2]
- 返回分页字段为 `CurrentPageNum`、`PageSize`、`TotalPageNum`、`TotalItemNum`、`NextPage`、`PrePage`。建议以 `NextPage=false` 结束，每次递增本地请求 `PageNum`；官方示例 `CurrentPageNum=0`，不能据此改成从 0 请求，也不要用它直接生成下一页。[A1] [A2]
- 注册条目可取 `DomainName`、`InstanceId`、`RegistrationDate`、`ExpirationDate`、`ExpirationDateLong`。最后一个是 Unix **毫秒**，可用 `time.UnixMilli`；字符串无时区，旧示例还使用 `Nov 02,2019 04:00:45`，不能只假设 RFC3339。[A1] [A13]
- 当前文档字段表将 `Data` 描述成 array，但 JSON 和官方旧版 Go SDK 都是 `Data` 对象内的 `Domain` 数组；另外示例的 `DomainStatus` 等为数字、字段表/旧 SDK 为字符串。建议只解码列表所需的名称、ID、日期字段，不把不需要的状态字段绑成刚性类型。[A1] [A8] [A14]
- CDN：`PageNumber` 范围 1–100000；`PageSize` 默认 20、范围 1–500。返回 `PageNumber`、`PageSize`、`TotalCount`。建议以已覆盖条数或 `PageNumber*PageSize >= TotalCount` 结束；没有 next-token。[A4]
- CDN 条目包括 `DomainName`、数值 `DomainId`、`Cname`、`DomainStatus`、`CdnType`、`Coverage`、`SslProtocol`、创建/修改时间；源站为 `Sources.Source[]`，其中 `Port` 数字、`Priority`/`Weight` 字符串。`CheckDomainShow=true` 显式包含审核中/审核失败/配置失败域名，其余过滤器不设。[A4]
- 最小 RAM action：注册列表为 **`domain:QueryCommonInfo`**、资源 `*`，不是 `domain:QueryDomainList`；CDN 为 **`cdn:DescribeUserDomains`**，资源模板 `acs:cdn:*:{#accountId}:domain/*`。两者授权表均无关联操作；全账号扫描可用资源 `*`，不需要 `domain:*` 或 `cdn:*`。[A1] [A4]
- endpoint 范围：上表是中国区接入地址；Domain 文档明确杭州为 `domain.aliyuncs.com`；CDN 海外多个地域映射 `cdn.ap-southeast-1.aliyuncs.com`，不能擅自将中国 endpoint 当作所有站点的唯一选择。[A2] [A5]

### 当前推荐签名：ACS3-HMAC-SHA256

依据官方规范及 Go `GetAuthorization`/`SignatureMethod` 独立核对，以下全部可用 Go 标准库实现；新实现建议选 ACS3。旧 RPC 文档已标“不再推荐”，兼容细节留在下一节。[A7] [A9] [A10]

1. 设置 `host`、`x-acs-action`、`x-acs-version`、`x-acs-date`（UTC `2006-01-02T15:04:05Z`）、`x-acs-signature-nonce`；STS 时加 `x-acs-security-token` 并参与签名。nonce 可用 `crypto/rand` 生成字节再 hex，不必新增 UUID 库。[A7] [A9]
2. Query 按参数名排序，名称和值各自 UTF-8/RFC3986 编码，再以 `=`、`&` 连接。空格必须 `%20`，不是 `+`；Go `url.Values.Encode()` 后替换 `+` 为 `%20`，不二次编码整个 Query。[A9] [A10]
3. `payloadHash=hexLower(SHA256(bodyBytes))`，写入 `x-acs-content-sha256`。本任务两个请求 body 为空，哈希是 `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`。[A9]
4. 纳入 `host`、所有 `x-acs-*`、以及实际存在的 `content-type`；仅头名称转小写，值去首尾空格、**保留值大小写**。按小写头名排序，连接成 `key:value\n`，末尾也有 LF；同序头名以分号组成 `SignedHeaders`。[A9] [A10]
5. `CanonicalRequest=method+"\n"+"/"+"\n"+query+"\n"+canonicalHeaders+"\n"+signedHeaders+"\n"+payloadHash`。headers 末尾 LF 加分隔 LF，因此存在空行。[A9] [A10]
6. `StringToSign="ACS3-HMAC-SHA256\n"+hexLower(SHA256(CanonicalRequest))`；`signature=hexLower(HMAC-SHA256(AccessKeySecret, StringToSign))`。密钥直接使用原文，不追加 `&`，不做 TC3 的日期派生。[A9] [A10]
7. `Authorization: ACS3-HMAC-SHA256 Credential=<id>,SignedHeaders=<names>,Signature=<hex>`。签名后不得改 Query、签名头或 body；Go 的 Host 来自 `req.URL.Host`/`req.Host`，不能只遍历 `req.Header`。[A9] [A10] [Go Request 官方定义](https://pkg.go.dev/net/http#Request)

官方固定向量（仅离线计算，示例中的写操作绝不发送）：`POST /`，host=`ecs.cn-shanghai.aliyuncs.com`，Action=`RunInstances`，Version=`2014-05-26`，ID/secret=`YourAccessKeyId`/`YourAccessKeySecret`，date=`2023-10-26T10:22:32Z`，nonce=`3156853299f313e23d1673dc12e1703d`，空 body，不设 Content-Type，Query 如下。[A9]

```text
ImageId=win2019_1809_x64_dtc_zh-cn_40G_alibase_20230811.vhd&RegionId=cn-shanghai
SignedHeaders=host;x-acs-action;x-acs-content-sha256;x-acs-date;x-acs-signature-nonce;x-acs-version
SHA256(CanonicalRequest)=7ea06492da5221eba5297e897ce16e55f964061054b7695beedaac1145b1e259
Signature=06563a9e1b43f5dfe96b81484da74bceab24a1d853912eee15083a6f0f3283c0
```

已用 Python 标准库在内存离线复算，两个结果与官方一致；Go 官方实现使用相同 SHA256/HMAC/hex 步骤。**官方最后的 HTTP 展示例误换 date 为 `09:01:01Z`、nonce 为另一值；golden 应用前面的固定参数与 CanonicalRequest，不能混搭。**[A9] [A10]

### 旧 RPC V2 签名兼容资料

- `SignatureMethod=HMAC-SHA1`、`SignatureVersion=1.0`、`AccessKeyId`、`Action`、`Version`、`Format=JSON`、UTC `Timestamp`、`SignatureNonce`，STS 再加 `SecurityToken`。合并 Query 和 form 参数，排除 `Signature` 本身，按键排序并 RFC3986 编码。[A11] [A12]
- `StringToSign=METHOD+"&%2F&"+percentEncode(canonicalQuery)`；HMAC-SHA1 的 key 为 `secret+"&"`，原始摘要用标准 Base64，再将 `Signature` 当普通参数 URL 编码。此处整个 Query **需要第二次编码**，与 ACS3 不同；`GET` 和 `POST` 不能共用签名。[A11] [A12]
- 官方向量：method=`GET`，`AccessKeyId=testid`、secret=`testsecret`、`Action=DescribeDedicatedHosts`、`Version=2014-05-26`、`Format=JSON`、`RegionId=cn-beijing`、nonce=`edb2b34af0af9a6d14deaf7c1a5315eb`、`Timestamp=2023-03-13T08:34:30Z`，加上述签名 method/version；预期 `9NaGiOspFP5UPcwX8Iwt2YJXXuk=`，本次离线复算一致。[A11]

### 可脱敏的公开响应 fixture

注册域名：下面是 [A1] 当前 JSON 示例的**所需字段子集**，保持 `Data.Domain` 结构；数值时间为虚构的 2026/2030-01-01 UTC。其余字段不应为了 fixture 完整而复制持有人资料。

```json
{
  "PrePage": false, "CurrentPageNum": 1, "RequestId": "example-domain-request",
  "PageSize": 10, "TotalPageNum": 1, "TotalItemNum": 1, "NextPage": false,
  "Data": {"Domain": [{
    "DomainName": "example.com", "InstanceId": "example-domain-001", "DomainType": "gTLD",
    "RegistrationDate": "2026-01-01 00:00:00", "RegistrationDateLong": 1767225600000,
    "ExpirationDate": "2030-01-01 00:00:00", "ExpirationDateLong": 1893456000000
  }]}
}
```

CDN：按 [A4] 的完整公开成功示例改写，调整总数为 1，避免将原示例 `TotalCount=2` 但只显示一个条目误当终页。

```json
{
  "RequestId": "example-cdn-request", "PageNumber": 1, "PageSize": 100, "TotalCount": 1,
  "Domains": {"PageData": [{
    "SslProtocol": "on", "Sandbox": "false", "GmtModified": "2026-01-02T00:00:00Z",
    "DomainName": "cdn.example.com", "DomainId": 10001, "GmtCreated": "2026-01-01T00:00:00Z",
    "Description": "example", "Coverage": "domestic", "ResourceGroupId": "example-group",
    "Cname": "cname.example.com", "DomainStatus": "online", "CdnType": "web",
    "Sources": {"Source": [{"Type": "domain", "Weight": "10", "Priority": "20", "Port": 443, "Content": "origin.example.com"}]}
  }]}
}
```

实现测试建议：由以上示例明确派生空列表、两页、`NextPage=true` 却空页、`CurrentPageNum=0` 和无效 JSON；前两项验证分页，矛盾分页应报不完整而非覆盖旧快照。错误响应必须先检查 HTTP 状态及 API 错误，不能解码成空成功；官方 SDK 对 HTTP 4xx/5xx 读取 `Code`/`Message`/`RequestId`。[A7]

[A1]: https://help.aliyun.com/zh/dws/developer-reference/api-domain-2018-01-29-querydomainlist
[A2]: https://help.aliyun.com/en/dws/developer-reference/call-openapi
[A3]: https://github.com/alibabacloud-go/domain-20180129/blob/master/client/client.go
[A4]: https://www.alibabacloud.com/help/en/cdn/developer-reference/api-cdn-2018-05-10-describeuserdomains
[A5]: https://help.aliyun.com/zh/cdn/developer-reference/api-cdn-2018-05-10-endpoint
[A6]: https://github.com/alibabacloud-go/cdn-20180510/blob/master/client/client.go
[A7]: https://github.com/alibabacloud-go/darabonba-openapi/blob/master/client/client.go
[A8]: https://github.com/aliyun/alibaba-cloud-sdk-go/blob/master/services/domain/struct_data_in_query_domain_list.go
[A9]: https://www.alibabacloud.com/help/zh/sdk/product-overview/v3-request-structure-and-signature
[A10]: https://github.com/alibabacloud-go/openapi-util/blob/master/service/service.go
[A11]: https://www.alibabacloud.com/help/zh/sdk/product-overview/rpc-mechanism
[A12]: https://github.com/aliyun/alibaba-cloud-sdk-go/blob/master/sdk/auth/rpc_signature_composer.go
[A13]: https://www.alibabacloud.com/help/en/dws/developer-reference/api-querydomainlist
[A14]: https://github.com/aliyun/alibaba-cloud-sdk-go/blob/master/services/domain/struct_domain.go

## 腾讯云

### 接口、分页、权限

| 用途 | HTTPS endpoint / method | X-TC-Action / X-TC-Version | JSON body / 返回列表 |
| --- | --- | --- | --- |
| 注册域名 | `POST https://domain.tencentcloudapi.com/` | `DescribeDomainNameList` / `2018-08-08` | `{"Offset":0,"Limit":100}`；`Response.DomainSet[]`。[T1] [T4] |
| CDN 最小列出 | `POST https://cdn.tencentcloudapi.com/` | `DescribeDomains` / `2018-06-06` | `{"Offset":0,"Limit":100}`；`Response.Domains[]`（BriefDomain）。[T2] [T5] |
| CDN 详细配置，可选 | 同上 | `DescribeDomainsConfig` / `2018-06-06` | 同上；`Response.Domains[]`（DetailDomain）。[T3] |

- **确有注册域名列表**：`domain` 产品的 `DescribeDomainNameList` 返回注册/到期日期、自动续费及购买状态。DNSPod 是另一个产品，`dnspod.tencentcloudapi.com` 的 `DescribeDomainList`、版本 `2021-03-23` 是 DNS 托管列表，不可替代。[T1] [T6] [T7]
- 三接口均不需 Region。使用 TC3 POST JSON；公开接口页的 GET 示例是旧签名示例，官方公共参数页明确说明支持 POST JSON。请求头带 `X-TC-Action`、`X-TC-Version`、秒级 `X-TC-Timestamp` 和 `Authorization`；STS 时带 `X-TC-Token`，长期密钥不设 token。[T1] [T2] [T3] [T4]
- 注册分页：`Offset` 默认 0；`Limit` 默认 20，范围 1–100；总数 **`Response.TotalCount`**。字段：`DomainId`、`DomainName`、`CreationDate`、`ExpirationDate`、数值 `AutoRenew`、布尔 `IsPremium`、`Tld`、`CodeTld`、`BuyStatus`。[T1] [T6]
- `BuyStatus` 包含注册中、注册失败、转入中等状态，不能把每个条目解释成“注册已成功且当前有效”。日期类型说明是 `YYYY-MM-DD HH:mm:ss`，官方响应示例却是 `YYYY-MM-DD`；建议保留原值，转换账本日期时接受这两种明确格式，不凭空添加时区。[T1] [T6]
- CDN 简表：`Offset` 默认 0；`Limit` 默认 100、最大 1000；详表默认 100、最大 **100**。两者总数字段都是 **`Response.TotalNumber`**，不是注册 API 的 TotalCount；均可不带 Filters，全量列出。详表另可带 Sort，最小列出不需要。[T2] [T3]
- 根据 offset/total 契约推导：每页处理 `n=len(items)`，`Offset+n >= total` 时结束，否则 `Offset += n` 继续；不要只按短页判终止。若 `n=0` 但 total 仍显示有未读记录，报不完整，不能无限循环或把部分清单当全量。[T1] [T2] [T3]
- CDN 简表已有 `ResourceId`、`Domain`、`Cname`、`Status`、`ServiceType`、`Area`、`CreateTime`、`UpdateTime`、`Origin`。`Origin.BackupOriginType`、`BackupServerName` 等可 null。M5 建议只取名称、ID、CNAME、状态等必要字段，**无需调用 DescribeDomainsConfig**。[T2] [T5]
- 详表可包含 `Authentication.TypeA.SecretKey` 等鉴权配置；即使是只读也不应原样写入 Snapshot。若实现后来确需详表，只映射允许的字段，单独声明增加的权限。[T3] [T5]
- 最小 CAM action：`domain:DescribeDomainNameList`、`cdn:DescribeDomains`；只有选择详表时才需 `cdn:DescribeDomainsConfig`。按 CAM API action 前缀也可写 `name/domain:DescribeDomainNameList`、`name/cdn:DescribeDomains`；以产品 action 表与 CAM 语法组合，不授予 `Describe*` 通配符。[T8] [T9] [T10]
- 资源：注册 action 行是 `qcs::domain::uin/${uin}:domainId/*`，CDN 为 `qcs::cdn::uin/${uin}:domain/${domain}`；全量账号清单可用 `resource:["*"]` 或账号下资源通配。注册 CAM 页总览写“操作级”而该 action 行写“资源级”，因此**资源级策略未实测**；读取范围受授权限制，不能把它等同账号所有资产。[T8] [T9] [T10] [T11]

### TC3-HMAC-SHA256：标准库实现步骤

官方签名规范、独立 Go 演示和 Go SDK 的签名实现均已核对。签名服务名分别为 `domain`、`cdn`，与 API 版本和 Action 不同。[T4] [T12] [T13] [T14]

1. JSON 先序列化一次，固定待发送的 `[]byte`；`Content-Type: application/json; charset=utf-8`。`CanonicalURI="/"`、POST 的 `CanonicalQueryString=""`，`payloadHash=hexLower(SHA256(bodyBytes))`，空格和 Unicode 转义差异会改变哈希。[T12] [T13]
2. 本报告向量签 `content-type;host;x-tc-action`；头名称和值按官方规则转小写并去首尾空格，头按名称排序，逐条 `key:value\n`，包含末尾 LF。网络 Action 仍发 `DescribeDomainNameList`/`DescribeDomains`，签名中的 Action 值为小写。[T12] [T13]
3. `CanonicalRequest="POST\n/\n\n"+canonicalHeaders+"\n"+signedHeaders+"\n"+payloadHash`。官方 Go SDK 也有只签 `content-type;host` 的合法路径；两种集合不可混用同一预期签名。[T12] [T14]
4. `date=time.Unix(timestamp,0).UTC().Format("2006-01-02")`；`scope=date+"/"+service+"/tc3_request"`；`StringToSign="TC3-HMAC-SHA256\n"+timestamp+"\n"+scope+"\n"+hexLower(SHA256(CanonicalRequest))`。[T12] [T13]
5. 定义 `H(key,data)=HMAC-SHA256(key,data)` 原始 32 字节：`kDate=H("TC3"+secret,date)`、`kService=H(kDate,service)`、`kSigning=H(kService,"tc3_request")`、`signature=hexLower(H(kSigning,StringToSign))`。**中间密钥不要 hex/Base64**。[T12] [T13]
6. `Authorization: TC3-HMAC-SHA256 Credential=<id>/<scope>, SignedHeaders=<names>, Signature=<hex>`。时间戳必须是秒且与 UTC 日期一致；官方允许时间偏差不超过 5 分钟。本任务不发 `X-TC-Region`。[T4] [T12]

官方演示向量：POST，host=`cvm.tencentcloudapi.com`，service=`cvm`，Action=`DescribeInstances`，Version=`2017-03-12`，timestamp=`1551113065`，scope=`2019-02-25/cvm/tc3_request`；Content-Type 和 SignedHeaders 同上，body **恰为下面一行，无行尾 LF**。[T12] [T13]

```json
{"Limit": 1, "Filters": [{"Values": ["\u672a\u547d\u540d"], "Name": "instance-name"}]}
```

```text
SHA256(body)=35e9c5b0e3ae67532d3c9f17ead6c90222632e5b1ff7f6e89887f1398934f064
SHA256(CanonicalRequest)=7019a55be8395899b900fb5564e4200d984910f34794a27cb3fb7d10ff6a1e84
```

以上两项已离线复算并与官方一致。当前官方文档/Go 演示已掩码原 SecretKey，页面最终签名不能当作可复算的完整 golden。补充向量**沿用官方输入，仅改成虚构 `secret=testsecret`、`id=testid`**，本次离线算得 `5783a974073f79ab46334c07e383b65090ab676d96e986a76dab65af9957b106`；这是研究推导结果，不冒称官方给定签名。[T12] [T13]
Go 仅需 `crypto/hmac`、`crypto/sha256`、`encoding/hex`、`encoding/json`、`net/http`、`strings`、`time` 等标准库；官方 Go 演示中的 HMAC 返回原始字节字符串，最终才 hex，独立验证了上面步骤无需新增依赖。[T13]

### 可脱敏的公开响应 fixture

注册域名：按 [T1] 完整公开成功示例改写，保留其只有日期的格式与数字/布尔类型。

```json
{
  "Response": {
    "TotalCount": 1,
    "DomainSet": [{
      "AutoRenew": 0, "IsPremium": false, "DomainId": "domain-example001",
      "ExpirationDate": "2030-01-01", "DomainName": "example.com", "CodeTld": "com",
      "CreationDate": "2026-01-01", "Tld": ".com", "BuyStatus": "ok"
    }],
    "RequestId": "example-domain-request"
  }
}
```

CDN：按 [T2] 完整公开成功示例改写；原示例总数 201/单条是分页示例，下面总数设 1 表示单页。保留 null/空数组及 Origin 嵌套。

```json
{
  "Response": {
    "RequestId": "example-cdn-request",
    "Domains": [{
      "Area": "mainland", "Cname": "cname.example.com", "CreateTime": "2026-01-01 00:00:00",
      "Disable": "normal", "Domain": "cdn.example.com", "Product": "cdn", "ParentHost": "",
      "Origin": {
        "Origins": ["origin.example.com"], "OriginType": "domain", "ServerName": "origin.example.com",
        "CosPrivateAccess": "off", "OriginPullProtocol": "http", "BackupOrigins": [],
        "BackupOriginType": null, "BackupServerName": null
      },
      "ProjectId": 0, "Readonly": "normal", "ResourceId": "cdn-example001",
      "ServiceType": "web", "Status": "offline", "UpdateTime": "2026-01-02 00:00:00"
    }],
    "TotalNumber": 1
  }
}
```

错误不能只按 HTTP 状态判断：官方 SDK 自测包含 **HTTP 200 + `Response.Error.Code=RequestLimitExceeded`**。建议补这种错误、403、空列表、两页及坏 JSON fixture；错误形状为 `{"Response":{"Error":{"Code":"RequestLimitExceeded","Message":"example error"},"RequestId":"example-request"}}`，其中 Message/RequestId 是补充的虚构测试值。[T15]
本次只做文档/源码核对和离线签名复算；未运行插件实现、没有真实 HTTP 录制、没有云账号 IAM 验收。公开示例可作合成 fixture 来源，但不能据此声称 M5 的真实录制来源或在线验收已完成。

[T1]: https://cloud.tencent.com/document/product/242/48941
[T2]: https://cloud.tencent.com/document/product/228/41118
[T3]: https://cloud.tencent.com/document/product/228/41117
[T4]: https://cloud.tencent.com/document/api/242/38887
[T5]: https://cloud.tencent.com/document/api/228/30987
[T6]: https://github.com/TencentCloud/tencentcloud-sdk-go/blob/master/tencentcloud/domain/v20180808/models.go
[T7]: https://cloud.tencent.com/document/product/1427/56172
[T8]: https://cloud.tencent.com/document/product/598/86259
[T9]: https://cloud.tencent.com/document/product/598/98110
[T10]: https://cloud.tencent.com/document/product/598/10603
[T11]: https://indonesia.tencentcloud.com/zh/document/product/228/35229
[T12]: https://cloud.tencent.com/document/api/242/38888
[T13]: https://github.com/TencentCloud/signature-process-demo/blob/main/signature-v3/golang/demo.go
[T14]: https://github.com/tencentcloud/tencentcloud-sdk-go/blob/master/tencentcloud/common/client.go
[T15]: https://github.com/tencentcloud/tencentcloud-sdk-go/blob/master/tencentcloud/common/client_test.go
