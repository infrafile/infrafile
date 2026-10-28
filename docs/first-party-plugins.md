# 第一方只读插件

九个插件随二进制内置，通过同一条 `sync` 命令执行。每个实例产生独立的 `state/<插件>/<实例>.yaml`，只更新机器观测，不修改 `inventory/`。配置、超时、失败保留旧快照和退出码沿用[插件宿主](plugins.md)。

用 `infrafile capabilities` 查看登记、观测、比较、计划、执行、回读范围；`infrafile capabilities --json` 提供字段、权限前提、条件和验证等级，MCP 的 `infrafile_capabilities` 返回同一目录。该查询离线且不需要数据仓，不执行插件或检查当前账号；[能力契约](json/capabilities.md)说明状态含义与旧插件兼容边界。

## 配置与凭据

完整的虚构配置见 [homelab 示例](../examples/homelab/first-party.example.yaml)。只把需要的实例复制到自己的 `infrafile.yaml`：

```yaml
apiVersion: infrafile/v1alpha1
kind: Config
plugins:
  aliyun:
    instances:
      home:
        access_key_id: env://ALIYUN_ACCESS_KEY_ID
        access_key_secret: sops://secrets/cloud.enc.yaml#aliyun.secret
        services: [domains, cdn]
  cloudflare:
    instances:
      home:
        api_token: op://Example/cloudflare/api_token
```

```bash
infrafile validate /path/to/my-infra
infrafile sync aliyun --dir /path/to/my-infra --timeout 60s --json
```

云插件的所有认证字段都必须是 `env://`、`sops://` 或 `op://` 引用，不能填明文。SOPS 路径相对数据仓；引用支持范围见[凭据指南](secrets.md)。只有显式执行相应 `sync` 才解析值。`secret check` 目前检查 inventory 的 `spec.secrets`，不会预检插件配置中的引用；普通 `validate` 会检查配置里的明文风险。

SOPS age 密钥命令受实例取消信号控制；仅支持非交互身份，不执行外部 age 插件或身份文件的口令交互。凭据解析超时也会保留该实例旧快照，并继续其他实例。

| 插件 | 必填字段 | 可选字段 |
|---|---|---|
| `cloudflare` | `api_token` | `account_id` 限定账号；v0.3 可选 `collect_origin` |
| `aliyun`、`tencent`、`baidu` | `access_key_id`、`access_key_secret` | `services`，默认 `[domains, cdn]`；`session_token` 临时凭据引用；v0.3 可选 `collect_origin` |
| `ctyun` | `access_key_id`、`access_key_secret` | `services` 仅允许 `[cdn]`（默认）；不支持 `session_token`；v0.3 可选 `collect_origin` |

未知字段、空服务列表、重复或未支持的服务返回 `config` 错误。选择单项服务时，只调用对应 API。每个实例所有服务共用 `--timeout` 时限；任一服务失败，该实例整体失败并保留上次快照。当前不自动重试。API 地址固定，拒绝重定向，单页响应最多 4 MiB。

## 云清单与观测字段

