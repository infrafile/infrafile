# v1.0 RC 独立接入

本流程面向首次使用者，覆盖发行包、独立 Git 台账、导入审阅、工作台、MCP 和真实只读接入。使用 macOS arm64 或 Linux amd64，准备 Bash、Git 和 Python 3；运行二进制无需 Go、Node 或维护者配置。其他平台的验证范围见[发布记录](release.md)。

稳定 Homebrew 配方仍为 v0.3.0。请从 [RC2 发行页](https://github.com/infrafile/infrafile/releases/tag/v1.0.0-rc.2)下载本机归档，按[验签步骤](release.md#验签)核对签名、哈希和 tag。以下在解压目录的 Bash 中执行；新文档和扩展 smoke 将随后续候选归档交付，不改变已发布 RC2 的内容。

## 1. 确认二进制并建仓

```bash
binary="$PWD/infrafile"
"$binary" version --json
umask 077
lab="$(mktemp -d)"
ledger="$lab/ledger"
"$binary" init "$ledger"
printf '\nstorageVersion: v1\n' >> "$ledger/infrafile.yaml"
"$binary" validate "$ledger"
git -C "$ledger" init -b main
git -C "$ledger" add .
git -C "$ledger" commit -s -m 'Initialize infrastructure ledger'
```

`version` 应报告所下载的候选版本与提交；RC2 为 `1.0.0-rc.2 / 1c21b91`。Git 需要你自己的 `user.name` 和 `user.email`；尚未设置时按 Git 提示配置这一个新仓库。提交失败先解决身份、签名或 hook 原因，保持原 hooks。`storageVersion: v1` 是显式启用新持久记录，已有台账先按[兼容与恢复](compatibility.md)备份，勿直接照抄覆盖。

此后把草稿、预览和证据放在 `$lab`，与 `$ledger` 分开，避免未跟踪文件使提交被拒绝。保留 `$lab` 路径，完成前不要删除。

## 2. 离线导入、修改草稿并提交

先用虚构 SSH 配置走通完整路径，不建立 SSH 连接：

```bash
cat > "$lab/ssh.conf" <<'EOF'
Host smoke-web
    HostName 192.0.2.10
    User ops
    Port 22
EOF
"$binary" import ssh-config --dir "$ledger" --file "$lab/ssh.conf" \
  --draft "$lab/ssh-draft.json" --json
```

打开 `ssh-draft.json`，只编辑候选的 `record` 或 `selected`。例如在 `record.metadata` 增加 `description`，保留来源、身份与摘要。然后运行：

```bash
"$binary" import review "$lab/ssh-draft.json" --dir "$ledger" --file "$lab/ssh.conf" --json
"$binary" change preview "$ledger" --draft "$lab/ssh-draft.json" \
  --source-file "$lab/ssh.conf" --out "$lab/review.json" --json
git -C "$ledger" status --short
```

审阅应为 `ok: true`，Git 状态仍干净，预览显示 `Server/smoke-web` 的完整新文件。阅读实际 diff 后，将预览中的 `digest` 原样填入：

```bash
"$binary" change apply "$lab/review.json" "$ledger" --digest '<已审阅的 digest>' --json
"$binary" change status "$ledger" --json
"$binary" show Server/smoke-web "$ledger" --json
git -C "$ledger" log -1
git -C "$ledger" status --short
```

预期 `committed`、`clean`、新增一个带 DCO 的本地提交，保留人工修改的说明；不会 push。重新从相同 SSH 来源生成**新文件名**的草稿，应报告 `exists`，不重复创建。来源或 Git 基准改变时重新生成并审阅，勿手改哈希。恢复所需命令及冲突处理见[台账变更](json/change.md#提交失败与恢复)。

## 3. 启动工作台和 MCP

```bash
"$binary" serve --dir "$ledger" --listen 127.0.0.1:8080 --allow-edit
```

浏览器打开终端给出的本机地址，用本次配对码登录。依次检查总览、资产、巡检、变更：查到 `Server/smoke-web`，核对说明；未采集的证据应显示缺失或 unknown。用台账编辑入口预览一次说明修改，核对 diff 后提交；退出登录后应要求重新配对。页面自动重读只读取保存结果，不执行采集。端口占用可换一个回环端口，详见[工作台](serve.md)。

另一个终端或 MCP 客户端使用二进制和台账的**绝对路径**：

```json
{
  "command": "/absolute/path/to/infrafile",
  "args": ["mcp", "--dir", "/absolute/path/to/ledger", "--timeout", "30s"]
}
```

重新加载客户端，确认发现[十个工具](agent.md#十个工具v10-开发分支)，实际调用 `infrafile_validate {}`、`infrafile_capabilities {}` 和 `infrafile_context {"key":"Server/smoke-web","max_age_days":7}`。与同一台账的 CLI 结果对照，保留 unknown；客户端成功发现和调用另行记录。直接在终端运行 MCP 后等待输入是正常行为，不能当成一次性查询卡住。

## 4. 接入自己的 Cloudflare 只读范围

先按[第一方插件与最小权限](../README.md#第一方插件与最小权限)准备限定目标 zone 的只读令牌，不将真实凭据交给维护者。在新台账的 `infrafile.yaml` 中保留版本字段并增加：

```yaml
plugins:
  cloudflare:
    instances:
      trial:
        api_token: env://INFRAFILE_CLOUDFLARE_TOKEN
```

在 Bash 中隐藏输入令牌，然后显式预检、同步该插件：

```bash
read -r -s -p 'Cloudflare read-only token: ' INFRAFILE_CLOUDFLARE_TOKEN
printf '\n'
export INFRAFILE_CLOUDFLARE_TOKEN
"$binary" validate "$ledger"
"$binary" doctor "$ledger" --plugin cloudflare --instance trial --json
"$binary" doctor "$ledger" --online --plugin cloudflare --instance trial --timeout 30s --json
"$binary" sync cloudflare --dir "$ledger" --timeout 60s --json
```

本例只有一个 Cloudflare 实例，未开启回源详情采集。在线 doctor 不写快照；sync 更新 `state/`。只读令牌不赋予受控写权限。空 zone 的零条 DNS 记录可正常返回，但空清单本身不证明权限完整；核对返回的 zone 是否为令牌所限定的对象。401、403、网络或预算问题按[doctor 诊断表](doctor.md#诊断与退出码)处理后再继续。

采集成功后，先审阅配置和观测并用自己的 Git 流程提交，使工作树干净；state 含私有 DNS 信息，只存自己的私有仓库：

```bash
git -C "$ledger" diff
git -C "$ledger" add infrafile.yaml state/
git -C "$ledger" commit -s -m 'Record reviewed Cloudflare observations'
"$binary" import state --dir "$ledger" --draft "$lab/cloud-draft.json" --json
"$binary" import review "$lab/cloud-draft.json" --dir "$ledger" --json
"$binary" change preview "$ledger" --draft "$lab/cloud-draft.json" \
  --out "$lab/cloud-review.json" --json
```

核对候选所属实例、zone ID、名称与缺口，仅保留需要纳管的 `selected` 项；修改草稿后重新 review 和 preview，预览使用新文件名。按第 2 步的方式审阅 digest 后提交，再次采集和生成新草稿，确认同源没有重复资产、已登记事实没有被导入覆盖。Cloudflare zone 映射为 Resource，不是注册域名。字段和冲突处理见[导入指南](import.md)。

```bash
"$binary" inspect "$ledger" --max-age 7d --json
"$binary" findings refresh "$ledger" --max-age 7d --json
"$binary" findings refresh "$ledger" --max-age 7d --json
"$binary" findings changes "$ledger" --after 0 --json
unset INFRAFILE_CLOUDFLARE_TOKEN
```

同源重复刷新不新增问题事件；unknown、缺基线或关系不完整保持可见，`inspect` 退出 0 只表示报告生成成功。工作台和 MCP 应能读取新登记，若客户端绑定了另一目录，修正路径并重新加载。真实受控操作另用专用无业务 zone，按[Agent 完整场景](agent-acceptance.md)逐步审阅、授权、执行、回读和恢复；不要用本节只读令牌尝试写入。

## 5. 留下独立试用结果

记录试用者是否为非维护者、系统/架构、发行版本与提交、步骤结果、实际 Web/MCP 客户端、遇到的问题及所需帮助。真实只读、受控操作、重复采集与升级分别给结论；帮助后重走受影响步骤。结果只提供脱敏摘要，凭据、域名、zone ID、原始快照和计划保留在试用者私有环境。

维护者可运行 `bash scripts/smoke.sh ./infrafile examples/homelab` 验证虚构数据路径；该检查不是外部试用者验收。T17 的完整条件见[验收清单](plan/v1.0-acceptance.md)。试用结束后停止 serve、撤销试用令牌，按自己的备份安排保留或清理新建台账；不覆盖任何既有台账。
