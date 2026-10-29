# 发版

发布入口是 `.github/workflows/release.yml`。`v*` 标签触发测试、离线快速上手、GoReleaser 六个平台构建，以及基于 GitHub OIDC 的 Cosign keyless 签名。工作流先创建草稿 Release，验收后公开并更新 Homebrew tap。以下以 v0.3.0 为例。

涉及新存储时，先按[兼容与恢复策略](compatibility.md)确认显式标记、旧正式二进制重放和完整恢复点；仅更换程序不构成数据降级。RC/正式版仍须完成 T19 对应候选的升级与恢复验收。

## 构建与发布约定

- GoReleaser 固定 v2.19.0，Cosign 固定 v3.1.4；Go 版本来自 go.mod。
- Linux / macOS / Windows 各提供 amd64、arm64。归档包含二进制、LICENSE、README、docs 与 examples。
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

## v0.3.0 发布验收

2026-10-28 完成公开发布及 Homebrew 升级验收：

- [v0.3.0 Release](https://github.com/infrafile/infrafile/releases/tag/v0.3.0) 已公开为正式最新版，共 10 个附件：六个平台归档、校验清单及签名 bundle、formula 及签名 bundle。标签固定在 `d8de6fafe3396cf0bbf097a77a8a047328d4400b`，未移动 v0.1.0 标签。
- [PR #1](https://github.com/infrafile/infrafile/pull/1) 合并后，[最终发布提交的 CI](https://github.com/infrafile/infrafile/actions/runs/40672698855) 与[正式发布工作流](https://github.com/infrafile/infrafile/actions/runs/40673075746)通过，包括 gofmt、vet、全仓 race、配方测试，以及 Linux amd64 正式归档的离线上手。
- 下载草稿附件后，使用 Cosign v3.1.4 验证两份 bundle：identity 为 `https://github.com/infrafile/infrafile/.github/workflows/release.yml@refs/tags/v0.3.0`，issuer 为 `https://token.actions.githubusercontent.com`。六个 SHA-256、各平台二进制及归档内文档/示例均通过核对，包含新增 audit/Gatus 契约与同步时间示例；无私有验收数据。重新生成的 formula 与签名附件逐字节相同。
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
