package plugin

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var sshUser = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

func (b *builtin) probeSSH(ctx context.Context, config map[string]any) (any, error) {
	var cfg struct {
		Host           string `json:"host"`
		User           string `json:"user"`
		Port           int    `json:"port"`
		IdentityFile   string `json:"identity_file"`
		KnownHostsFile string `json:"known_hosts_file"`
	}
	if err := decodeConfig(config, &cfg); err != nil {
		return nil, err
	}
	if !validHost(cfg.Host) || (cfg.User != "" && !sshUser.MatchString(cfg.User)) || cfg.Port < 0 || cfg.Port > 65535 {
		return nil, failure("config", "SSH host、user 或 port 无效")
	}
	args := []string{"-T", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "UpdateHostKeys=no", "-o", "PermitLocalCommand=no", "-o", "RemoteCommand=none", "-o", "ClearAllForwardings=yes", "-o", "ForwardAgent=no", "-o", "ForwardX11=no", "-o", "ControlPath=none", "-o", "ControlMaster=no", "-o", "ControlPersist=no", "-o", "ConnectionAttempts=1", "-o", "ConnectTimeout=10"}
	if cfg.User != "" {
		args = append(args, "-l", cfg.User)
	}
	if cfg.Port != 0 {
		args = append(args, "-p", strconv.Itoa(cfg.Port))
	}
	for _, file := range []string{cfg.IdentityFile, cfg.KnownHostsFile} {
		if strings.ContainsAny(file, "\r\n\x00") {
			return nil, failure("config", "SSH 文件路径不能包含控制字符")
		}
	}
	if cfg.IdentityFile != "" {
		args = append(args, "-i", repoPath(b.dir, cfg.IdentityFile))
	}
	if cfg.KnownHostsFile != "" {
		args = append(args, "-o", "UserKnownHostsFile="+strconv.Quote(repoPath(b.dir, cfg.KnownHostsFile)))
	}
	args = append(args, "--", cfg.Host, "uname -srm")
	command := exec.CommandContext(ctx, "ssh", args...)
	command.WaitDelay = 100 * time.Millisecond
	var out boundedOutput
	command.Stdout = &out
	if command.Run() != nil {
		return nil, failure("probe", "SSH 只读探测失败；请检查连接、主机密钥和非交互认证")
	}
	fields := strings.Fields(out.String())
	if len(fields) != 3 {
		return nil, failure("protocol", "SSH uname 响应格式不符")
	}
	return map[string]any{"host": cfg.Host, "system": fields[0], "release": fields[1], "architecture": fields[2]}, nil
}

func repoPath(dir, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(dir, path)
}

type boundedOutput struct{ bytes.Buffer }

func (b *boundedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 4096 {
		return 0, io.ErrShortBuffer
	}
	return b.Buffer.Write(p)
}
