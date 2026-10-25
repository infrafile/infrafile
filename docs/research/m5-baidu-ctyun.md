# M5：百度云、天翼云只读 API 研究

核对日期：2026-10-25。只读取官方公开文档、官方 SDK 源码；未访问云账号、凭据或私人资产，未请求任何业务云 API。下列 fixture 是官方公开响应示例的虚构值改写，**不是账号录制响应**。不新增 Go 依赖，不修改实现。

优先结论：天翼云**未找到公开可核验的已注册域名列表 API 契约**，M5 应将该能力标为 unsupported/未支持，不能用 DNS zone 或 CDN 加速域名代替。百度 BCD 有注册域名列表；百度国内 CDN、天翼 CDN 均有公开列表接口。权限缺口和证据边界见各节。

## 百度云

### 接口、分页与响应

| 能力 | HTTP endpoint / method / version | 分页与结束条件 | 最小读权限证据 |
| --- | --- | --- | --- |
| 已注册域名 BCD | `GET https://bcd.baidubce.com/v2/domain`；REST，无 Action 参数。官方请求模板是 `/v{version}/domain`，该列表的示例明确为 `/v2/domain`。[接口][B1] | 必填 `pageNo`、`pageSize`；示例从 1 开始；每页最多 100，漏分页参数或超限报错；响应 `totalCount`、`result[]`。建议按 `pageNo*pageSize >= totalCount` 结束，这是根据总数推导的客户端规则。[接口][B1] | 官方仅确认 BCD 为**服务级只读/运维/管理权限**，未在本次来源中找到列表级 action 或可确认的只读策略英文名称；不能编造 `ListDomains`/`BcdReadAccessPolicy`。[IAM支持范围][B4] |
| 国内 CDN | `GET https://cdn.baidubce.com/v2/domain`；REST v2，无 Action；不是海外 `/v2/abroad/domains`。[接口][B2]、[调用说明][B3] | `maxItems` 可选，1–2000，默认 200；首请求不带 `marker`，以后原样使用上页 `nextMarker`；`isTruncated=false` 才结束，末页不出现 `nextMarker`。[接口][B2] | `service=bce:cdn`、`region=*`、`permission=["QueryDomainList"]`、`resource=["domain/*"]`；服务级备选只读策略为 `CdnReadAccessPolicy`，范围更宽。[权限表及策略语法][B5] |

- BCD 可选过滤字段：`domain`、`domainPunycode`、`domainTld`、`resourceId`、`tagKey`、`tagValue`、`domainStatus`；全量列表不需要这些过滤。[接口][B1]
- BCD 字段表仅列 `domainName/status`，但公开响应实际为 `totalCount/result[]`，项目包含 `domainName`、`domain`、`resourceId`、`provider`、`dns[]`、`registerTime`、`expireTime`、`domainStatus` 等；实施应以响应示例结构为依据，不能期待顶层 `domains` 或只读 `status`。[接口][B1]
- BCD 时间示例为 UTC ISO 8601；`email/contactId/userId` 等也在原始响应里，库存映射只取业务需要的域名、注册商、日期、状态，避免复制整条原始对象。[接口][B1] 此处字段取舍是 M5 建议。
- BCD 存在 `403 AccessForbidden` 的 API 白名单限制，文档说明需通过工单申请；IAM 读权限不等于已获 API 使用资格。[API总述][B6]
- BCD 官方列表示例使用内部测试主机并出现 `Content-Length: -1`；不可把测试 host 或该非法长度照搬到实现。生产 host 来自请求结构 `bcd.baidubce.com`；本报告选择 HTTPS，未进行服务端 TLS/请求实测。[接口][B1]
- 国内 CDN 列表项只承诺 `domains[].name`，**没有 CNAME、状态、源站或注册到期日**；`origin` 是可选的源站筛选参数，不是列表响应字段。[接口][B2]
- 国内 CDN SDK 独立证实路径 `/v2/domain` 和 `isTruncated/domains/nextMarker` 结构；SDK helper 返回 nextMarker，但插件应遵守文档的 `isTruncated` 结束语义。[官方 Go SDK][B8]

### BCE v1 签名：Go 标准库可实现

以下同时按[官方规范及完整向量][B7]与[官方 Go signer][B9]核对；CDN 官方特别说明无需 region、建议 v1。[调用说明][B3]

