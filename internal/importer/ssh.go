package importer

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/google/shlex"
)

type directive struct {
	key      string
	values   []string
	includes [][]directive
}

const (
	maxSSHFiles       = 1024
	maxSSHBytes       = 8 << 20
	maxSSHEvaluations = 1_000_000
)

type sshBudget struct {
	ctx                       context.Context
	files, bytes, evaluations int
}

func SSHConfig(ctx context.Context, file string) ([]Entry, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, errors.New("无法确定 SSH 用户目录")
	}
	aliases := map[string]bool{}
	budget := &sshBudget{ctx: ctx}
	directives, err := budget.readSSH(file, home, aliases, map[string]bool{})
	if err != nil {
		return nil, err
	}
	names := []string{}
	for alias := range aliases {
		names = append(names, alias)
	}
	sort.Strings(names)
	entries := []Entry{}
	for _, alias := range names {
		values := map[string]string{}
		declared, err := budget.evaluateSSH(directives, alias, values)
		if err != nil {
			return nil, err
		}
		if !declared {
			continue
		}
		address := values["hostname"]
		if address == "" {
			address = alias
		}
		address = strings.ReplaceAll(address, "%h", alias)
		if strings.ContainsAny(address, "%\r\n\x00 \t") {
			return nil, errors.New("SSH HostName 必须是可导入的字面量地址")
		}
		spec := map[string]any{"address": address}
		ssh := map[string]any{}
		if values["user"] != "" {
			if strings.ContainsAny(values["user"], "%\r\n\x00 \t") {
				return nil, errors.New("SSH User 必须是非空的字面量用户名，不支持 token")
			}
			ssh["user"] = values["user"]
		}
		if values["port"] != "" {
			port, err := strconv.Atoi(values["port"])
			if err != nil || port < 1 || port > 65535 {
				return nil, errors.New("SSH Port 必须为 1–65535 的整数")
			}
			ssh["port"] = port
		}
		if len(ssh) > 0 {
			spec["ssh"] = ssh
		}
		name := strings.Trim(unsafeName.ReplaceAllString(strings.ToLower(alias), "-"), ".-")
		entries = append(entries, draft("ssh-config/"+alias, "Server", name, spec))
	}
	return entries, nil
}

func (b *sshBudget) readSSH(file, home string, aliases, stack map[string]bool) ([]directive, error) {
	if err := b.ctx.Err(); err != nil {
		return nil, err
	}
	b.files++
	if b.files > maxSSHFiles {
		return nil, errors.New("SSH Include 累计读取超过 1024 次（重复引用也计入）")
	}
	canonical, err := filepath.EvalSymlinks(file)
	if err != nil {
		return nil, errors.New("无法读取 SSH config 或 Include 文件")
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil || stack[canonical] || len(stack) >= 16 {
		return nil, errors.New("SSH Include 存在循环或嵌套超过 16 层")
	}
	stack[canonical] = true
	defer delete(stack, canonical)
	f, err := os.Open(file)
	if err != nil {
		return nil, errors.New("无法读取 SSH config")
	}
	defer f.Close()
	directives := []directive{}
	scanner := bufio.NewScanner(f)
	for line := 1; scanner.Scan(); line++ {
		if err := b.ctx.Err(); err != nil {
			return nil, err
		}
		b.bytes += len(scanner.Bytes()) + 1
		if b.bytes > maxSSHBytes {
			return nil, errors.New("SSH config 累计读取超过 8 MiB（重复引用也计入）")
		}
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		n := strings.IndexAny(text, " \t=")
		if n < 0 {
			return nil, fmt.Errorf("SSH config 第 %d 行缺少参数", line)
		}
		key, rest := strings.ToLower(text[:n]), strings.TrimSpace(text[n:])
		rest = strings.TrimSpace(strings.TrimPrefix(rest, "="))
		if key == "match" {
			return nil, fmt.Errorf("SSH config 第 %d 行含暂不支持的条件或包含规则", line)
		}
		if key != "host" && key != "hostname" && key != "user" && key != "port" && key != "include" && key != "canonicalizehostname" {
			continue
		}
		values, err := shlex.Split(rest)
		if err != nil || len(values) == 0 || (key != "host" && key != "include" && len(values) != 1) {
			return nil, fmt.Errorf("SSH config 第 %d 行参数格式错误", line)
		}
		if key == "canonicalizehostname" {
			if values[0] != "no" && values[0] != "false" {
				return nil, errors.New("SSH 地址规范化需要实际解析，导入不支持")
			}
			continue
		}
		d := directive{key: key, values: values}
		if key == "include" {
			for _, pattern := range values {
				if strings.ContainsAny(pattern, "%$\x00") {
					return nil, errors.New("SSH Include 不支持 token 或环境变量展开")
				}
				if strings.HasPrefix(pattern, "~/") {
					pattern = filepath.Join(home, pattern[2:])
				} else if strings.HasPrefix(pattern, "~") {
					return nil, errors.New("SSH Include 仅支持当前用户的 ~/ 路径")
				}
				if !filepath.IsAbs(pattern) {
					pattern = filepath.Join(home, ".ssh", pattern)
				}
				files, err := filepath.Glob(pattern)
				if err != nil {
					return nil, errors.New("SSH Include 通配路径无效")
				}
				for _, included := range files {
					child, err := b.readSSH(included, home, aliases, stack)
					if err != nil {
						return nil, err
					}
					d.includes = append(d.includes, child)
				}
			}
		}
		directives = append(directives, d)
		if key == "host" {
			for _, alias := range values {
				if !strings.ContainsAny(alias, "*?!") {
					aliases[alias] = true
				}
			}
		}
	}
	if scanner.Err() != nil {
		return nil, errors.New("SSH config 无法完整读取")
	}
	return directives, nil
}

func (b *sshBudget) evaluateSSH(directives []directive, alias string, values map[string]string) (bool, error) {
	if err := b.ctx.Err(); err != nil {
		return false, err
	}
	active, declared := true, false
	for _, d := range directives {
		if err := b.ctx.Err(); err != nil {
			return false, err
		}
		b.evaluations += 1 + len(d.values) + len(d.includes)
		if b.evaluations > maxSSHEvaluations {
			return false, errors.New("SSH config 累计求值工作量超过 1000000")
		}
		if d.key == "host" {
			active = matchHost(d.values, alias)
			for _, p := range d.values {
				if active && p == alias {
					declared = true
				}
			}
		} else if d.key == "include" {
			if active {
				for _, child := range d.includes {
					childDeclared, err := b.evaluateSSH(child, alias, values)
					if err != nil {
						return false, err
					}
					declared = childDeclared || declared
				}
			}
		} else if active && values[d.key] == "" {
			values[d.key] = d.values[0]
		}
	}
	return declared, nil
}

func matchHost(patterns []string, alias string) bool {
	match := false
	for _, pattern := range patterns {
		negated := strings.HasPrefix(pattern, "!")
		literal := regexp.QuoteMeta(strings.TrimPrefix(pattern, "!"))
		expression := strings.NewReplacer(`\*`, ".*", `\?`, ".").Replace(literal)
		ok, _ := regexp.MatchString("^"+expression+"$", alias)
		if ok && negated {
			return false
		}
		match = match || ok
	}
	return match
}