权限见 [README 权限表](../README.md#第一方插件与最小权限)。清单仅覆盖当前凭据有权访问的资源；权限限制可能使供应商返回较小的集合，不应把清单差异直接视为资源删除。

| 插件 | API 与分页 | 快照 `spec` |
|---|---|---|
| Cloudflare | `/zones`，每个 zone 的 `/dns_records`；page / total_count | `zones[]`：id、name、status、paused、dns_records；记录有 id、name、type、content、ttl、priority（若有）、proxied、proxiable |
| 阿里云 | `QueryDomainList`（2018-01-29，PageNum / NextPage）；`DescribeUserDomains`（2018-05-10，PageNumber / TotalCount） | `domains[]` 的 name、id、expires_at；`cdn[]` 的 name、id、cname、status、type、area |
| 腾讯云 | `DescribeDomainNameList`（2018-08-08）与 `DescribeDomains`（2018-06-06）；Offset / TotalCount 或 TotalNumber | `domains[]`、`cdn[]`，只保留下述公共字段 |
| 百度云 | BCD `GET /v2/domain`，pageNo / totalCount；CDN `GET /v2/domain`，marker / isTruncated | `domains[]`；CDN 列表仅提供域名，因此 `cdn[]` 只有 name |
| 天翼云 | EOP `GET /domain/query-domain-list`，page / page_size；分别查询 area_scope 1、2、3 并去重 | `cdn[]` 的 name、cname、status、type、area；另有 `unsupported: [registered-domains]` |

公共域名字段为 `name`、可选 `id` / `status` / `expires_at`；公共 CDN 字段为 `name`、可选 `id` / `cname` / `status` / `type` / `area`。供应商未提供的字段省略，状态和产品类型保留供应商语义，不跨厂商强行归一。到期日为 `YYYY-MM-DD`；阿里云优先使用毫秒时间戳并按北京时间取日期，与供应商的到期日字符串保持一致。ID 输出字符串。列表稳定排序；采集时间只写 `_sync.yaml`。

Cloudflare 显式请求 full、partial、secondary、internal zone 类型；DNS 内容和代理状态属于观测数据。阿里云使用中国站 domain / CDN 地址。天翼的 `area` 映射为 domestic / overseas / global；数值 status 转成字符串，type 保留 product_code。不把 DNS zone 或 CDN 域名冒充已注册域名。注册域名查询只列当前云账号的注册清单，不查询全网注册信息。

认证只在内存中用于 Bearer、ACS3、TC3、BCEv1 或 EOP 请求；不保存原始响应、联系信息、回源鉴权和请求日志。写入前仍执行宿主 deny-list，并拒绝结果中出现本次解析的完整认证值。未知字段名下的未知秘密仍不能靠 deny-list 完整识别。

## 回源采集（v0.3）

五家云实例可显式配置布尔 `collect_origin: true`；国内云同时需要 cdn 服务。默认关闭，关闭时请求与权限保持原清单行为。开启后逐条写入 `cdn[].origin` 或 `zones[].origin`。新增只读权限、请求预算和各厂商字段限制见[接口核对](research/v0.3-cdn-origins.md)；不要沿用只具备列表权限的令牌假定采集会成功。

例如以下两段分别位于 `infrafile.yaml` 与 `inventory/resources/edge.yaml`：

```yaml
apiVersion: infrafile/v1alpha1
kind: Config
plugins:
  aliyun:
    instances:
      home:
        access_key_id: env://ALIYUN_ACCESS_KEY_ID
        access_key_secret: env://ALIYUN_ACCESS_KEY_SECRET
        services: [cdn]
        collect_origin: true
audit:
  watch:
    - {target: Resource/edge, plugin: aliyun, instance: home, service: cdn}
```

```yaml
apiVersion: infrafile/v1alpha1
kind: Resource
metadata: {name: edge}
spec:
  provider: aliyun
  type: cdn
  resource_id: '10001' # 替换为已审核清单中的真实 ID
  endpoint: static.example.com
  origin:
    hosts: [origin.example.com, 192.0.2.1]
    port: 8443
    protocol: https
```

有 resource_id 的观测要求台账登记匹配的 ID；只有无 ID 的观测才按 endpoint 后备匹配。Cloudflare 使用 provider=cloudflare、type=dns-zone、zone ID，watch.service=zones。

先执行 `infrafile sync aliyun --dir /path/to/my-infra --timeout 60s --json` 并确认成功，再执行 `infrafile audit /path/to/my-infra --rule cdn_drift --max-age 7d --json`。audit 全程离线。期望由人维护，`import state` 不自动填充 origin。

每实例最多 200 个条目；任意详情超时、403、业务错误或超限都使该实例失败，保留旧快照。Cloudflare 没有 Origin Rules entrypoint 时的 404 也视为失败，不冒充空配置。已成功采集但条件/区域复杂、源站字段缺失或端口/协议不可观测时保存未知，在 audit 中输出 cdn_origin_unknown；ok=true 不等于配置全部符合期望。字段、证据与豁免见 [audit 契约](json/audit.md#cdn-回源漂移v03)。

`spec.origin`、collect_origin 不适用于 v0.2 的校验/插件配置；回退前移除这两个字段及 v0.3 的 watch 配置。v0.3 的回源只读映射尚未进行真实账号验收，不等同于下面已完成的 v0.2 清单验收。

## DNS、TLS、WHOIS 与 SSH

| 插件 | 配置 | 结果与边界 |
|---|---|---|
| `dns` | `host`：ASCII 域名，国际域名先转 punycode | 系统解析器查询 A / AAAA 与 CNAME；`host`、排序去重的 `addresses`、`canonical_name`。不是全部 DNS 记录导出，不查询 MX / TXT |
| `tls` | `host`：域名或 IP；`port` 默认 443 | TLS 1.2+ 握手后断开，不发送 HTTP；返回证书 subject、issuer、dns_names、not_before、not_after、sha256，以及 `verified` / `verification` |
| `whois` | `domain`；可选 `server`（主机名或主机名:端口，默认 43） | 默认先查询 IANA，再查询它指向的 WHOIS 服务；返回 domain、server、found、registrar、expires_at、nameservers、statuses 中可获得的字段 |
| `ssh` | `host`；可选 `user`、`port`、`identity_file`、`known_hosts_file` | 系统 OpenSSH 执行固定 `uname -srm`，返回 host、system、release、architecture；不提供任意命令入口 |

TLS 为了记录过期证书，会先观察对端证书，再用系统信任库、主机名及证书链验证。`verified: false` 表示未通过信任验证，即使 `sync` 成功。`verification` 为 valid、untrusted、hostname-mismatch 或 expired-or-not-yet-valid；证书自身有效期错误优先呈现。观测成功不等于服务可信。

WHOIS 只解析常见域名、到期日、注册商、NS 和状态字段，不保存联系人或原文。响应没有与查询域名匹配的 Domain Name 行、且某行以无匹配标记（如 `No match for`、`NOT FOUND`）开头时返回 `found: false`，字段值里出现这类字样不算；不可识别的响应、日期或未匹配域名报错。它不继续追踪注册商 referral，也没有 RDAP 回退；已关闭 WHOIS 的注册局可能无法查询。到期日未公开时省略，不推算。

SSH 需要本机 `ssh` 与目标的类 Unix `uname`。开启 BatchMode、严格主机密钥验证，关闭密钥自动更新、交互、PTY、代理/X11/端口转发、连接复用和 LocalCommand；继承用户可信 SSH 配置与已有认证选择（包括配置中的 ProxyJump / ProxyCommand）。私钥由 OpenSSH 读取，不从凭据引用写出临时私钥。显式文件路径相对数据仓或使用绝对路径；不展开 `~`。未指定 user / port 时沿用 SSH 配置。单次连接超时 10 秒，整个实例仍受 `--timeout` 控制；SSH 原始 stderr 不返回。

DNS 使用运行机器的系统解析器；TUN/fake-IP 代理可能返回虚拟地址，即使 sync 成功，也不能将其视为真实公网解析。应在干净出口执行探测，或用已验证的外部采集器生成带真实观测时间的快照。切勿给历史快照改时间来假装刷新。

阿里云 CDN 原始清单可能用 `.example.com` 表示泛域名；state 保留原文，导入和审计在共享映射中归一成 `*.example.com`，不会当成根域名。注册域名和其他供应商的校验边界不变。

## 验证范围

云插件回放[公开样例改写及合成夹具](../internal/plugin/testdata/README.md)，覆盖分页、业务错误、HTTP 错误、缺失字段、取消、凭据反射拒绝与 CLI 输出。签名测试使用官方向量或独立计算的合成向量。探测测试使用内存 DNS、本地 TLS / WHOIS 和模拟 SSH 进程，无需云账号、用户密钥或真实服务器。

v0.2 已在使用方私有环境验证真实 SOPS 引用、Cloudflare zone/DNS 与四家国内云 CDN 只读清单、TLS、WHOIS 及固定 SSH uname 探测；空清单只验证到有权查询，不证明非空资源的全部边界。原始快照和验收投影留在使用方私有数据仓，不作为公开测试夹具。注册域名列表 API、真实最小 IAM 策略和线上写入仍未验收；天翼注册域名暂不支持，百度 BCD 和天翼精确 API 授权仍有公开文档缺口。官方来源、签名细节与权限证据见[阿里云／腾讯云调研](research/m5-aliyun-tencent.md)及[百度／天翼调研](research/m5-baidu-ctyun.md)。