1. `timestamp` 使用 UTC，格式 `2006-01-02T15:04:05Z`；`prefix = "bce-auth-v1/" + AK + "/" + timestamp + "/1800"`。
2. `signingKey = lowerHex(HMAC-SHA256(key=SK, data=prefix))`。**它是 64 字节十六进制文本；第二次 HMAC 不使用解码后的 32 字节。**
3. `canonicalURI`：路径以 `/` 开始；UTF-8 字节按 RFC 3986 编码，保留路径 `/` 和 unreserved 字符，百分号编码使用大写十六进制。
4. `canonicalQuery`：对每个 key/value 各编码一次，空值仍写 `key=`；排除 `authorization`，将编码后的 `key=value` 按字典序排序，以 `&` 拼接。marker 中的 `+`、`/`、`=` 必须编码；Go `url.QueryEscape` 的空格 `+` 要改为 `%20`。
5. 签名头名转小写、值 Trim，头名和值分别 URI 编码后用 `:` 连接，排序后用 `\n` 连接，**末尾不追加换行**。本次 GET 可显式签 `host;x-bce-date`；如果带 STS token，`x-bce-security-token` 也须加入签名。
6. `canonicalRequest = METHOD + "\n" + canonicalURI + "\n" + canonicalQuery + "\n" + canonicalHeaders`。没有额外的 body-hash 行；发送的 `x-bce-*` 头按规范纳入签名，`x-bce-request-id` 例外。
7. `signature = lowerHex(HMAC-SHA256(key=[]byte(signingKey), data=canonicalRequest))`；`Authorization = prefix + "/" + signedHeaders + "/" + signature`。GET 可空 body。
8. 标准库足够：`crypto/hmac`、`crypto/sha256`、`encoding/hex`、`net/url`、`sort`、`strings`、`time`、`net/http`。Go 的 Host 应取 `Request.Host` 或 `URL.Host`，不要只在 `Header` map 里查找。此项为 Go 实现建议。

官方公开完整向量（a/b 重复串是官方虚构密钥，不是账号凭据）：`AK="a"*32`、`SK="b"*32`、时间 `2015-04-27T08:23:49Z`、有效期 1800。以下 canonical request 无末尾换行：[向量来源][B7]

```text
PUT
/v1/test/myfolder/readme.txt
partNumber=9&uploadId=a44cc9bab11cbd156984767aad637851
content-length:8
content-md5:NFzcPqhviddjRNnSOGo4rw%3D%3D
content-type:text%2Fplain
host:bj.bcebos.com
x-bce-date:2015-04-27T08%3A23%3A49Z
```

预期 `signingKey=1d5ce5f464064cbee060330d973218821825ac6952368a482a592e6615aef479`；`signature=d74a04362e6a848f5b39b15421cb449427f419c95a480fd6b8cf9fc783e2999e`。官方 Authorization 的 signedHeaders 段留空表示默认头集，因此有 `1800//signature`；本次离线复算两值均相符。此向量只验证算法，不调用其 PUT API。[向量来源][B7]

### 可改写为 fixture 的公开 HTTP 响应

BCD：以下保留官方示例字段结构，域名、标识、日期、联系人均为虚构值；删除了原始传输长度等无关头。来源：[BCD列表响应][B1]。

```http
HTTP/1.1 200 OK
Content-Type: application/json;charset=UTF-8
x-bce-request-id: 00000000-0000-4000-8000-000000000001

{
  "totalCount": 1,
  "result": [{
    "userId": "fixture-user", "resourceId": "fixture-domain-1", "contactId": "fixture-contact",
    "domainName": "example.com", "domain": "example.com", "provider": "fixture-registrar",
    "dns": ["ns1.example.com", "ns2.example.com"],
    "registerTime": "2024-01-01T00:00:00Z", "expireTime": "2027-01-01T00:00:00Z",
    "auditStartTime": "2024-01-01T00:01:00Z", "auditStatus": "NONVERIFY", "realAuditStatus": "NONVERIFY",
    "expireStatus": "NORMAL", "emailVerifyStatus": "PENDING", "namingVerifyStatus": "PENDING",
    "privacyProtected": false, "beianStatus": "3", "domainStatus": "NONVERIFY",
    "privacySupported": true, "panelOpened": false, "auditEnabled": true,
    "renewEnabled": true, "userType": "INDIVIDUAL", "email": "owner@example.com"
  }]
}
```

