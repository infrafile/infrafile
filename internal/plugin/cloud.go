package plugin

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
	"time"
)

type cloudAuth struct{ ID, Secret, Token string }

type cloudConfig struct {
	ID       string   `json:"access_key_id"`
	Secret   string   `json:"access_key_secret"`
	Token    string   `json:"session_token"`
	Services []string `json:"services"`
}

func (b *builtin) cloudConfig(ctx context.Context, config map[string]any, defaults []string) (cloudAuth, []string, error) {
	var cfg cloudConfig
	if err := decodeConfig(config, &cfg); err != nil {
		return cloudAuth{}, nil, err
	}
	if cfg.Services == nil {
		cfg.Services = defaults
	}
	if len(cfg.Services) == 0 {
		return cloudAuth{}, nil, failure("config", "services 不能为空")
	}
	seen := map[string]bool{}
	for _, service := range cfg.Services {
		if !slices.Contains(defaults, service) || seen[service] {
			return cloudAuth{}, nil, failure("config", "services 含未支持或重复的服务")
		}
		seen[service] = true
	}
	id, err := b.credential(ctx, cfg.ID)
	if err != nil {
		return cloudAuth{}, nil, err
	}
	key, err := b.credential(ctx, cfg.Secret)
	if err != nil {
		return cloudAuth{}, nil, err
	}
	auth := cloudAuth{ID: id, Secret: key}
	if cfg.Token != "" {
		auth.Token, err = b.credential(ctx, cfg.Token)
	}
	return auth, cfg.Services, err
}

type domainEntry struct {
	Name      string `json:"name"`
	ID        string `json:"id,omitempty"`
	Status    string `json:"status,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

type cdnEntry struct {
	Name   string `json:"name"`
	ID     string `json:"id,omitempty"`
	CNAME  string `json:"cname,omitempty"`
	Status string `json:"status,omitempty"`
	Type   string `json:"type,omitempty"`
	Area   string `json:"area,omitempty"`
}

func dateOnly(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02", "Jan 02,2006 15:04:05"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.Format("2006-01-02"), nil
		}
	}
	return "", failure("protocol", "云 API 到期日期格式无法识别")
}

func sha256Hex(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func hmacSHA256(key []byte, value string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(value))
	return h.Sum(nil)
}
func rfc3986(query string) string { return strings.ReplaceAll(query, "+", "%20") }
