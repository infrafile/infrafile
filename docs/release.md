# 发版

v0.1 的发布入口是 `.github/workflows/release.yml`。`v*` 标签触发测试、离线快速上手、GoReleaser 六个平台构建，以及基于 GitHub OIDC 的 Cosign keyless 签名。工作流先创建草稿 Release，验收后公开并更新 Homebrew tap。

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
git tag -a v0.1.0 -m 'infrafile v0.1.0'
git push origin v0.1.0
```

不要移动已发布标签。下载草稿需要有仓库权限的 gh 登录；工作流失败时先检查已存在的草稿资产，再修复原因。不要用本地 snapshot 替换正式构建产物。

## 验签

需要 Cosign v3、GitHub CLI 和 Python 3。下列命令在新建的空临时目录中运行：

```sh
tag=v0.1.0
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
```

身份必须同时匹配仓库、工作流路径、tag 和 OIDC issuer，不能只做哈希比对。命令依据 [GoReleaser 的 Cosign 签名说明](https://goreleaser.com/customization/sign/sign/)。解压本机平台归档，再运行 `infrafile version --json`，确认构建版本与 tag、commit 对应；运行随该 tag 的 `scripts/smoke.sh` 检查上手流程。

## 公开 Release 与提交 tap

验签、版本和离线流程通过后：

```sh
gh release edit v0.1.0 --repo infrafile/infrafile --draft=false --latest
```

第一次创建公开的 `infrafile/homebrew-tap`；后续更新使用该仓当前 main。审阅下载的 `infrafile.rb`，确认四个平台 URL 和 SHA 与本次已验签的校验和一致，将其保存为 tap 的 `Formula/infrafile.rb`，带 DCO 提交后推送。tap 不存储任何凭据。

```sh
brew install infrafile/tap/infrafile
brew test infrafile/tap/infrafile
infrafile version --json
ifl version --json
```

Homebrew 要求信任时，审阅配方后只信任这一 formula：`brew trust --formula infrafile/tap/infrafile`，见 [Tap Trust](https://docs.brew.sh/Tap-Trust)。升级已有安装使用 `brew update`、`brew upgrade infrafile/tap/infrafile`。最后按[快速上手](quickstart.md)跑完临时 homelab 流程，才能把“签名产物已发布”与“包管理器安装可用”两项都记为完成。
