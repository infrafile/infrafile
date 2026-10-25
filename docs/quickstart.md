# 快速上手

这套流程只使用随包提供的虚构 homelab，不需要云账号、密钥或 1Password，也不连接实际服务器。

## 安装

macOS / Linux，amd64 / arm64：

```sh
brew install infrafile/tap/infrafile
infrafile version
ifl version
```

两条命令应显示相同版本。若 Homebrew 要求信任第三方 formula，先核对 [tap](https://github.com/infrafile/homebrew-tap) 的配方，再执行 `brew trust --formula infrafile/tap/infrafile`，随后重试安装；旧版 Homebrew 无此步骤。

也可从 [Releases](https://github.com/infrafile/infrafile/releases) 下载对应系统和架构的压缩包；Windows 使用 zip。手动安装前按[发布指南的验签步骤](release.md#验签)核对产物。解压后包含 `infrafile`（Windows 为 `infrafile.exe`）、`examples/` 和 `docs/`；`ifl` 软链由 Homebrew 安装。

macOS / Linux 手动解压后，在解压目录执行下面两行，将二进制加入当前终端的 PATH，再继续演练；Windows 可在 PowerShell 中运行 `.\infrafile.exe version`。

```sh
export PATH="$PWD:$PATH"
infrafile version
```

## 准备示例副本

以下命令使用 macOS / Linux 的 shell。Homebrew 安装后：

```sh
work="$(mktemp -d)"
cp -R "$(brew --prefix infrafile)/share/infrafile/examples/homelab" "$work/homelab"
demo="$work/homelab"
```

手动解压安装时，将 cp 的源目录替换为解压目录下的 `examples/homelab`。后续写入都发生在临时副本中。

## 校验、查询和到期

```sh
infrafile validate "$demo"
infrafile show Server/web1 "$demo"
infrafile show App/blog "$demo" --json
infrafile expiry "$demo" --within 30d
infrafile ssh-config "$demo"
```

初始校验应通过，包含 13 条记录。web1 的虚构地址为 `203.0.113.10`；blog 同时有 App 与 Repo，查询时指定类型。show 还会显示引用关系。到期结果随日期变化：退出 0 表示无到期项，1 表示存在已过期或阈值内到期项，2 表示无法执行；脚本不要把 1 当成程序故障。SSH 配置只输出，不写入你的 `~/.ssh/config`。

## 同步虚构观测并导入

```sh
infrafile sync example --dir "$demo"
infrafile import state --dir "$demo" --json
infrafile import state --dir "$demo" --write --json
infrafile validate "$demo"
infrafile import state --dir "$demo" --write --json
```

`example` 是内置的离线插件。第一次 import 仅展示草稿；加 `--write` 才创建不存在的资产，本示例校验将变成 17 条记录。重复导入不会覆盖已有记录。未指定插件的 `sync` 会尝试配置中的其他插件，因此这里显式选择 example。

## 生成配置与 Agent 指南

```sh
infrafile dnscontrol "$demo"
infrafile agent guide
infrafile agent install codex --dir "$work"
```

DNSControl 输出包含 `NO_PURGE`，不会实际修改 DNS。指南安装在临时项目的 `.agents/skills/infrafile/SKILL.md`；Claude Code 将 codex 换为 claude。重复安装相同内容不重写，已有不同内容会保留并报错。

需要 MCP 时，让客户端启动 `infrafile mcp --dir <demo 的绝对路径>`，再调用 `infrafile_show`，参数为 `{"key":"Server/web1"}`。它是等待客户端输入的协议服务，不是一次性查询命令；配置方式见 [Agent 指南](agent.md)。

## 建立自己的数据仓

```sh
infrafile init "$work/my-infra"
infrafile validate "$work/my-infra"
```

空仓应通过校验，接着在 `inventory/` 里登记自己的资产。凭据只写引用，见[凭据指南](secrets.md)。示例里的 SOPS / op 引用没有真实后端，因此不要用 `secret check` 的失败来判断上手流程是否成功。

云同步、SSH 实测、CDN plan / apply 和真实 DNS 推送需要单独配置自己的目标与权限，见[第一方插件](first-party-plugins.md)和[受控写指南](control.md)。上手流程不调用这些操作。

维护者可运行 `bash scripts/smoke.sh <二进制> examples/homelab` 自动验证上述离线流程；它使用并清理独立临时目录。
