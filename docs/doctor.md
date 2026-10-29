# 分层预检

`infrafile doctor [目录]` 默认只检查本地数据仓、已启用实例的配置、凭据引用和所需权限说明；不解密、联网、执行凭据命令或启动插件。现有 `secret check` 对 1Password 的元数据查询语义不变，doctor 使用独立的禁网检查入口。

```sh
infrafile doctor /path/to/ledger --json
infrafile doctor /path/to/ledger --plugin cloudflare --instance demo --json
infrafile doctor /path/to/ledger --online --plugin cloudflare --instance demo --timeout 30s --json
```

离线默认检查全部实例及 inventory 的 `spec.secrets` 引用；指定插件和实例时仅检查该实例。整个仓库始终先经过布局、Schema、外键、归档与明文凭据校验。校验失败只输出固定诊断，避免把非法输入或秘密反射进预检结果；在本地用 `validate` 定位原文件。

env 只确认变量存在；SOPS 只确认仓内加密字段及支持的 age 元数据，不证明密钥、MAC 或权限可用。密文必须是最多 4 MiB 的普通文件；符号链接和非普通文件拒绝，打开后核对文件身份并限量读取。1Password 离线只校验引用格式，结果为 unknown；不执行 `op`。离线 passed 不表示已验证认证或连接。

## 显式在线检查

必须同时指定一个已启用的插件与实例，不接受“全部在线”。九个第一方插件复用现有 sync 的只读采集路径，在内存中验证响应信封，不创建 state、锁、快照、同步时间、回执或正向基线。example 只运行虚构演示。外部插件缺少受信任的只读约定，即使指定在线也不启动；不因其 describe 自称只读而赋予权限。

每次所选实例总时限默认 30 秒，最多 2 分钟，包含凭据解析；云 API 最多 20 个 HTTP 请求，每个响应沿用 4 MiB 上限，达到预算即报告 unknown。云列表可能分页或逐 zone 查询 DNS，超限请缩小配置范围；这不是部分清单成功。SSH 只执行原有固定 uname，WHOIS/DNS/TLS 沿用各自有界探针与取消逻辑；SSH 仍使用可信用户配置，不构成子进程沙箱。

在线将请求配置选择的全部服务；如需减少范围，先审阅 `services` / `collect_origin` 和账号过滤配置。预检不修改配置。成功只证明这次请求的只读操作完成；空清单不能证明完整账号权限，读取成功不证明写权限或业务健康。TLS 返回证书但信任验证失败会单列为 failed。

## 诊断与退出码

结果保留无法确认的情况，不输出响应体、请求头、签名、凭据值或原始错误。权限说明来自能力目录，是所需条件，不能当作当前账号已获授权的证明。

| code | 含义 |
| --- | --- |
| valid / offline-ready | 本次本地检查通过，未验证在线权限 |
| invalid-repository / invalid-config / invalid-reference | 本地布局、配置或引用格式不符合约定 |
| missing-reference / missing-executable | 引用不存在，或找不到程序 |
| reference-unavailable / online-required | 本地无法确认引用可访问性；例如需要显式在线检查的 op 字段 |
| external-unverified / unsupported | 外部插件细节未知；在线只读预检不受支持且未启动程序 |
| read-succeeded | 选定实例的本次只读采集完成，不是权限或业务健康证明 |
| unauthorized | 确认收到 HTTP 401 |
| permission-or-feature-denied | 确认收到 HTTP 403；无法区分权限、功能/套餐或访问策略 |
| unreachable | 网络连接或名称解析失败 |
| credential-unavailable | 显式在线解析凭据失败；不猜测为云端认证拒绝 |
| verification-failed | TLS 信任验证未通过 |
| request-budget / cancelled-or-timeout / unknown | 未完成、超限、超时、取消或无法可靠分类；不是空清单成功 |

各厂商 HTTP 200 中的 API 拒绝没有统一可验证的错误语义，当前保守返回 unknown，不按错误文本猜测认证/权限。其他不能确定的 HTTP、协议或探针错误同样保留 unknown。

退出码：0 请求的检查均通过；1 有 failed 或 unknown；2 参数错误或无法开始。无实例时的 0 仅表示请求的本地仓库检查通过。JSON 契约见 [doctor JSON](json/doctor.md)。真实只读、最小 IAM、专用验收账号和生产集成证据仍须分别记录；离线与 HTTP 回放不能替代真实账号验收。