国内 CDN：官方多页响应结构如下，marker 是虚构不透明串；实现不得自行解析 marker。[公开HTTP示例][B2]

```http
HTTP/1.1 200 OK
Content-Type: application/json; charset=utf-8

{"domains":[{"name":"www.example.com"},{"name":"static.example.com"}],"isTruncated":true,"nextMarker":"fixture+/=="}
```

文档契约派生的末页 fixture 可为 `{"domains":[{"name":"media.example.com"}],"isTruncated":false}`；空结果 BCD 可为 `{"totalCount":0,"result":[]}`。这两项是**合成边界用例**，并非官方原样响应。[列表契约][B1]、[CDN分页契约][B2]

验证建议：BCD 多页与 totalCount、CDN marker 编码及末页、HTTP 403/错误 JSON、BCE 官方签名向量；若 `isTruncated=true` 却缺少 nextMarker，或总数未到却提前空页，应报协议/不完整结果错误，不将不完整列表当作成功。错误体为 `requestId/code/message`。[BCD错误说明][B6]、[CDN错误说明][B3] 失败策略为 M5 建议。

[B1]: https://cloud.baidu.com/doc/BCD/s/4jwvymhs7
[B2]: https://cloud.baidu.com/doc/CDN/s/sjwvyewt1
[B3]: https://cloud.baidu.com/doc/CDN/s/rjwvyev26
[B4]: https://cloud.baidu.com/doc/IAM/s/yjxu3gfsl/
[B5]: https://cloud.baidu.com/doc/CDN/s/zk04vd3kw
[B6]: https://cloud.baidu.com/doc/BCD/s/pjwvymihl
[B7]: https://cloud.baidu.com/doc/Reference/s/njwvz1yfu
[B8]: https://github.com/baidubce/bce-sdk-go/blob/b6c858f4557821a495633a3c46f21989edbe9b7d/services/cdn/api/domain.go
[B9]: https://github.com/baidubce/bce-sdk-go/blob/b6c858f4557821a495633a3c46f21989edbe9b7d/auth/signer.go

## 天翼云

### 注册域名与 DNS 能力边界：不要以 zone 冒充 Domain

- **已注册域名：未找到公开可核验的列表 API。** 本次检查的新域名服务完整目录仅有产品介绍、购买、注册、续费、DNS、转移、过户等操作指南，未公开 endpoint、分页、签名或响应契约；结论是“本次无法据官方公开资料实施”，不是证明厂商内部不存在接口。[新域名服务目录][C1]
- **公网 DNS zone：本次也未找到公开列表 API 契约。** 云解析目录以控制台域名/解析记录操作为主；不能从控制台功能推导一个 HTTP API，更不能由解析区推导注册所有权。[云解析目录][C2]
- **内网 DNS zone：存在公开 API/官方 SDK。** 官方 API 使用说明链接到 OpenAPI 门户；官方 SDK 定义 `GET /v4/private-zone/new-list`，必填 `regionID`，可选 `zoneID/zoneName/pageNo/pageNumber/pageSize`；pageNo 推荐，默认 1，pageSize 默认 10、最大 50。响应为 `statusCode=800`、`returnObj.zones[].zoneID/name`、`totalCount/currentCount/totalPage`，属于私有解析区。[官方说明][C3]、[官方SDK][C4]
- 内网 DNS 仅用于确认能力边界，本次不纳入 M5 Domain；未继续核对其部署区域 endpoint/IAM 或构造 fixture。M5 建议明确输出 registered-domain 能力未支持，仅同步下述 CDN；不要用空域名数组暗示已成功列举注册资产。

### CDN 列表：endpoint、参数、成功与结束条件

