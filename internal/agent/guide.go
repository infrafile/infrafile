// Package agent 保存随二进制发布的 Agent 指南及项目级安装逻辑。
package agent

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed guide.md
var guide string

func Guide(version, commit string) string {
	return strings.NewReplacer("{{VERSION}}", version, "{{COMMIT}}", commit).Replace(guide)
}

func Install(dir, client, content string) (path, status string, err error) {
	folder := ""
	switch client {
	case "codex":
		folder = ".agents"
	case "claude":
		folder = ".claude"
	default:
		return "", "", fmt.Errorf("客户端必须是 codex 或 claude")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", "", err
	}
	defer root.Close()
	for _, part := range []string{folder, "skills", "infrafile"} {
		info, err := root.Lstat(part)
		if os.IsNotExist(err) {
			err = root.Mkdir(part, 0755)
		} else if err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.IsDir()) {
			return "", "", fmt.Errorf("技能目录不能是符号链接或普通文件")
		}
		if err != nil {
			return "", "", err
		}
		next, err := root.OpenRoot(part)
		if err != nil {
			return "", "", err
		}
		defer next.Close()
		root = next
	}
	path = filepath.Join(dir, folder, "skills", "infrafile", "SKILL.md")
	if info, err := root.Lstat("SKILL.md"); err == nil {
		if !info.Mode().IsRegular() {
			return "", "", fmt.Errorf("已有指南不是普通文件")
		}
		data, err := root.ReadFile("SKILL.md")
		if err != nil {
			return "", "", err
		}
		if bytes.Equal(data, []byte(content)) {
			return path, "unchanged", nil
		}
		return "", "", fmt.Errorf("已有指南内容不同，已保留；请审阅并移走旧文件后再安装")
	} else if !os.IsNotExist(err) {
		return "", "", err
	}
	f, err := root.OpenFile("SKILL.md", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return "", "", err
	}
	_, writeErr := f.WriteString(content)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		root.Remove("SKILL.md")
		return "", "", fmt.Errorf("指南写入失败")
	}
	return path, "created", nil
}
