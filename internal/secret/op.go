package secret

import (
	"context"
	"os/exec"
	"strings"
	"time"
	"unicode"
)

func readOP(ctx context.Context, ref string, value bool) (string, error) {
	parts := strings.Split(strings.TrimPrefix(ref, "op://"), "/")
	if len(parts) < 3 || len(parts) > 4 || strings.ContainsAny(ref, "?#") || strings.ContainsFunc(ref, unicode.IsControl) {
		return "", &Error{"invalid-ref", "op 引用必须是 op://vault/item/[section/]field，不带查询参数"}
	}
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			return "", &Error{"invalid-ref", "op 引用中的名称不能为空"}
		}
	}
	if !value {
		ref += "?attribute=id"
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "op", "read", "--no-newline", ref)
	data, err := cmd.Output()
	if ctx.Err() != nil {
		return "", &Error{"cancelled", "1Password 操作超时或已取消"}
	}
	if err != nil {
		return "", &Error{"unavailable", "1Password 引用无法访问，请检查 op 安装、登录和字段权限"}
	}
	if !value {
		if len(data) == 0 {
			return "", &Error{"not-found", "1Password 未返回字段 ID"}
		}
		return "", nil
	}
	return string(data), nil
}
