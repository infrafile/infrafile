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

需要 Go（版本见 `go.mod`）与 Python 3（Homebrew 配方生成器测试）。

```bash
make check   # gofmt + go vet + go test -race + 配方生成器测试，与 CI 一致
make build   # 输出 bin/infrafile
```

## 红线

- 测试夹具、示例和文档里**只用虚构数据**：IP 用文档保留段（`192.0.2.0/24`、`198.51.100.0/24`、`203.0.113.0/24`），域名用 `example.com` / `example.net` 等。
- **任何地方都不写真实凭据**，包括测试和提交信息。
