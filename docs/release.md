# 发版

发布入口是 `.github/workflows/release.yml`。`v*` 标签触发测试、离线快速上手、GoReleaser 六个平台构建，以及基于 GitHub OIDC 的 Cosign keyless 签名。工作流先创建草稿 Release，验收后公开并更新 Homebrew tap。以下以 v0.3.0 为例。

涉及新存储时，先按[兼容与恢复策略](compatibility.md)确认显式标记、旧正式二进制重放和完整恢复点；仅更换程序不构成数据降级。RC 提供 T17/T19 所需候选产物，正式版等待对应候选的真实接入、升级与恢复验收。

## 构建与发布约定

- GoReleaser 固定 v2.19.0，Cosign 固定 v3.1.4；Go 版本来自 go.mod。
- Linux / macOS / Windows 各提供 amd64、arm64。归档包含二进制、LICENSE、README、docs、examples，以及 `scripts/smoke.sh` 和 `scripts/prepare_agent_acceptance.py`。Homebrew 将示例、文档和脚本安装到 pkgshare；脚本需要 Bash / Python 3。
- `checksums.txt` 列出六个归档的 SHA-256，`checksums.txt.sigstore.json` 是签名 bundle。
- `scripts/homebrew.py` 从同批校验和生成 macOS / Linux formula，包含 `infrafile` 与 `ifl` 软链，以及共享示例和文档。formula 和自己的签名 bundle 一并上传草稿 Release。
- GoReleaser 的旧 brews 发布器[已经弃用](https://goreleaser.com/customization/publish/homebrew_formulas/)，这里保留标准 formula，使用校验和生成配方。tap 的提交由维护者在验签后完成，无需给构建工作流配置跨仓令牌。
- 签名使用 job 的 `id-token: write`，发布使用该仓的 `GITHUB_TOKEN`。不保管 Cosign 私钥。Sigstore 签名证明构建身份与文件完整性，不等于 Apple Developer ID 签名或公证。

## 发布前

```sh
make check
goreleaser check
actionlint
shellcheck scripts/smoke.sh
goreleaser release --snapshot --skip=publish,sign --parallelism 2
```

snapshot 只检验打包，不代表正式签名或发布成功；再次生成前先检查 dist 中是否有需要保留的文件。用本机平台的 snapshot 二进制运行 `scripts/smoke.sh`，检查六个归档、示例目录和校验和。实现与审查完成后提交，推送 main，等待该提交的 CI 成功。

确认目标 tag 尚不存在，再创建不可移动的发布标签：

```sh
git tag -a v0.3.0 -m 'infrafile v0.3.0'
git push origin v0.3.0
```

不要移动已发布标签。下载草稿需要有仓库权限的 gh 登录；工作流失败时先检查已存在的草稿资产，再修复原因。不要用本地 snapshot 替换正式构建产物。

## v1.0 候选版

首个候选使用 `v1.0.0-rc.1`；创建前确认远端没有同名 tag/Release，并等待精确候选提交的 CI。`prerelease: auto` 将带 RC 后缀的 tag 标为预发布，工作流仍先生成草稿、签名并检查正式 Linux 归档。归档中的 smoke 同时覆盖旧离线上手和显式 v1 存储副本的能力、上下文、unknown、问题历史重复刷新与缺回执空态；不联网采集或执行云写。

候选契约以该 tag 的 [兼容策略](compatibility.md)、[CLI JSON](json/README.md)、[MCP](json/mcp.md)、[插件协议](plugins.md)和[第一方插件范围](first-party-plugins.md)为准。它们描述候选的接口边界；`v1alpha1` 保留既有兼容承诺，后续 RC 变化须记录并重验受影响路径。

按下节验签并确认正式归档的版本/commit、脚本和离线流程后，方可公开预发布：

```sh
gh release edit v1.0.0-rc.1 --repo infrafile/infrafile --draft=false --prerelease --latest=false
```

RC 不更新稳定 Homebrew tap，也不替换用户现有的 v0.3.0 安装。在独立目录运行已验签的 macOS arm64 / Linux amd64 归档，供 T17 独立上手和 T19 升级/恢复验收。formula 仍作为同批签名附件生成并核对 URL/哈希，稳定 tap 等正式发布门槛通过后再更新。

候选已知限制：完整通知送达、外部非维护者独立试用、14 个真实日历日的持续运行及正式范围切换尚待逐项验收；Windows 的新持久执行拒绝运行，其他未做原生验收的平台仅承诺构建产物。各候选的升级/恢复结果见下方完成记录。自有台账的控制面/DNS 结果不证明 HTTP/TLS/应用健康。详细门槛仍由[验收清单](plan/v1.0-acceptance.md)及工作包 issue 维护，RC 公开不表示 v1.0 完成。

## 验签

需要 Cosign v3、GitHub CLI 和 Python 3。下列命令在新建的空临时目录中运行：

```sh
(
set -eu
tag=v0.3.0
gh release download "$tag" --repo infrafile/infrafile
identity="https://github.com/infrafile/infrafile/.github/workflows/release.yml@refs/tags/$tag"
cosign verify-blob --certificate-identity "$identity" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --bundle checksums.txt.sigstore.json checksums.txt
cosign verify-blob --certificate-identity "$identity" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --bundle infrafile.rb.sigstore.json infrafile.rb
python3 - <<'PY'
import hashlib, pathlib
for line in pathlib.Path('checksums.txt').read_text().splitlines():
    expected, name = line.split()
    assert pathlib.Path(name).name == name
    assert hashlib.sha256(pathlib.Path(name).read_bytes()).hexdigest() == expected, name
print('all archive checksums passed')
PY
)
```

身份必须同时匹配仓库、工作流路径、tag 和 OIDC issuer，不能只做哈希比对。命令依据 [GoReleaser 的 Cosign 签名说明](https://goreleaser.com/customization/sign/sign/)。解压本机平台归档，再运行 `infrafile version --json`，确认构建版本与 tag、commit 对应；运行随该 tag 的 `scripts/smoke.sh` 检查上手流程。

## 公开 Release 与提交 tap

验签、版本和离线流程通过后：

```sh
gh release edit v0.3.0 --repo infrafile/infrafile --draft=false --latest
```

第一次创建公开的 `infrafile/homebrew-tap`；后续更新使用该仓当前 main。审阅下载的 `infrafile.rb`，确认四个平台 URL 和 SHA 与本次已验签的校验和一致，将其保存为 tap 的 `Formula/infrafile.rb`，带 DCO 提交后推送。tap 不存储任何凭据。

```sh
brew install infrafile/tap/infrafile
brew test infrafile/tap/infrafile
infrafile version --json
ifl version --json
```

Homebrew 要求信任时，审阅配方后只信任这一 formula：`brew trust --formula infrafile/tap/infrafile`，见 [Tap Trust](https://docs.brew.sh/Tap-Trust)。升级已有安装使用 `brew update`、`brew upgrade infrafile/tap/infrafile`。最后按[快速上手](quickstart.md)跑完临时 homelab 流程，才能把“签名产物已发布”与“包管理器安装可用”两项都记为完成。

## v1.0.0-rc.2 候选发布验收

2026-10-31（北京时间）已公开[第二个签名预发布候选](https://github.com/infrafile/infrafile/releases/tag/v1.0.0-rc.2)，tag 固定在 `1c21b9155917bdf64efd82158d7cbb9e4420dd8c`。稳定 Latest 和稳定 Homebrew tap 继续为 v0.3.0；没有调整通知渠道或正式发布门槛。

RC2 包含 RC1 之后的 MCP 单次查询固定代次、React/Radix 工作台、总览与服务端资产分类、保存结果自动重读、证书元数据和登记到期展示，以及文字对比度修复。读取已保存结果不触发采集，未检查与 unknown 不代表健康；候选契约以 RC2 tag 内文档为准。

- [精确提交 CI](https://github.com/infrafile/infrafile/actions/runs/41148983694)及[正式构建/签名工作流](https://github.com/infrafile/infrafile/actions/runs/41201616421)成功。10 个附件齐全，两份 Cosign bundle 均核对本仓 `release.yml@refs/tags/v1.0.0-rc.2` 身份及 GitHub Actions issuer；六归档哈希、必需文档/示例/脚本和配方逐字节重生成通过。该记录针对正式签名产物，不沿用前一轮未签名 snapshot。
- macOS arm64 与原生 Linux amd64 使用已验签的 v0.3.0、RC1、RC2 二进制完成安装/升级/别名、离线上手、旧七个 MCP 工具契约与调用、RC1 历史读取、重复刷新无新增事件、损坏历史拒绝、钩子失败后新进程恢复和重新审阅后提交。两平台各 35 个旧夹具文件未被升级查询改写；40 项 unknown 保持，回退在独立目录进行，升级后新修改保留。Linux 只收到公开归档与虚构夹具，临时目录已清理。
- 独立 Homebrew 前缀使用字节一致的签名配方，完成 v0.3.0 安装、RC1/RC2 实际 `brew upgrade`、每版 `brew test` 和 `infrafile`/`ifl` 同一二进制验证。初始归档来自已验签下载并预填缓存；RC2 公开后另行强制从发布 URL 下载，哈希与签名清单相同。安装二进制与 macOS 归档哈希一致，系统前缀与稳定 tap 未修改。自定义前缀仅作隔离验收，不是推荐的 Homebrew 部署位置。
- 签名 macOS RC2 通过 Chromium/WebKit 完整工作台流程，包含总览/自动重读、主题/窄屏、认证撤销/HTTPS、编辑失败恢复和执行回执。Homebrew 安装程序在服务进程 PATH 无 Node、浏览器禁止外部资源请求时，四个页面均可用。测试使用虚构数据，不执行真实云写或发送通知。

只有 macOS arm64 / Linux amd64 在本轮原生运行；另外四个平台仅验证构建/归档/哈希，Windows 持久写入边界保持。RC2 可用于后续独立试用，T20 的正式发布部分仍开放：T10 通知送达暂缓，T17 尚无独立非维护者参与者，T18 真实 14 日、两次周采集及正式范围切换继续分别验收。后续正式版须重新核对其精确提交、签名、安装/升级与稳定 tap，不由本次 RC 验收自动通过。

## v1.0.0-rc.1 候选发布验收

2026-10-30（北京时间）已公开[首个预发布候选](https://github.com/infrafile/infrafile/releases/tag/v1.0.0-rc.1)，tag 固定在 `6156a39c7c71742e0c2070abece629f25c1223be`。稳定 Latest、Homebrew tap 和本机已安装版本保持 v0.3.0。

- [精确提交 CI](https://github.com/infrafile/infrafile/actions/runs/40915526690)及[正式发布工作流](https://github.com/infrafile/infrafile/actions/runs/40915885167)成功，包含完整检查、六平台构建、签名及 Linux amd64 归档内脚本 smoke。早先主分支 CI 的写后取消测试失败日志保留；[PR #33](https://github.com/infrafile/infrafile/pull/33)改为确认模拟写入后取消，未调整产品超时或削弱 uncertain/禁止盲重试断言。
- 10 个附件均已下载。Cosign 核对 checksums 和 formula 的两份 bundle，固定 identity 为本仓 release.yml 的 RC tag、issuer 为 GitHub Actions；六个归档哈希、包内文档/示例/脚本和配方逐字节重生成一致。正式 macOS arm64 归档报告 `1.0.0-rc.1 / 6156a39`。
- macOS arm64 与原生 Linux amd64 正式归档完成离线 smoke、v0.3.0 升级、七个旧 MCP 工具契约/调用、观测/历史/事务损坏拒绝、钩子失败后新进程恢复、外部修改保护与独立目录回退。历史恢复保留原时间和 unknown，升级后新增修改未被旧备份覆盖。Linux 仅使用虚构台账，临时目录已清理。
- 私有历史台账的 256 条记录完成新旧二进制结果对照及独立目录恢复；404 个历史输入与 499 个保留控制候选文件不变，升级后 895 unknown / ok=false 保持。独立密钥备份在隔离默认密钥后解开恢复的密文，错误密钥拒绝，私钥和凭据明文不落盘。原始材料、脚本、哈希与初次验收脚本失败留私有环境；结果明确对应保留快照，不声明当前台账健康或正式切换。

T19/AT19 针对 RC1 的完成证据汇总于[工作包 F](https://github.com/infrafile/infrafile/issues/15)。其他四个平台目标仅核对构建/归档/哈希，未做原生运行；签名配方未发布到稳定 tap。外部独立试用、14 日运行、完整客户端受控场景和正式范围切换继续开放，不能据本次候选关闭 v1.0 总任务。tag 内文档保留打标签时状态，本完成记录以后续 main 为准。

## v0.3.0 发布验收

2026-10-28 完成公开发布及 Homebrew 升级验收，原始验收范围见 [v0.3 方案 M5](plan/v0.3.md#m5-发布验收)：

- [v0.3.0 Release](https://github.com/infrafile/infrafile/releases/tag/v0.3.0) 已公开，共10个附件：六个平台归档、校验清单及签名 bundle、formula 及签名 bundle。标签固定在 `3e43bcee3b676950544bdc3d883a0767d96162bf`，对应 PR #6 合并提交。
- [正式发布工作流](https://github.com/infrafile/infrafile/actions/runs/40696756251)完成。六个归档校验和、checksums 与 formula 两份 Cosign 签名均已验收；签名身份限定该仓 release.yml 的 v0.3.0 标签及 GitHub OIDC issuer。
- Linux amd64（工作流）与 macOS arm64（本机）的正式归档实际上手通过；本机二进制报告 `0.3.0 / 3e43bce`，其余四个平台仅完成构建、归档及哈希检查。
- [tap 配方提交 6c2ff1c](https://github.com/infrafile/homebrew-tap/commit/6c2ff1ce2c82a251fa8516294f43e9d141e5c91a)对应0.3.0；本机 Homebrew 从0.2.0升级到0.3.0，brew test、安装后上手以及 infrafile/ifl 同一二进制检查通过。
- 正式二进制在私有投影上的禁网 audit 与六个离线 MCP 工具复验通过；不能据此宣称桌面客户端已加载、定时任务已实际触发、远端 CI 或监控通知已验收。

Cloudflare 回源403、未建立基线的目标、最小 IAM、真实生产写入与完整权威台账切换仍按 [v0.3 接入边界](plan/v0.3.md#m4-路径边界与使用接入)记录。以上为历史验收记录，本轮文档校正另行核对了正式标签、Release 元数据和 tap 提交，未重复执行发布或移动标签。

## v0.2.0 发布验收

2026-10-28 完成公开发布及 Homebrew 升级验收：

- [v0.2.0 Release](https://github.com/infrafile/infrafile/releases/tag/v0.2.0) 当时公开为正式最新版，共 10 个附件：六个平台归档、校验清单及签名 bundle、formula 及签名 bundle。标签固定在 `d8de6fafe3396cf0bbf097a77a8a047328d4400b`，未移动 v0.1.0 标签。
- [PR #1](https://github.com/infrafile/infrafile/pull/1) 合并后，[最终发布提交的 CI](https://github.com/infrafile/infrafile/actions/runs/40672698855) 与[正式发布工作流](https://github.com/infrafile/infrafile/actions/runs/40673075746)通过，包括 gofmt、vet、全仓 race、配方测试，以及 Linux amd64 正式归档的离线上手。
- 下载草稿附件后，使用 Cosign v3.1.4 验证两份 bundle：identity 为 `https://github.com/infrafile/infrafile/.github/workflows/release.yml@refs/tags/v0.2.0`，issuer 为 `https://token.actions.githubusercontent.com`。六个 SHA-256、各平台二进制及归档内文档/示例均通过核对，包含新增 audit/Gatus 契约与同步时间示例；无私有验收数据。重新生成的 formula 与签名附件逐字节相同。
- macOS arm64 正式归档报告 `0.2.0 / d8de6fa`，完整离线上手通过。随后公开 Release，[tap 配方提交 bc3e70c](https://github.com/infrafile/homebrew-tap/commit/bc3e70c87026ef8991df77d47369302a3135c868)采用已验签的同批附件。
- 本机 Homebrew 从 0.1.0 升级到 0.2.0，`brew test infrafile/tap/infrafile` 通过；`infrafile` 与 `ifl` 解析到同一二进制，版本均为 `0.2.0 / d8de6fa`。安装版的完整离线上手与先前私有台账验收快照的离线审计回归通过；后者未重新请求云 API，也未迁移权威台账。

macOS amd64、Linux arm64 和 Windows 两个架构仅验证构建、归档内容及哈希，未做原生运行。线上 apply、注册域名列表 API、完整最小 IAM、桌面 MCP 客户端加载及主台账完整迁移仍未验收。标签归档保留打标签时的文档，完成记录以 main 的本节为准。

## v0.1.0 发布验收

2026-10-25 完成首次公开发布：

- [Release](https://github.com/infrafile/infrafile/releases/tag/v0.1.0) 为公开正式版本，共 10 个附件：六个归档、校验清单及签名 bundle、formula 及签名 bundle。标签固定在 `d92dd87b0a8bc81e6f0c79db044411a11922f741`。
- [源码 CI](https://github.com/infrafile/infrafile/actions/runs/40179774245) 与[发布工作流](https://github.com/infrafile/infrafile/actions/runs/40180093985/attempts/2)通过，包括 Go race 测试、配方生成测试和 Linux amd64 正式归档的离线上手检查。
- 下载正式附件后，按上述固定工作流身份与 issuer 验证两份 Cosign bundle；六个 SHA-256、归档内的二进制、文档和示例均匹配。重新生成的 formula 与签名附件逐字节一致。
- [tap 配方提交](https://github.com/infrafile/homebrew-tap/commit/20d69fb)使用已验签的附件。macOS arm64 实际运行 `brew install infrafile/tap/infrafile` 和 `brew test infrafile/tap/infrafile` 成功；`infrafile` 与 `ifl` 都报告 `0.1.0 / d92dd87`，并解析到同一二进制。
- 使用 Homebrew 安装的二进制和共享示例跑完快速上手：空仓校验、13 条初始记录、离线同步、导入后 17 条记录、重复导入不创建、DNSControl、SSH 配置与 Agent 指南安装均通过。

首次工作流因 WHOIS 测试对套接字截止时间与 context 通知先后的假设失败，未产出 Release；第二次通过。main 上的 `d68ae7e` 只修正该测试断言，连续 50 次 race 验证通过，未改运行时代码或移动发布标签。

macOS amd64、Linux arm64 和 Windows 两个架构只验证了构建与归档，未做原生运行；真实云账号、IAM 权限和生产写入仍未验收。归档内文档保留标签时的状态，发布完成记录以此处 main 文档为准。
