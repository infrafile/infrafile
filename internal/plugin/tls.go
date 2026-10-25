package plugin

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"time"
)

func probeTLS(ctx context.Context, config map[string]any) (any, error) {
	var cfg struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	}
	if err := decodeConfig(config, &cfg); err != nil {
		return nil, err
	}
	if cfg.Port == 0 {
		cfg.Port = 443
	}
	if !validHost(cfg.Host) || cfg.Port < 1 || cfg.Port > 65535 {
		return nil, failure("config", "TLS host 或 port 无效")
	}
	// 只观察证书，不发送应用数据；握手后单独验证，过期证书也能保留到期日。
	dialer := tls.Dialer{Config: &tls.Config{ServerName: cfg.Host, InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)))
	if err != nil {
		return nil, failure("probe", "TLS 握手失败")
	}
	defer conn.Close()
	state := conn.(*tls.Conn).ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return nil, failure("probe", "TLS 服务没有返回证书")
	}
	leaf := state.PeerCertificates[0]
	intermediates := x509.NewCertPool()
	for _, certificate := range state.PeerCertificates[1:] {
		intermediates.AddCert(certificate)
	}
	_, verifyErr := leaf.Verify(x509.VerifyOptions{DNSName: cfg.Host, Intermediates: intermediates})
	verification := "valid"
	if verifyErr != nil {
		verification = "untrusted"
		var invalid x509.CertificateInvalidError
		var hostname x509.HostnameError
		if errors.As(verifyErr, &invalid) && invalid.Reason == x509.Expired {
			verification = "expired-or-not-yet-valid"
		}
		if errors.As(verifyErr, &hostname) {
			verification = "hostname-mismatch"
		}
	}
	// 系统信任库可能先报告未知签发者；证书自身的到期事实仍需准确呈现。
	now := time.Now()
	if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
		verification = "expired-or-not-yet-valid"
	}
	names := slices.Clone(leaf.DNSNames)
	slices.Sort(names)
	return map[string]any{
		"host": cfg.Host, "port": cfg.Port, "verified": verifyErr == nil, "verification": verification,
		"certificate": map[string]any{"subject": leaf.Subject.String(), "issuer": leaf.Issuer.String(), "dns_names": names,
			"not_before": leaf.NotBefore.UTC().Format(time.RFC3339), "not_after": leaf.NotAfter.UTC().Format(time.RFC3339), "sha256": fmt.Sprintf("%x", sha256.Sum256(leaf.Raw))},
	}, nil
}
