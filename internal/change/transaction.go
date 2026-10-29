package change

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type transaction struct {
	Version     string          `json:"version"`
	Kind        string          `json:"kind"`
	Data        transactionData `json:"data"`
	journalHash string
}
type transactionData struct {
	ID             string  `json:"id"`
	Owner          owner   `json:"owner"`
	Phase          string  `json:"phase"`
	Preview        Preview `json:"preview"`
	IndexBefore    []byte  `json:"index_before"`
	Tree           string  `json:"tree"`
	Commit         string  `json:"commit"`
	IndexAfterHash string  `json:"index_after_hash"`
}

func save(root *os.Root, t *transaction) error {
	b, err := encoded(t)
	if err != nil {
		return err
	}
	name := ".infrafile/transactions/" + t.Data.ID + ".json"
	err = atomicWrite(root, name, b, 0600, func() error {
		old, e := read(root, name, maxBytes)
		if e != nil {
			return e
		}
		if t.journalHash == "" {
			if old != nil {
				return errors.New("事务记录已存在")
			}
		} else if hash(old) != t.journalHash {
			return errors.New("事务记录已被外部修改")
		}
		return nil
	})
	if err == nil {
		t.journalHash = hash(b)
	}
	return err
}
func privateGit(r *repository, id string) string { return privatePath(r.dir, id, "") + "/git" }

func prepareIndex(ctx context.Context, r *repository, p *Preview, id string) (string, error) {
	gitDir := privateGit(r, id)
	if _, err := r.git(ctx, nil, nil, "init", "--bare", "--object-format="+r.format, gitDir); err != nil {
		return "", err
	}
	env := r.commitEnv(gitDir, id)
	if _, err := r.git(ctx, env, nil, "symbolic-ref", "HEAD", p.Base.Branch); err != nil {
		return "", err
	}
	if _, err := r.git(ctx, env, nil, "update-ref", p.Base.Branch, p.Base.Head); err != nil {
		return "", err
	}
	if _, err := r.git(ctx, env, nil, "read-tree", p.Base.Head); err != nil {
		return "", err
	}
	for _, f := range p.Files {
		attrs, err := r.git(ctx, nil, nil, "check-attr", "-z", "filter", "working-tree-encoding", "--", f.Path)
		if err != nil {
			return "", err
		}
		parts := bytes.Split(attrs, []byte{0})
		for i := 2; i < len(parts); i += 3 {
			if string(parts[i]) != "unspecified" && string(parts[i]) != "unset" {
				return "", errors.New("变更文件使用自定义 Git filter/encoding，请用手工 Git 流程")
			}
		}
		blob, err := r.git(ctx, env, []byte(f.After), "hash-object", "-w", "--stdin", "--no-filters")
		if err != nil {
			return "", err
		}
		mode := "100644"
		if f.Mode&0111 != 0 {
			mode = "100755"
		}
		if _, err = r.git(ctx, env, nil, "update-index", "--add", "--cacheinfo", mode+","+strings.TrimSpace(string(blob))+","+f.Path); err != nil {
			return "", err
		}
	}
	tree, err := r.git(ctx, env, nil, "write-tree")
	return strings.TrimSpace(string(tree)), err
}

func verifyFiles(root *os.Root, files []File, after bool) error {
	for _, f := range files {
		b, err := read(root, f.Path, maxBytes)
		expected := []byte(f.Before)
		exists := f.Existed
		if after {
			expected = []byte(f.After)
			exists = true
		}
		info, statErr := root.Lstat(f.Path)
		if err != nil || !bytes.Equal(b, expected) || (exists && (statErr != nil || !info.Mode().IsRegular() || uint32(info.Mode().Perm()) != f.Mode)) || (!exists && !os.IsNotExist(statErr)) {
			return errors.New("变更文件已被外部修改，保留现有内容")
		}
	}
	return nil
}

