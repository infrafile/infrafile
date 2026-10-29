// Package secret 检查凭据引用，并只在显式运行命令时解析凭据值。
package secret

import (
	"context"
	"os"
	"regexp"
	"strings"
)

// Error 的代码和说明可安全输出，不包含后端错误或凭据值。
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

// Resolver 以数据仓根目录为 SOPS 文件路径的边界。
type Resolver struct{ Dir string }

var EnvName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Check 只检查引用能否定位，不请求凭据值。
func (r Resolver) Check(ctx context.Context, ref string) error {
	_, err := r.read(ctx, ref, false)
	return err
}

// CheckOffline never executes credential helpers; op field access remains unknown.
func (r Resolver) CheckOffline(ctx context.Context, ref string) error {
	if strings.HasPrefix(ref, "op://") {
		if err := checkOPRef(ref); err != nil {
			return err
		}
		return &Error{"online-required", "1Password 字段可访问性需要显式在线检查"}
	}
	return r.Check(ctx, ref)
}

// Resolve 在内存中解析值，供子进程环境使用。
func (r Resolver) Resolve(ctx context.Context, ref string) (string, error) {
	value, err := r.read(ctx, ref, true)
	if err == nil && strings.ContainsRune(value, 0) {
		return "", &Error{"invalid-value", "凭据含 NUL，不能放入进程环境"}
	}
	return value, err
}

func (r Resolver) read(ctx context.Context, ref string, value bool) (string, error) {
	if ctx.Err() != nil {
		return "", &Error{"cancelled", "凭据操作已取消"}
	}
	if strings.HasPrefix(ref, "env://") {
		name := strings.TrimPrefix(ref, "env://")
		if !EnvName.MatchString(name) {
			return "", &Error{"invalid-ref", "env 引用必须是 env://变量名"}
		}
		v, exists := os.LookupEnv(name)
		if !exists {
			return "", &Error{"not-found", "环境变量不存在"}
		}
		if value {
			return v, nil
		}
		return "", nil
	}
	if strings.HasPrefix(ref, "sops://") {
		return r.sops(ctx, ref, value)
	}
	if strings.HasPrefix(ref, "op://") {
		return readOP(ctx, ref, value)
	}
	return "", &Error{"invalid-ref", "仅支持 sops://、op://、env:// 凭据引用"}
}
