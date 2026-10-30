# 参与贡献

项目处于早期阶段，接口和数据格式仍在变动。

## 治理

采用 BDFL 模式，由维护者最终拍板。**大改动请先开 issue 讨论**，确认方向后再提 PR。

## DCO 签名

不签 CLA，改用 [DCO](https://developercertificate.org/)。每个提交都要带 `Signed-off-by` 行：

```bash
git commit -s
```

签名即表示你确认有权以本项目的许可证（Apache-2.0）提交这些代码。

## 本地开发

需要 Go（版本见 `go.mod`）、Python 3（Homebrew 配方生成器测试）、Node.js 22.12+ 的受支持 LTS 版本和 npm 11（锁定开发版本 11.21.1）。前端使用 React、Radix Themes、TypeScript 和 Vite，依赖锁定在 `web/package-lock.json`；Node 只参与开发构建，发行二进制不依赖 Node。

```bash
make check build  # 安装锁定前端依赖、构建、类型/单测、Go 门禁、配方与 Schema 测试，输出 bin/infrafile
make frontend     # npm ci + Vite；直接 go test/go build 前也需先生成内嵌资源
npx --prefix web playwright install chromium webkit
INFRAFILE_BROWSER_ENGINE=chromium make browser-test
INFRAFILE_BROWSER_ENGINE=webkit make browser-test
```

`build/test/vet/check` 共用 `frontend` 前置步骤，同次 make 调用只执行一次。只修改前端时可用 `npm --prefix web run typecheck`、`npm --prefix web test` 和 `npm --prefix web run build`；要浏览实际页面，随后重新编译 Go 二进制。`browser-test` 复用已构建的二进制，详见[工作台验收](docs/serve.md#浏览器验收)。CI 的 Linux 浏览器依赖通过 Playwright `install --with-deps` 安装。

提交 `web/src/` 和锁文件，不提交 `web/node_modules/`、`cmd/infrafile/web/dist/`。GoReleaser 的 before hook 同样执行 `make frontend`，确保源码归档和发布路径生成相同内嵌资源。

## 红线

- 测试夹具、示例和文档里**只用虚构数据**：IP 用文档保留段（`192.0.2.0/24`、`198.51.100.0/24`、`203.0.113.0/24`），域名用 `example.com` / `example.net` 等。
- **任何地方都不写真实凭据**，包括测试和提交信息。
