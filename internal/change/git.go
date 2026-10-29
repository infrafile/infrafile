package change

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type configEntry struct{ key, value string }
type repository struct {
	dir, gitDir, commonDir, objects, hooks, format string
	rootInfo                                       os.FileInfo
	config                                         []configEntry
}
type limitWriter struct{ bytes.Buffer }

func (w *limitWriter) Write(b []byte) (int, error) {
	if w.Len()+len(b) > maxBytes {
		return 0, errors.New("Git 输出超过上限")
	}
	return w.Buffer.Write(b)
}

func gitEnv() []string {
	env := []string{}
	for _, v := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(v), "GIT_") && !strings.HasPrefix(v, "INFRAFILE_EDIT_READ=") {
			env = append(env, v)
		}
	}
	return append(env, "GIT_TERMINAL_PROMPT=0", "GIT_NO_LAZY_FETCH=1", "GIT_NO_REPLACE_OBJECTS=1", "GIT_OPTIONAL_LOCKS=0")
}
func (r *repository) git(ctx context.Context, extra []string, input []byte, args ...string) ([]byte, error) {
	argv := append([]string{"-C", r.dir, "-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false"}, args...)
	cmd := exec.CommandContext(ctx, "git", argv...)
	cmd.WaitDelay = 2 * time.Second
	cmd.Env = append(gitEnv(), extra...)
	cmd.Stdin = bytes.NewReader(input)
	var out limitWriter
	cmd.Stdout = &out
	cmd.Stderr = &limitWriter{}
	if cmd.Run() != nil {
		return nil, errors.New("Git 操作失败；保留原内容和恢复记录")
	}
	return out.Bytes(), nil
}
func (r *repository) text(ctx context.Context, args ...string) (string, error) {
	b, err := r.git(ctx, nil, nil, args...)
	return strings.TrimSpace(string(b)), err
}

func openRepository(ctx context.Context, dir string) (*repository, error) {
	dir, err := filepath.Abs(dir)
	if err == nil {
		dir, err = filepath.EvalSymlinks(dir)
	}
	if err != nil {
		return nil, errors.New("无法固定数据仓")
	}
	r := &repository{dir: dir}
	r.rootInfo, err = os.Lstat(dir)
	if err != nil || !r.rootInfo.IsDir() {
		return nil, errors.New("数据仓不是普通目录")
	}
	top, err := r.text(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, errors.New("变更需要已有初始提交的数据 Git 仓")
	}
	top, err = filepath.EvalSymlinks(top)
	if err != nil || top != dir {
		return nil, errors.New("数据仓必须位于 Git 工作树根目录")
	}
	for _, field := range []struct {
		args []string
		dst  *string
	}{{[]string{"rev-parse", "--absolute-git-dir"}, &r.gitDir}, {[]string{"rev-parse", "--git-common-dir"}, &r.commonDir}, {[]string{"rev-parse", "--git-path", "objects"}, &r.objects}, {[]string{"rev-parse", "--git-path", "hooks"}, &r.hooks}, {[]string{"rev-parse", "--show-object-format"}, &r.format}} {
		value, e := r.text(ctx, field.args...)
		if e != nil {
			return nil, e
		}
		if field.dst != &r.format {
			if !filepath.IsAbs(value) {
				value = filepath.Join(dir, value)
			}
			resolved, e := filepath.EvalSymlinks(value)
			if e != nil && !os.IsNotExist(e) {
				return nil, e
			}
			if e == nil {
				value = resolved
			}
		}
		*field.dst = value
	}
	if r.format != "sha1" && r.format != "sha256" {
		return nil, errors.New("不支持 Git 对象格式")
	}
	b, err := r.git(ctx, nil, nil, "config", "--null", "--list")
	if err != nil || len(b) > 64<<10 {
		return nil, errors.New("Git 配置无法有界读取")
	}
	for _, entry := range bytes.Split(b, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		parts := bytes.SplitN(entry, []byte{'\n'}, 2)
		value := ""
		if len(parts) == 2 {
			value = string(parts[1])
		}
		r.config = append(r.config, configEntry{string(parts[0]), value})
	}
	return r, nil
}

func (r *repository) base(ctx context.Context, inputHash string, locked bool) (Base, error) {
	b := Base{Repository: r.dir, GitDir: r.gitDir, CommonDir: r.commonDir, InputHash: inputHash}
	var err error
	b.Branch, err = r.text(ctx, "symbolic-ref", "-q", "HEAD")
	if err != nil || !strings.HasPrefix(b.Branch, "refs/heads/") {
		return b, errors.New("变更需要已提交的普通分支")
	}
	b.Head, err = r.text(ctx, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return b, err
	}
	index, err := readExternal(filepath.Join(r.gitDir, "index"), maxBytes)
	if err != nil {
		return b, err
	}
	b.IndexHash = hash(index)
	var cfg bytes.Buffer
	for _, e := range r.config {
		cfg.WriteString(e.key)
		cfg.WriteByte(0)
		cfg.WriteString(e.value)
		cfg.WriteByte(0)
	}
	b.ConfigHash = hash(cfg.Bytes())
	args := []string{"status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignore-submodules=none", "--", "."}
	if locked {
		args = append(args, ":(exclude)state/.sync.lock")
	}
	status, err := r.git(ctx, nil, nil, args...)
	if err != nil || len(status) != 0 {
		return b, errors.New("工作树或暂存区有修改，拒绝工具写入")
	}
	shared, err := r.text(ctx, "rev-parse", "--shared-index-path")
	if err != nil || shared != "" {
		return b, errors.New("不支持 split-index")
	}
	flags, err := r.git(ctx, nil, nil, "ls-files", "-v", "-z")
	if err != nil {
		return b, err
	}
	for _, line := range bytes.Split(flags, []byte{0}) {
		if len(line) > 0 && line[0] != 'H' {
			return b, errors.New("不支持稀疏、未合并或 assume-unchanged 索引")
		}
	}
	for _, name := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "rebase-merge", "rebase-apply"} {
		if _, e := os.Lstat(filepath.Join(r.gitDir, name)); !os.IsNotExist(e) {
			return b, errors.New("Git 操作未完成，拒绝工具写入")
		}
	}
	return b, nil
}

// An isolated Git directory keeps hooks and branch naming, then a CAS publishes only the verified tree.
func (r *repository) commitEnv(gitDir, transaction string) []string {
	entries := []configEntry{}
	for _, e := range r.config {
		if e.key == "include.path" || strings.HasPrefix(e.key, "includeif.") {
			continue
		}
		entries = append(entries, e)
	}
	entries = append(entries, configEntry{"core.bare", "false"}, configEntry{"core.worktree", r.dir}, configEntry{"core.hooksPath", r.hooks}, configEntry{"extensions.worktreeConfig", "false"}, configEntry{"core.splitIndex", "false"})
	env := []string{"GIT_DIR=" + gitDir, "GIT_WORK_TREE=" + r.dir, "GIT_OBJECT_DIRECTORY=" + r.objects, "INFRAFILE_EDIT_READ=" + transaction, "GIT_CONFIG_COUNT=" + strconv.Itoa(len(entries))}
	for i, e := range entries {
		env = append(env, "GIT_CONFIG_KEY_"+strconv.Itoa(i)+"="+e.key, "GIT_CONFIG_VALUE_"+strconv.Itoa(i)+"="+e.value)
	}
	return env
}
