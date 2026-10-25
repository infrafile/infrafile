# 插件与观测快照

M4 提供插件宿主和两个虚构示例；M5 增加五家云服务和 DNS / TLS / WHOIS / SSH 探测，配置见[第一方插件指南](first-party-plugins.md)。

## 启用与运行

`infrafile.yaml` 的 `plugins` 按插件名配置，每个实例的值就是传给插件的配置对象：

```yaml
apiVersion: infrafile/v1alpha1
kind: Config
plugins:
  example:
    instances:
      home: {}
  example-python:
    instances:
      home:
        region: example-region
```

插件名、实例名最多 64 个字符，以小写字母开头，后接小写字母、数字或由单个 `-` 分开的非空段。没有 `plugins` 时，`sync` 成功返回空列表。

```bash
make build
# 内置示例无需外部程序
bin/infrafile sync example --dir examples/homelab
# Python 示例需要 python3；从本项目根目录运行
PATH="$PWD/examples/plugins:$PATH" bin/infrafile sync --dir examples/homelab --json
```

宿主先校验整个数据仓，再按插件名、实例名排序执行。省略插件名时运行所有已配置实例；指定名称时只运行该插件的所有实例。内置名称优先，其余通过 PATH 查找 `infrafile-plugin-<名称>`。不会自动运行 PATH 上未启用的程序，不通过 shell 拼接命令。

`--timeout 30s` 是每个实例从启动到完成握手和同步的总时限，必须大于 0。每个外部实例启动独立进程；失败、崩溃或超时不影响其他实例。收到取消信号后停止后续实例。会话结束时宿主关闭管道并回收直接子进程。

退出码：0 全部成功（也包含没有配置插件）；1 至少一个实例失败；2 参数、数据仓或公共状态目录无法使用。结果结构见 [sync JSON 契约](json/sync.md)。

## 协议

外部协议是 [JSON-RPC 2.0](https://www.jsonrpc.org/specification) 的逐行 stdio 子集：每条请求／响应都是一行 UTF-8 JSON，以换行结束并立即 flush。一次只发送一个请求，使用正整数 ID，响应必须回传相同 ID；`result` 与 `error` 必须且只能出现一个。不使用批量请求、通知、并发请求或 `Content-Length` 头。单条响应必须小于 4 MiB。

启动后先调用 `describe`：

```json
{"jsonrpc":"2.0","id":1,"method":"describe","params":{}}
{"jsonrpc":"2.0","id":1,"result":{"protocol_version":"v1","capabilities":["sync"]}}
```

能力允许 `sync`、`probe`、`control`、`import`，不能重复；M7 增加 `probe` 与 `control` 调用。未声明所调用能力、未知协议版本或不合法响应都拒绝执行。sync 握手通过后：

```json
{"jsonrpc":"2.0","id":2,"method":"sync","params":{"instance":"home","config":{"region":"example-region"}}}
{"jsonrpc":"2.0","id":2,"result":{"data":{"domains":[{"name":"example.net"}],"region":"example-region"}}}
```

`config` 原样来自实例配置；`data` 必须是 JSON 对象。数组顺序由插件负责保持稳定，宿主稳定排序对象键。插件不得混入本次采集时间；证书到期日等业务事实可以保留。云资源 ID 建议使用字符串；宿主也保留 JSON 整数的精度。JSON Schema 位于 [`protocol/`](../protocol/)，包括请求、响应、握手、同步结果和快照信封。配置 Schema 继续通过 `infrafile schema export` 导出。

内置实现走同一个 `Client.Call` 接口和同一套握手／同步结果校验，省去进程和 JSON-RPC 信封传输。内置 `example` 返回 `example.com`，外部 [Python 示例](../examples/plugins/infrafile-plugin-example-python)返回 `example.net`，两者均不访问网络。

### probe / control（M7）

受控写插件声明 `probe` 和 `control`。二者使用相同的 `{data: {...}}` 结果信封，参数为：

```json
{
  "instance": "main",
  "config": {"api_token": "env://CF_CONTROL_TOKEN"},
  "target": {"zone_id": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
  "action": "cdn-pause",
  "parameters": {},
  "expected_hash": "<control 必须提供的 64 位观测 SHA-256>"
}
```

probe 不得产生远端写入，expected_hash 可省略。暂停／恢复的 data 至少含布尔 `paused`；改回源还需有有序 `rules`，通过 target.rule_id 选中启用的 route 规则，修改 action_parameters.origin.host。其他观测字段原样参与哈希和回读比较。暂停／恢复 parameters 为空；改回源为 `{"host":"origin.example.net"}`，target 另带 ruleset_id / rule_id。

control 必须在执行前重新检查 expected_hash，一次只执行该动作，返回 `{data:{}}` 即可。宿主不会将 control 的响应当作最终观测，会单独调用 probe。Cloudflare 内置实现遵守这一规则；外部插件必须自行实现相同语义。结果经过敏感字段检查，配置仍只传引用；不会自动写 state。完整流程见[受控写指南](control.md)。

## 状态文件

宿主把每个实例的 `data` 放入 `state/<插件>/<实例>.yaml` 的 `spec`：

```yaml
apiVersion: infrafile/v1alpha1
kind: Snapshot
metadata:
  name: home
spec:
  domains:
    - name: example.com
```

同步时间集中放在 `state/_sync.yaml`，记录成功实例的本次命令开始时间（UTC）：

```yaml
apiVersion: infrafile/v1alpha1
kind: Sync
spec:
  example:
    home: "2026-12-01T12:00:00Z"
```

相同观测数据不重写快照；失败不覆盖该实例的旧快照，也不推进其同步时间。未选中的实例和已移除配置的历史快照不会删除。每个文件通过同目录临时文件和 rename 替换；快照与时间文件不是一个跨文件事务，若时间写入失败，结果会明确报告快照已保存。状态目录、插件目录及目标文件不允许符号链接，宿主不写 `inventory/`。

`state/.sync.lock` 防止多个宿主同时覆盖时间文件，正常结束会删除；强制杀死或断电可能留下锁。确认没有其他 sync 运行后，才手动删除该锁。损坏的 `_sync.yaml` 会阻止同步并保留原文。

## 凭据与信任边界

写入前递归检查对象、数组、字段名和值：拒绝 password、token、api_key、private_key、clientSecret、authorization、cookie、credentials 等敏感名称及其常见复数变体（如 passwords、accessTokens、cookies），即使值为空、为对象或为引用也拒绝；另复用台账的常见令牌、私钥和 URL 密码检测。失败只报告固定错误，不输出字段值。外部 stderr 和 RPC 错误原文丢弃，避免插件错误日志携带凭据。

实例配置同样接受台账明文检测。第一方云插件在显式执行 `sync` 时，通过现有凭据解析器读取各自约定的引用字段，SOPS 路径相对数据仓；解析值只用于请求认证，结果如包含已解析的完整值则拒绝输出。外部实例配置仍原样传递，不隐式解密；需要凭据的外部程序可以通过已有的 `infrafile run --secret ... -- infrafile sync ...` 获取环境变量。示例不需要凭据。

deny-list 用于阻止常见误写，不能识别任意字段名下的任意秘密。外部插件是以当前用户身份运行的可信程序，继承环境和当前工作目录，具备该用户的文件与网络权限；宿主不是操作系统沙箱，也不负责插件启动的后台后代进程。仅启用自己信任的程序。
