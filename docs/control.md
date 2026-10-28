# 受控写与 DNSControl

M7 提供 `plan`、`plan show`、`apply` 和 `dnscontrol`。计划、执行和 DNS 配置生成均先校验数据仓；`plan show` 只读计划文件。不会修改 inventory 或 state，也不自动提交 Git。

## 绑定控制目标

控制命令只接受未归档的 `Resource/<名称>`，资源类型为 `cdn` 或 `dns-zone`。在 Resource 中明确绑定插件、已启用的实例和远端标识，`provider` 必须等于插件名：

```yaml
apiVersion: infrafile/v1alpha1
kind: Resource
metadata: {name: edge}
spec:
  provider: cloudflare
  type: cdn
  endpoint: assets.example.com
  control:
    plugin: cloudflare
    instance: main
    zone_id: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    ruleset_id: bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
    rule_id: cccccccccccccccccccccccccccccccc
```

以上 ID 均为虚构。暂停和恢复只需要 `zone_id`；改回源还需要一个已部署的 Origin Rules ruleset 及其中已启用的 `route` 规则。插件配置与 sync 相同：

```yaml
plugins:
  cloudflare:
    instances:
      main:
        api_token: env://CF_CONTROL_TOKEN
        # account_id: 自己的账号 ID（填写后会校验 zone 归属）
```

2026-10-25 确认的边界：暂停／恢复作用于**整个 zone**，对应 Cloudflare 的 `paused`；暂停后该 zone 不再享有代理的安全与性能能力，并可能暴露源站。改回源只修改已有 Origin Rule 的 `action_parameters.origin.host`，不改规则匹配条件、顺序、其他规则、端口、Host header 或 SNI，也不更新 DNS 记录。

## 生成、审阅和执行

```bash
infrafile plan cdn-pause Resource/edge --dir ./my-infra -o pause.json
infrafile plan show pause.json
infrafile apply pause.json --dir ./my-infra --json

infrafile plan cdn-resume Resource/edge --dir ./my-infra -o resume.json
infrafile plan cdn-origin Resource/edge --dir ./my-infra \
  --host origin.example.net -o origin.json
```

每份 v1 计划包含一个动作，最终 JSON 上限为 4 MiB；还须满足共享解析器的 100 层嵌套、100,000 个展开节点及 16 MiB 展开字段路径与标量上限，生成和读取使用相同限制。超出时在输出或创建文件前拒绝。省略 `-o` 时输出可保存的 JSON；`-o` 只创建新文件，权限为 0600，不覆盖已有路径。生成计划会实时读取云端，不使用可能过期的 state 快照；不会调用写 API。`--timeout` 默认 30s，是整个命令的插件调用总时限。

计划记录格式版本、UTC 生成时间、台账主键、插件绑定、Resource 内容哈希、实例配置哈希、动作参数、变更前状态及 SHA-256、预期变更后状态。不会把实例配置或凭据值写进计划。哈希使用 Go `encoding/json` 的稳定对象键顺序，数字保留 JSON 精度。

`plan show` 校验结构、哈希、敏感字段和动作与预期结果的一致性后展示前后状态；JSON 输出保留完整计划。`apply` 接收这一份计划，并且：

1. 检查 Resource、绑定和实例配置没有变化，目标仍未归档。
2. 重新读取远端并比较观测哈希。不同则返回 `stale`，不发送控制请求。
3. 状态已等于计划目标时返回 `unchanged`；否则调用插件 `control`。
4. 再回读，只有与预期状态一致才返回 `applied`。不符为 `mismatch`；请求或回读失败为 `uncertain`，可能已经产生远端变更，应先回读并重新计划。

Cloudflare 插件在 PATCH 前也重读并校验 `expected_hash`。这不是服务端原子 compare-and-swap：最后一次 GET 与 PATCH 之间仍可能有并发变更。没有自动重试写请求、回滚、审批签名或跨动作事务；显式运行 apply 即执行当前文件中的计划，请先审阅。完整计划本身可被编辑，哈希用于漂移检查，不是身份认证。

zone 动作的观测范围是 zone ID、名称及 paused；Origin Rule 动作还比较整个有序规则列表，忽略 Cloudflare 的版本号和更新时间。这样目标规则、其他规则或顺序变化都会使原计划失效。

退出码：0 成功（含无需修改）；1 观测过期、执行后不符或结果不确定；2 参数、计划、数据仓、凭据或执行前观测失败。详情见 [plan](json/plan.md) 和 [apply](json/apply.md) JSON 契约。

## Cloudflare 权限和边界