- **`GET https://cdnapi-global.ctapi.ctyun.cn/domain/query-domain-list`**；REST 路径没有版本号，没有另传 `Action/Version` 的要求，GET 参数放 URL query、body 为空。[终端节点][C5]、[列表接口][C6]
- 分页实际字段是 **`page`、`page_size`**：默认 1 / 50，page_size 最大 100；不是 `pageNo/pageSize`、`pagesize` 或 `page_number`。[列表接口][C6]
- 可选 `domain`、`product_code`、`status`、`area_scope`；product_code 支持 `001/003/004/008/014`，status 1–12，不传查所有状态。[列表接口][C6]
- `area_scope` 为 1 国内、2 海外、3 全球，**缺省只查国内**。如目标是所有加速范围，需分别查询 1、2、3 并按业务标识去重；此为根据过滤语义推导的客户端方案，不能将省略参数解释为全范围。[列表接口][C6]
- 成功业务码 **`code=100000`**，不是通用 CTAPI 的 `statusCode=800`；响应顶层是 `code/message/total/total_count/page/page_size/page_count/result[]`。total 与 total_count 都是总条数，字段表将分页字段标为可选。[列表接口][C6]
- `result[]` 字段：`domain/cname/product_code/product_name/status/insert_date/area_scope/record_num`；insert_date 单位毫秒；没有注册商或注册到期日。CNAME 示例有末尾 `.`，应保留原值或明确统一规范。[列表接口][C6]
- 建议顺序递增 page，以 `page >= page_count` 结束；若未返回页数，以累计条数达到 total_count（或 total）判断。若总数字段均缺失，可用短页结束并明确这是客户端推导规则；有明确更多页却提前空页应失败。接口未规定独立游标。[列表接口][C6]

### 签名结论与可复算步骤

**该 endpoint 使用 CTAPI EOP HMAC-SHA256 签名，不应接入旧 APIKEY 方案。** 产品专属“签名&鉴权”明确列出同一 host 与 EOP 请求头；2026-07 更新的认证页也使用 AK/SK。未找到此 endpoint 应发送 APIKEY 的官方依据。[CDN签名][C7]、[新认证页][C8]

按[CDN签名文档][C7]和[官方 Go SDK request.go][C9]独立交叉核对（以下 HMAC 参数统一写 key 在前）：

1. 生成 `ctyun-eop-request-id` UUID；Go 可用 `crypto/rand` 生成 16 字节并设置 UUID v4 位，无需引入 uuid 依赖。
2. `eop-date` 格式 `20060102T150405Z`。官方 Go SDK 明确先转换为 **Asia/Shanghai（UTC+8）**；末尾 `Z` 为字面量，不能按 BCE 的 UTC 方式照搬。Go 可用 `time.FixedZone("CST", 8*3600)` 避免依赖本机 zoneinfo。
3. query 全部签名、按键排序，以 `key=value` 和 `&` 拼接；SDK 用 `url.Values.Encode()`，签名和实际发送复用同一 RawQuery。文档要求值 URI 编码；本次只传数字分页/area_scope，不涉及空格编码差异。[请求构造][C10]
4. `bodyHash = lowerHex(SHA256(rawBodyBytes))`。GET 空 body 是零字节，不是 `{}`、`null` 或换行；其哈希为 `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`。
5. `H = "ctyun-eop-request-id:"+id+"\n"+"eop-date:"+date+"\n"`；`S = H+"\n"+query+"\n"+bodyHash`。故 date 后有 **两个换行**，query 也为空时有三个；bodyHash 后无换行。默认签名不含 HTTP method、path、host。
6. `kTime = HMAC-SHA256(SK, date)`；`kAK = HMAC-SHA256(kTime, AK)`；`kDate = HMAC-SHA256(kAK, date[:8])`。**三级中间结果均为原始字节，不是 hex 文本**；最后一级数据只取 `yyyyMMdd`。
7. `signature = standardBase64(HMAC-SHA256(kDate, S))`，保留 Base64 padding；不是 base64(hex)、URL-safe Base64 或 BCE 两级 hex。
8. 发送 `Eop-Authorization: <AK> Headers=ctyun-eop-request-id;eop-date Signature=<signature>`，同时发出匹配的 `ctyun-eop-request-id`、`eop-date`。AK/Headers/Signature 之间各一个空格。
9. 标准库仅需 `crypto/hmac`、`crypto/sha256`、`crypto/rand`、`encoding/hex`、`encoding/base64`、`net/url`、`time`、`fmt`、`net/http`；以上 UUID/固定时区选择为 M5 建议，不导入官方 SDK。

官方示例限制：CDN 文档提供空 body 的 SHA256 和 canonical 串，但示例2前文时间 `160752`、最终字符串时间 `160930` 不一致；展示的最终 Authorization 没有配套 SK，**不是可复算的完整签名 oracle**。公式一处写 `hmacsha256(eop-date,kAk)`，正文说只取日期；官方 Go 的 `date[0:8]` 消除此歧义。[文档][C7]、[源码][C9]

补充离线合成向量（按上述官方算法计算，**不是厂商发布的完整向量，也不是账号请求**）：

