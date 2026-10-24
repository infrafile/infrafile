package validate

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"

	"github.com/infrafile/infrafile/internal/ledger"
)

// checkDeleted 报告在 git 引用 ref 中存在、当前工作区里却没有的记录文件。
// 下线应当加 metadata.archived，而不是删文件。
func checkDeleted(repo *ledger.Repo, ref string) ([]ledger.Problem, error) {
	if strings.HasPrefix(ref, "-") {
		return nil, fmt.Errorf("--base 的值 %q 不是 git 引用", ref)
	}
	if _, err := git(repo.Dir, "rev-parse", "--verify", "--quiet", ref+"^{commit}"); err != nil {
		return nil, fmt.Errorf("读不到 git 基线 %s：%w", ref, err)
	}
	// ls-tree 的路径参数和输出都相对当前目录，所以数据仓在 git 仓库的子目录里也能用。
	out, err := git(repo.Dir, "ls-tree", "-r", "-z", "--name-only", ref, "--", ledger.InventoryDir)
	if err != nil {
		return nil, fmt.Errorf("读不到 git 基线 %s：%w", ref, err)
	}

	current := make(map[string]bool, len(repo.Files))
	for _, f := range repo.Files {
		current[f] = true
	}
	var problems []ledger.Problem
	for _, name := range strings.Split(string(out), "\x00") {
		rel := baseRel(name)
		if rel == "" || current[rel] {
			continue
		}
		problems = append(problems, ledger.Problem{Code: ledger.CodeDeleted, File: rel,
			Message: fmt.Sprintf("记录在 %s 中存在，现在被删除了；下线请改用 metadata.archived", ref)})
	}
	return problems, nil
}

// baseRel 返回 ls-tree 输出中的记录文件路径（git 默认输出相对当前目录的路径，
// 也就是相对数据仓），不是记录文件时返回空串。
func baseRel(name string) string {
	parts := strings.Split(name, "/")
	if len(parts) != 3 || parts[0] != ledger.InventoryDir ||
		!strings.HasSuffix(parts[2], ".yaml") || strings.HasPrefix(parts[2], ".") {
		return ""
	}
	if _, ok := ledger.KindByDir(parts[1]); !ok {
		return ""
	}
	return name
}

func git(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%s", msg)
		}
		return nil, err
	}
	return out, nil
}