- zone 观测使用 Zone Read；暂停／恢复使用 Zone Write，限定目标 zone。[读取 zone](https://developers.cloudflare.com/api/resources/zones/methods/get/)、[编辑 zone](https://developers.cloudflare.com/api/resources/zones/methods/edit/)。
- Origin Rules 需要 Origin Read / Origin Write，并具有对应功能授权。**回源主机覆盖需要 Enterprise**，且新主机名应属于同一个 Cloudflare 账号；这里只校验主机名形式，服务端负责授权与可用性判断。[权限名称](https://developers.cloudflare.com/fundamentals/api/reference/permissions/)、[可用性](https://developers.cloudflare.com/rules/origin-rules/)、[设置约束](https://developers.cloudflare.com/rules/origin-rules/features/)。
- 通过单规则 PATCH 保留其他规则，按 API 要求携带目标规则完整定义。[更新规则](https://developers.cloudflare.com/ruleset-engine/rulesets-api/update-rule/)。
- HTTP 成功和回读一致代表配置已接受，不代表公网流量、源站连通性或 TLS 握手已验收。Cloudflare zone 的只读计划生成及 plan show 已在使用方真实账号验证；未执行 apply，Origin Rules、写入 IAM 与 Enterprise 授权仍只覆盖虚构离线测试。

外部插件可复用同一 `probe` / `control` 契约，见[插件指南](plugins.md)。插件是受信任的本地程序；宿主不能阻止恶意插件在 probe 中写远端。

## DNSControl 配置生成

2026-10-25 确认：Domain 的 `spec.dns` 明确声明期望 DNS，`bindings` 继续表达资产关系，不推导 DNS 记录：

```yaml
spec:
  dns:
    provider: cloudflare-main
    records:
      - {name: "@", type: A, content: 192.0.2.10, ttl: 300, proxied: true}
      - {name: www, type: CNAME, content: example.com, proxied: true}
      - {name: "@", type: MX, content: mail.example.net, priority: 10}
      - {name: "@", type: TXT, content: "v=spf1 -all"}
      - {name: "@", type: CAA, tag: issue, content: ca.example.net}
```

```bash
infrafile dnscontrol ./my-infra > dnsconfig.js
dnscontrol check
# 独立配置 DNSControl 凭据后，由用户执行 preview / push。
```

`provider` 是 DNSControl 凭据条目名称，不自动读取 Account 或解析 infrafile 凭据。生成器只向 stdout 输出 JS；`--json` 返回配置字符串及域名列表。它不执行 DNSControl、不创建 creds.json、不联网。使用 DNSControl 的 `NewDnsProvider(name)`，提供方 TYPE 由 DNSControl 凭据配置决定；注册商为 `none`。

离线 `dnscontrol check` 也需要知道条目类型。可以在审阅目录提供只含类型、不含凭据的 `creds.json`：`{"none":{"TYPE":"NONE"},"cloudflare-main":{"TYPE":"CLOUDFLAREAPI"}}`。生成的 homelab 配置已用 DNSControl 5.4.0 的 check 验证；preview / push 所需的真实认证由 DNSControl 单独配置。

支持 A、AAAA、CNAME、MX、TXT、NS、CAA。`name` 为相对名称，根为 `@`；目标主机名填写不带结尾点的完整域名（至少两个标签，单标签报错），输出自动补点。MX 必须有 priority，CAA 必须有 tag（issue / issuewild / iodef，flags 固定为 0）。可选 ttl 为正整数秒；未填时沿用 DNSControl 默认值。proxied 仅用于 A / AAAA / CNAME，输出 CF_PROXY_ON / CF_PROXY_OFF，仅适用于 Cloudflare，省略时沿用 DNSControl 默认代理行为。

根域 `NS`（name 为 `@`）映射为 `NAMESERVER()`，不接受 TTL；子域委派仍生成 `NS()`。注册商为 NONE，因此不会通过生成配置修改父区委派；提供方自有的 NS 处理仍由 DNSControl 决定。[NS 与 NAMESERVER 的区别](https://docs.dnscontrol.org/language-reference/domain-modifiers/nameserver)。

归档 Domain 和没有 dns 字段的记录跳过；重复记录（A / AAAA 按规范化后的地址比较）、CNAME 冲突及非法数据报错，stdout 为空。输出按域名和记录排序，JS 字符串经 JSON 编码，不插入原始代码。各 zone 固定使用 **NO_PURGE**：未列出的远端记录不自动删除，因此从台账删除 DNS 项不会删除远端记录；删除另行交给 DNSControl 审阅处理。[NO_PURGE](https://docs.dnscontrol.org/language-reference/domain-modifiers/no_purge)、[Cloudflare 元数据](https://docs.dnscontrol.org/provider/cloudflareapi)、[TXT 编码](https://docs.dnscontrol.org/language-reference/domain-modifiers/txt)。