func (r *repository) identity(ctx context.Context, base Base) error {
	current, err := openRepository(ctx, r.dir)
	if err != nil || current.dir != base.Repository || current.gitDir != base.GitDir || current.commonDir != base.CommonDir || !os.SameFile(r.rootInfo, current.rootInfo) {
		return errors.New("数据仓 Git 身份已变化")
	}
	branch, err := current.text(ctx, "symbolic-ref", "-q", "HEAD")
	if err != nil || branch != base.Branch {
		return errors.New("分支已变化")
	}
	var cfg bytes.Buffer
	for _, e := range current.config {
		cfg.WriteString(e.key)
		cfg.WriteByte(0)
		cfg.WriteString(e.value)
		cfg.WriteByte(0)
	}
	if hash(cfg.Bytes()) != base.ConfigHash {
		return errors.New("Git 配置已变化")
	}
	return nil
}

func verifyInputs(ctx context.Context, r *repository, p *Preview, after bool) error {
	inputs, _, err := capture(ctx, r.dir)
	if err != nil {
		return err
	}
	for _, f := range p.Files {
		if after {
			if string(inputs[f.Path]) != f.After {
				return errors.New("候选内容已变化")
			}
		} else if string(inputs[f.Path]) != f.Before {
			return errors.New("原内容已变化")
		}
		if f.Existed {
			inputs[f.Path] = []byte(f.Before)
		} else {
			delete(inputs, f.Path)
		}
	}
	// Recreate the baseline including original modes; new directories are disallowed by Prepare.
	root, err := os.OpenRoot(r.dir)
	if err != nil {
		return err
	}
	defer root.Close()
	values := map[string]string{}
	for name, b := range inputs {
		info, e := root.Lstat(strings.TrimSuffix(name, "/"))
		if e != nil {
			return e
		}
		values[name] = hash(b) + ":" + info.Mode().String()
	}
	b, err := encoded(values)
	if err != nil || hash(b) != p.Base.InputHash {
		return errors.New("台账输入已发生额外变化")
	}
	return r.identity(ctx, p.Base)
}

func installIndex(r *repository, t *transaction) error {
	after, err := readExternal(filepath.Join(privateGit(r, t.Data.ID), "index"), maxBytes)
	if err != nil || hash(after) != t.Data.IndexAfterHash {
		return errors.New("事务索引损坏")
	}
	root, err := os.OpenRoot(r.gitDir)
	if err != nil {
		return err
	}
	defer root.Close()
	check := func() error {
		b, e := read(root, "index", maxBytes)
		if e != nil {
			return e
		}
		h := hash(b)
		if h != hash(t.Data.IndexBefore) && h != t.Data.IndexAfterHash {
			return errors.New("真实索引已被外部修改")
		}
		return checkLocks(r.lockNames(), t.Data.Owner)
	}
	return atomicWrite(root, "index", after, 0600, check)
}

// Apply never resets or pushes. Once active is durable, any failure requires explicit recovery.
func Apply(ctx context.Context, dir string, p *Preview) (Result, error) {
	return apply(ctx, dir, p, nil)
}