```text
AK = aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
SK = bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
eop-date = 20220525T160752Z
ctyun-eop-request-id = 00000000-0000-4000-8000-000000000001
query = area_scope=1&page=1&page_size=100
body = <zero bytes>
S = ctyun-eop-request-id:00000000-0000-4000-8000-000000000001\neop-date:20220525T160752Z\n\narea_scope=1&page=1&page_size=100\ne3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
kTime(hex, display only) = be10da5b62c6ae09730619cf351974e705ec20178518e5042c0b442d109cd6a4
kAK(hex, display only) = f7c40f2e0f83bf78584763e64e9278496908b3189d630f976119f5779cda7e81
kDate(hex, display only) = 91547e90ed359f55a27bb04644d51fad124f438aa6d1d90c9d1950b46a5f1f02
signature = wHFhvGz6dxDK5GTV35/RLO6L2WNEXsOvxb7njKai5r0=
```

### IAM 与 fixture

- **API 最小只读 action 名称尚未确认。** 新版官方明确“控制台和 API 的权限独立设置”；`cdn viewer` 和 `ctcdn:domainManage:domainViewList` 属于控制台策略，不能写成已验证的 API 最小权限。[系统策略][C11]、[控制台自定义权限表][C12]
- 旧 CDN+IAM 文档的 `CDN只读角色` 也明确是控制台角色；工作区定制化角色只给出工单途径，没有列出本 API action。不可据此推荐管理员权限，或虚构 `cdn:ListDomains`。README 应保留 API 精确授权项待厂商确认的限制。[旧权限管理][C13]
- 官方公开列表响应可脱敏复用：下面 JSON 保留字段结构，改为一条虚构数据；HTTP 200/Content-Type 为本地测试包装，官方页面只展示 body，没有完整 HTTP 响应头。[响应来源][C6]

```http
HTTP/1.1 200 OK
Content-Type: application/json

{
  "code": 100000, "message": "success", "total": 1, "total_count": 1,
  "page": 1, "page_count": 1, "page_size": 50,
  "result": [{
    "area_scope": 1, "insert_date": 1704067200000,
    "domain": "www.example.com", "cname": "edge.example.com.",
    "record_num": "京ICP备00000000号-1", "product_code": "008",
    "product_name": "CDN加速", "status": 4
  }]
}
```

派生分页 fixture 应覆盖 page=1/2、page_size=1、total=total_count=2、page_count=2；状态 4 启用/6 停用、空 result、非成功 code、缺失可选总数字段。官方请求例 product_code=004 却返回 008，不能将该组合用作过滤逻辑的真实验收证据。[响应来源][C6]

[C1]: https://www.ctyun.cn/document/10025221/
[C2]: https://www.ctyun.cn/document/10000108/
[C3]: https://www.ctyun.cn/document/10026757/11059016
[C4]: https://github.com/ctyun-it/terraform-provider-ctyun/blob/16835f5c358a87cb6dcd35d8ccfb6ca330899bc5/internal/core/ctvpc/ctvpc_new_private_zone_list_api.go
[C5]: https://www.ctyun.cn/document/10015932/10048728
[C6]: https://www.ctyun.cn/document/10015932/10035101
[C7]: https://www.ctyun.cn/document/10015932/10035091
[C8]: https://www.ctyun.cn/document/10015932/10775643
[C9]: https://github.com/ctyun-it/terraform-provider-ctyun/blob/16835f5c358a87cb6dcd35d8ccfb6ca330899bc5/internal/core/ctyun-sdk-core/request.go
[C10]: https://www.ctyun.cn/document/10015932/10775652
[C11]: https://www.ctyun.cn/document/10015932/11076882
[C12]: https://www.ctyun.cn/document/10015932/11076883
[C13]: https://www.ctyun.cn/document/10015932/10621667

## 本次验证与交付边界

- 已用 Python 标准库离线复算百度官方完整向量，并计算天翼虚构输入的 EOP 向量，再用 Node crypto 独立复核 EOP 中间值及签名；核对官方 Go SDK 的字节/hex/Base64、日期、换行及 query 行为。
- 未实测云 API；fixture 需标注 public-doc-example/synthetic 来源，不能称为真实账号录制。注册域名/API IAM 缺失资料应保留为能力限制，不由实现猜测补齐。
- 仅新增本文，不新增 fixture 文件、实现、依赖、账号配置；主任务可据此编写离线 fixture 与签名测试。
