package secret

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"filippo.io/age"
	"filippo.io/age/agessh"
	sopsage "github.com/getsops/sops/v3/age"
	"github.com/google/shlex"
)

// 只加载非交互身份；age 插件和口令保护的身份文件会绕过实例取消边界。
func ageIdentities(ctx context.Context, recipient string) (sopsage.ParsedIdentities, error) {
	identities := sopsage.ParsedIdentities{}
	parse := func(data []byte, ssh bool) {
		defer clear(data)
		if ssh {
			if identity, err := agessh.ParseIdentity(data); err == nil {
				identities = append(identities, identity)
			}
		} else if parsed, err := age.ParseIdentities(bytes.NewReader(data)); err == nil {
			identities = append(identities, parsed...)
		}
	}
	read := func(path string, ssh bool) {
		if data, err := os.ReadFile(path); err == nil {
			parse(data, ssh)
		}
	}
	// SOPS_AGE_KEY 接受同一行的多个身份；文件和命令保持每行一个身份。
	var envLines []string
	for _, line := range strings.Split(os.Getenv("SOPS_AGE_KEY"), "\n") {
		if !strings.HasPrefix(line, "#") {
			envLines = append(envLines, strings.Fields(line)...)
		}
	}
	parse([]byte(strings.Join(envLines, "\n")), false)
	read(os.Getenv("SOPS_AGE_KEY_FILE"), false)
	read(os.Getenv("SOPS_AGE_SSH_PRIVATE_KEY_FILE"), true)
	if home, err := os.UserHomeDir(); err == nil {
		read(filepath.Join(home, ".ssh", "id_ed25519"), true)
		read(filepath.Join(home, ".ssh", "id_rsa"), true)
	}
	configDir, _ := os.UserConfigDir()
	if runtime.GOOS == "darwin" && os.Getenv("XDG_CONFIG_HOME") != "" {
		configDir = os.Getenv("XDG_CONFIG_HOME")
	}
	if configDir != "" {
		read(filepath.Join(configDir, "sops", "age", "keys.txt"), false)
	}
	for _, source := range []struct {
		name string
		ssh  bool
	}{{"SOPS_AGE_KEY_CMD", false}, {"SOPS_AGE_SSH_PRIVATE_KEY_CMD", true}} {
		command, ok := os.LookupEnv(source.name)
		if !ok {
			continue
		}
		args, err := shlex.Split(command)
		if err != nil || len(args) == 0 {
			return nil, &Error{"invalid-config", "age 密钥命令无法解析"}
		}
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		cmd.Env = append(os.Environ(), "SOPS_AGE_RECIPIENT="+recipient)
		cmd.WaitDelay = 100 * time.Millisecond
		data, err := cmd.Output()
		if ctx.Err() != nil {
			clear(data)
			return nil, &Error{"cancelled", "凭据操作已取消"}
		}
		if err == nil {
			parse(data, source.ssh)
		} else {
			clear(data)
		}
	}
	return identities, nil
}