// step is a deterministic fault boundary used by integration tests, never a runtime option.
func apply(ctx context.Context, dir string, p *Preview, step func(string) error) (Result, error) {
	result := Result{Version: "v1", Status: "rejected", Code: "invalid_preview"}
	if p == nil || !p.OK {
		return result, nil
	}
	sum, err := p.sum()
	if err != nil || sum != p.Digest {
		return result, nil
	}
	r, err := openRepository(ctx, dir)
	if err != nil {
		return result, err
	}
	if _, busy := operations.LoadOrStore(r.dir, true); busy {
		result.Code = "busy"
		return result, nil
	}
	defer operations.Delete(r.dir)
	fresh, err := Prepare(ctx, r.dir, p.Request, p.PreserveSecrets)
	if err != nil || fresh.Digest != p.Digest {
		result.Code = "stale_or_dirty"
		return result, nil
	}
	if len(p.Files) == 0 {
		result.Status = "unchanged"
		result.Code = "no_changes"
		return result, nil
	}
	if err = privateDirs(ctx, r); err != nil {
		return result, err
	}
	id := randomID()
	o, names, err := acquire(r, id)
	if err != nil {
		_ = release(names, o)
		result.Code = "busy"
		return result, nil
	}
	active := false
	defer func() {
		if !active {
			_ = release(names, o)
		}
	}()
	fresh, err = prepare(ctx, r.dir, p.Request, p.PreserveSecrets, true)
	if err != nil || fresh.Digest != p.Digest {
		result.Code = "stale_or_dirty"
		return result, nil
	}
	root, err := os.OpenRoot(r.dir)
	if err != nil {
		return result, err
	}
	defer root.Close()
	// Kind directories already exist, so recovery never removes or invents user directories.
	for _, f := range p.Files {
		info, e := root.Lstat(filepath.ToSlash(filepath.Dir(f.Path)))
		if e != nil || !info.IsDir() {
			return result, errors.New("请先建立并提交记录的 kind 目录")
		}
	}
	index, err := readExternal(filepath.Join(r.gitDir, "index"), maxBytes)
	if err != nil {
		return result, err
	}
	if err = root.Mkdir(".infrafile/transactions/"+id, 0700); err != nil {
		return result, err
	}
	t := &transaction{Version: "v1", Kind: "EditTransaction", Data: transactionData{ID: id, Owner: o, Phase: "prepared", Preview: *p, IndexBefore: index}}
	initial, err := encoded(t)
	if err != nil {
		return result, err
	}
	if len(initial) > maxBytes-512 {
		return result, errors.New("事务需预留提交对象和恢复阶段空间，请缩小批次")
	}
	if err = save(root, t); err != nil {
		return result, err
	}
	active = true
	result.Status = "recovery_required"
	result.Code = "transaction_interrupted"
	result.Transaction = id
	if err = atomicWrite(root, ".infrafile/active", []byte(id+"\n"), 0600, func() error {
		_, e := root.Lstat(".infrafile/active")
		if !os.IsNotExist(e) {
			return errors.New("已有活动事务")
		}
		return checkLocks(names, o)
	}); err != nil {
		return result, nil
	}
	checkpoint := func(name string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := checkLocks(names, o); err != nil {
			return err
		}
		if step != nil {
			return step(name)
		}
		return nil
	}
	fail := func() (Result, error) { return result, nil }
	if checkpoint("prepared") != nil {
		return fail()
	}
	if t.Data.Tree, err = prepareIndex(ctx, r, p, id); err != nil {
		result.Code = "index_prepare_failed"
		return fail()
	}
	t.Data.Phase = "writing"
	if save(root, t) != nil {
		return fail()
	}
	for i, f := range p.Files {
		if checkpoint(fmt.Sprintf("before_write_%d", i)) != nil {
			return fail()
		}
		if err = atomicWrite(root, f.Path, []byte(f.After), os.FileMode(f.Mode), func() error {
			if e := r.identity(ctx, p.Base); e != nil {
				return e
			}
			if e := checkLocks(names, o); e != nil {
				return e
			}
			return verifyFiles(root, []File{f}, false)
		}); err != nil {
			return fail()
		}
	}
	if checkpoint("written") != nil || verifyFiles(root, p.Files, true) != nil || verifyInputs(ctx, r, p, true) != nil {
		return fail()
	}
	env := r.commitEnv(privateGit(r, id), id)
	message := "infrafile: apply inventory change\n\nInfrafile-Transaction: " + id
	if _, err = r.git(ctx, env, nil, "commit", "-s", "-m", message); err != nil {
		result.Code = "commit_failed"
		return fail()
	}
	commit, err := r.git(ctx, env, nil, "rev-parse", "HEAD")
	if err != nil {
		return fail()
	}
	t.Data.Commit = strings.TrimSpace(string(commit))
	if err = verifyCommit(ctx, r, t); err != nil {
		result.Code = "commit_changed"
		return fail()
	}
	afterIndex, err := readExternal(filepath.Join(privateGit(r, id), "index"), maxBytes)
	if err != nil {
		return fail()
	}
	t.Data.IndexAfterHash = hash(afterIndex)
	t.Data.Phase = "ready"
	if save(root, t) != nil || checkpoint("ready") != nil || verifyInputs(ctx, r, p, true) != nil || verifyFiles(root, p.Files, true) != nil {
		return fail()
	}
	if err = onlyChanges(ctx, r, p); err != nil {
		return fail()
	}
	actualIndex, err := readExternal(filepath.Join(r.gitDir, "index"), maxBytes)
	if err != nil || hash(actualIndex) != p.Base.IndexHash {
		return fail()
	}
	// Share real refs, but keep HEAD and its reflog in the private directory while the real HEAD stays locked.
	if err = atomicWrite(root, ".infrafile/transactions/"+id+"/git/commondir", []byte(r.commonDir+"\n"), 0600, func() error { return checkLocks(names, o) }); err != nil {
		return fail()
	}
	publishEnv := append(r.commitEnv(privateGit(r, id), id), "GIT_COMMON_DIR="+r.commonDir)
	if _, err = r.git(ctx, publishEnv, nil, "update-ref", p.Base.Branch, t.Data.Commit, p.Base.Head); err != nil {
		result.Code = "head_changed"
		return fail()
	}
	if checkpoint("published") != nil {
		return fail()
	}
	if err = installIndex(r, t); err != nil {
		result.Code = "index_finalize_failed"
		return fail()
	}
	if checkpoint("indexed") != nil || verifyInputs(ctx, r, p, true) != nil || verifyFiles(root, p.Files, true) != nil {
		return fail()
	}
	if err = onlyChanges(ctx, r, p); err != nil {
		return fail()
	}
	if head, e := r.text(ctx, "rev-parse", "HEAD"); e != nil || head != t.Data.Commit {
		return fail()
	}
	t.Data.Phase = "complete"
	if save(root, t) != nil {
		return fail()
	}
	if err = removeActive(root, id); err != nil {
		return fail()
	}
	if err = release(names, o); err != nil {
		return fail()
	}
	active = false
	result.Status = "committed"
	result.Code = "committed"
	result.Commit = t.Data.Commit
	return result, nil
}

