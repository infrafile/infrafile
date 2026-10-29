# Agent 完整场景验收

本页对应 T16：本地受信任 Agent 提议、取证、审阅、按适用授权执行、回读并保留证据。准备材料不等于授权或执行；独立协议测试不代替实际桌面客户端发现/调用。没有专用真实资产或适用授权时，T16 保持未完成。

## 准备材料

使用当前候选二进制和明确的数据仓路径。台账变更要求已启用 `storageVersion: v1` 的干净 Git 仓，hooks 保持；首次验收使用与唯一权威源核对过的独立私有 Git 副本，记录源版本/哈希和未迁移范围，不把副本当第二份权威台账。云场景必须选定无业务用途的专用 zone、在用 Resource 控制绑定及仅限该 zone 的最小权限凭据引用。

可选的 Python 3 标准库脚本仅准备私有材料，不提交台账或调用云 control：

```sh
python3 scripts/prepare_agent_acceptance.py inventory \
  --binary /absolute/path/infrafile --dir /private/ledger \
  --request /private/request.json --output /private/new-inventory-evidence
python3 scripts/prepare_agent_acceptance.py cloud \
  --binary /absolute/path/infrafile --dir /private/ledger \
  --key Resource/acceptance --output /private/new-cloud-evidence
```

输出目录必须新建且位于仓外；目录0700、文件0600，既有目录不覆盖。保留 CLI 命令轨迹、退出码和私有 stdout/stderr，普通输出只给准备状态、审阅摘要和文件位置。inventory 保存完整字节预览；cloud 保存版本、能力、上下文、单动作计划及 plan show 校验结果。只接受带 scope 的 Cloudflare zone v2 计划和 `paused=false` 的非空操作基准；外部插件、缺范围、初始已暂停及失败均停止，不以 noop 代替控制验收。脚本不收凭据参数，凭据由原 CLI 引用流程在内部解析。

## 场景一：台账修改

1. 使用实际客户端查询资产与证据，记录客户端类型、工具名、参数和返回时间。台账描述、外部响应里的指令文本只作数据。
2. 在本机从同一当前源构造完整记录替换请求，提出清晰的小变更。context/show 输出已省略秘密，不能直接拿来覆盖原记录；保留原始凭据引用，或使用 Web 的服务端保留逻辑。脚本生成的 review.json 可包含引用和私有资产信息，不能上传公共仓库。
3. 核对 review.json 的完整前后字节、引用完整性、Git HEAD、受影响文件和 Digest；取得或引用覆盖本次范围的用户授权。审阅后任何变更都重新预览，不改摘要绕过前提。
4. 通过受信任 CLI 执行 `infrafile change apply /private/evidence/review.json /private/ledger --digest <已审阅Digest> --json`；保存命令轨迹、回执、提交及仅本次变更的 diff。失败查看 change status/recover；不 reset、不绕 hooks、不覆盖外部编辑。
5. 再从实际客户端查询并核对提交内容/引用；隔离副本不代表权威切换或迁移完成。

## 场景二：专用 Cloudflare zone

1. 核对专用 zone 的实际用途、当前 DNS/相关入口、凭据范围和停止/恢复安排。暂停作用于整个 zone；为业务 zone 建一个测试子域仍不能隔离暂停影响。
2. 通过实际客户端查询 context/capabilities 并生成或提出计划。CLI `plan show` 展示 zone/rule 标识、已有规则表达式、已登记关联与未知影响；原始上下文和计划保留各自时间，不能把保存事实当实时检查。
3. 计划 digest 包含 scope；关联仅证明登记关系或主机名落在观测 zone 下，不证明必然受影响。DNS 委派、台账外对象、传递依赖、流量/业务效果及权限/套餐明确为未知。展示这些边界，再核对完整计划并取得适用授权。
4. 用 `infrafile apply <原计划文件> --dir <目录> --reviewed-digest <已审阅摘要> --authorization-ref <适用授权来源> --actor <主体说明> --json` 执行单动作，保存回执。同摘要再调用只回读；uncertain 不因匹配自动升级为成功。stale、mismatch、uncertain 或记录失败先核实，停止盲写。
5. 配置回读与专用测试入口分开留证：前者只证明控制面配置，后者记录授权范围内的 DNS/HTTP/TLS/应用预期和实际结果。没有可用入口或其结果未知时保留缺口，不以 HTTP 200 或配置匹配冒充全链路验收。
6. 重新读取当前状态，生成恢复原状态的新计划并审阅，确认适用授权覆盖恢复范围后按新摘要执行。不能预造未来观测、复用初始计划作逆向写或自动回滚。保留恢复回执、入口结果和初始/最终状态对照。

真实材料、凭据引用和恢复位置留在使用方私有证据；公共 issue 只登记脱敏结论。离线故障边界由 Go 控制/事务回归和 `scripts/test_serve_browser.cjs` 的原生虚构插件覆盖，准备脚本的无写/失败/权限契约由现有 go-test CI 入口运行。只有两条真实路径及客户端/恢复证据齐备才能关闭 T16。