func verifyCommit(ctx context.Context, r *repository, t *transaction) error {
	commit := t.Data.Commit
	if !objectID(commit, r.format) || !objectID(t.Data.Tree, r.format) {
		return errors.New("提交对象无效")
	}
	line, err := r.text(ctx, "show", "-s", "--format=%T%n%P%n%B", commit)
	parts := strings.SplitN(line, "\n", 3)
	if err != nil || len(parts) != 3 || parts[0] != t.Data.Tree || parts[1] != t.Data.Preview.Base.Head || !strings.Contains(parts[2], "\nInfrafile-Transaction: "+t.Data.ID) {
		return errors.New("钩子改变了提交树、父提交或事务标记")
	}
	return nil
}
func objectID(value, format string) bool {
	n := 40
	if format == "sha256" {
		n = 64
	}
	if len(value) != n {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func onlyChanges(ctx context.Context, r *repository, p *Preview) error {
	b, err := r.git(ctx, nil, nil, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignore-submodules=none")
	if err != nil {
		return err
	}
	allowed := map[string]bool{"state/.sync.lock": true}
	for _, f := range p.Files {
		allowed[f.Path] = true
	}
	for _, line := range bytes.Split(b, []byte{0}) {
		if len(line) == 0 {
			continue
		}
		if len(line) < 4 || line[0] == 'R' || line[1] == 'R' || !allowed[string(line[3:])] {
			return errors.New("仓库出现本次之外的修改")
		}
	}
	return nil
}
func removeActive(root *os.Root, id string) error {
	b, err := read(root, ".infrafile/active", 1024)
	if err != nil || string(b) != id+"\n" {
		return errors.New("活动事务标记已变化")
	}
	if err = root.Remove(".infrafile/active"); err != nil {
		return err
	}
	child, err := root.OpenRoot(".infrafile")
	if err != nil {
		return err
	}
	defer child.Close()
	return syncDir(child)
}
