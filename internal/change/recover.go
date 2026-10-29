package change

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/infrafile/infrafile/internal/ledger"
)

func loadTransaction(ctx context.Context, r *repository, id string) (*transaction, error) {
	if !digestPattern.MatchString(id) {
		return nil, errors.New("事务 ID 无效")
	}
	root, err := os.OpenRoot(r.dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	b, err := read(root, ".infrafile/transactions/"+id+".json", maxBytes)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, os.ErrNotExist
	}
	var t transaction
	if err = decode(ctx, b, &t); err != nil {
		return nil, err
	}
	d := &t.Data
	p := &d.Preview
	sum, err := p.sum()
	if err != nil || sum != p.Digest || !p.OK || p.Version != "v1" || p.Kind != "EditPreview" || t.Version != "v1" || t.Kind != "EditTransaction" || d.ID != id || d.Owner.ID != id || d.Owner.Version != "v1" || d.Owner.PID <= 0 || p.Base.Repository != r.dir || p.Base.GitDir != r.gitDir || !objectID(p.Base.Head, r.format) || hash(d.IndexBefore) != p.Base.IndexHash || len(p.Files) > maxFiles {
		return nil, errors.New("事务身份、版本或基准损坏")
	}
	switch d.Phase {
	case "prepared", "writing", "ready", "complete", "rolled_back":
	default:
		return nil, errors.New("事务阶段损坏")
	}
	seen := map[string]bool{}
	for _, f := range p.Files {
		doc, ps, e := ledger.ParseDocument(ctx, "candidate", []byte(f.After))
		if e != nil || len(ps) != 0 || doc == nil {
			return nil, errors.New("事务候选损坏")
		}
		record, _ := doc.Value.(map[string]any)
		key, name, e := recordPath(record)
		if e != nil || f.Path != name || f.Key != key || seen[name] || f.Mode&^0777 != 0 || f.BeforeHash != hash([]byte(f.Before)) || f.AfterHash != hash([]byte(f.After)) || (!f.Existed && f.Before != "") {
			return nil, errors.New("事务文件或前后哈希损坏")
		}
		seen[name] = true
	}
	if d.Commit != "" {
		if !digestPattern.MatchString(d.IndexAfterHash) || verifyCommit(ctx, r, &t) != nil {
			return nil, errors.New("事务提交损坏")
		}
	}
	t.journalHash = hash(b)
	return &t, nil
}

// Status is read-only and reports lock/active mismatches without loading partial inventory.
func readActive(root *os.Root) ([]byte, error) {
	b, err := read(root, ".infrafile/active", 1024)
	if err != nil {
		return nil, err
	}
	info, e := root.Lstat(".infrafile/active")
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil || !info.Mode().IsRegular() || len(b) != 65 || b[64] != '\n' || !digestPattern.Match(b[:64]) {
		return nil, errors.New("活动事务标记损坏，保留现场")
	}
	return b, nil
}

func Status(ctx context.Context, dir string) (Result, error) {
	result := Result{Version: "v1", Status: "clean", Code: "no_active_transaction"}
	r, err := openRepository(ctx, dir)
	if err != nil {
		return result, err
	}
	root, err := os.OpenRoot(r.dir)
	if err != nil {
		return result, err
	}
	defer root.Close()
	info, e := root.Lstat(".infrafile")
	if os.IsNotExist(e) {
		return result, nil
	}
	if e != nil || !info.IsDir() {
		return result, errors.New("事务目录不是普通目录")
	}
	b, e := readActive(root)
	o, ownerErr := readOwner(ctx, r)
	if e != nil {
		return result, e
	}
	if len(b) == 0 && os.IsNotExist(ownerErr) {
		return result, nil
	}
	result.Status = "recovery_required"
	result.Code = "unfinished_transaction"
	if ownerErr != nil {
		return result, errors.New("活动事务与写锁不一致，保留现场")
	}
	result.Transaction = o.ID
	if len(b) != 0 && string(b) != o.ID+"\n" {
		return result, errors.New("活动事务标记与写锁不一致")
	}
	_, err = loadTransaction(ctx, r, o.ID)
	if err != nil && !os.IsNotExist(err) {
		return result, err
	}
	return result, nil
}

// Recover rolls back only matching owned bytes, or completes an already published, verified commit.
func Recover(ctx context.Context, dir, id string) (Result, error) {
	result := Result{Version: "v1", Status: "recovery_required", Code: "recovery_refused", Transaction: id}
	if err := supportsEdits(); err != nil {
		return result, err
	}
	if !digestPattern.MatchString(id) {
		return result, errors.New("事务 ID 无效")
	}
	r, err := openRepository(ctx, dir)
	if err != nil {
		return result, err
	}
	if _, busy := operations.LoadOrStore(r.dir, true); busy {
		return result, nil
	}
	defer operations.Delete(r.dir)
	root, err := os.OpenRoot(r.dir)
	if err != nil {
		return result, err
	}
	defer root.Close()
	info, err := root.Lstat(".infrafile")
	if err != nil || !info.IsDir() {
		return result, errors.New("事务目录不是普通目录")
	}
	if info, err = root.Lstat(".infrafile/recovery.lock"); err == nil && !info.Mode().IsRegular() {
		return result, errors.New("恢复锁不是普通文件")
	}
	claim, err := root.OpenFile(".infrafile/recovery.lock", os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return result, err
	}
	defer claim.Close()
	if lockRecovery(claim) != nil {
		result.Code = "busy"
		return result, nil
	}
	o, err := readOwner(ctx, r)
	t, te := loadTransaction(ctx, r, id)
	if os.IsNotExist(err) && te == nil && (t.Data.Phase == "complete" || t.Data.Phase == "rolled_back") {
		result.Status = t.Data.Phase
		result.Code = "already_finished"
		result.Commit = t.Data.Commit
		return result, nil
	}
	if err != nil || o.ID != id {
		return result, errors.New("写锁不属于指定事务")
	}
	if o.PID != os.Getpid() && processAlive(o.PID) {
		result.Code = "owner_running"
		return result, nil
	}
	active, e := readActive(root)
	if e != nil {
		return result, e
	}
	if len(active) > 0 && string(active) != id+"\n" {
		return result, errors.New("活动事务已变化")
	}
	// A crash during lock acquisition precedes journal/active creation and cannot have written records.
	if os.IsNotExist(te) && len(active) == 0 {
		names := []string{}
		for _, name := range r.lockNames() {
			if _, e := os.Lstat(name); os.IsNotExist(e) {
				continue
			}
			if checkLocks([]string{name}, o) != nil {
				return result, errors.New("孤立写锁所有者不一致")
			}
			names = append(names, name)
		}
		if _, e = r.base(ctx, "", true); e != nil {
			return result, e
		}
		if e = release(names, o); e != nil {
			return result, e
		}
		result.Status = "rolled_back"
		result.Code = "unused_locks_released"
		return result, nil
	}
	if te != nil {
		return result, te
	}
	if t.Data.Owner != o {
		return result, errors.New("事务与锁所有者不一致")
	}
	// Finish interrupted lock cleanup only for durable terminal states.
	terminal := t.Data.Phase == "complete" || t.Data.Phase == "rolled_back"
	names := []string{}
	for _, name := range r.lockNames() {
		if _, e = os.Lstat(name); os.IsNotExist(e) && terminal {
			continue
		}
		if e = checkLocks([]string{name}, o); e != nil {
			return result, e
		}
		names = append(names, name)
	}
	if len(active) == 0 && terminal {
		if err = release(names, o); err != nil {
			return result, err
		}
		result.Status = t.Data.Phase
		result.Code = "cleanup_completed"
		result.Commit = t.Data.Commit
		return result, nil
	}
	if len(active) == 0 && t.Data.Phase == "prepared" {
		if verifyFiles(root, t.Data.Preview.Files, false) != nil {
			return result, errors.New("尚未激活的事务文件已变化")
		}
		base, e := r.base(ctx, t.Data.Preview.Base.InputHash, true)
		if e != nil || base != t.Data.Preview.Base {
			return result, errors.New("尚未激活的事务基准已变化")
		}
		t.Data.Phase = "rolled_back"
		if err = save(root, t); err != nil {
			return result, err
		}
		if err = release(names, o); err != nil {
			return result, err
		}
		result.Status = "rolled_back"
		result.Code = "unused_locks_released"
		return result, nil
	}
	if len(active) == 0 {
		return result, errors.New("非终态事务缺少 active 标记，保留现场")
	}
	if err = r.identity(ctx, t.Data.Preview.Base); err != nil {
		return result, err
	}
	head, err := r.text(ctx, "rev-parse", "HEAD")
	if err != nil {
		return result, err
	}
	p := &t.Data.Preview
	if t.Data.Commit != "" && head == t.Data.Commit {
		if verifyFiles(root, p.Files, true) != nil || verifyInputs(ctx, r, p, true) != nil || onlyChanges(ctx, r, p) != nil {
			return result, errors.New("提交后内容被外部修改，保留现场")
		}
		if err = installIndex(r, t); err != nil {
			return result, err
		}
		t.Data.Phase = "complete"
		result.Status = "committed"
		result.Code = "commit_finalized"
		result.Commit = head
	} else if head == p.Base.Head {
		index, e := readExternal(filepath.Join(r.gitDir, "index"), maxBytes)
		if e != nil || !bytes.Equal(index, t.Data.IndexBefore) {
			return result, errors.New("索引已被外部修改，保留现场")
		}
		// Check the entire batch before touching any file; an external edit preserves all remaining evidence.
		after := []File{}
		for _, f := range p.Files {
			if verifyFiles(root, []File{f}, false) == nil {
				continue
			}
			if verifyFiles(root, []File{f}, true) != nil {
				return result, errors.New("文件已被外部修改，恢复停止")
			}
			after = append(after, f)
		}
		for _, f := range after {
			if e := ctx.Err(); e != nil {
				return result, e
			}
			if e := checkLocks(names, o); e != nil {
				return result, e
			}
			if f.Existed {
				err = atomicWrite(root, f.Path, []byte(f.Before), os.FileMode(f.Mode), func() error { return verifyFiles(root, []File{f}, true) })
			} else {
				if err = verifyFiles(root, []File{f}, true); err == nil {
					err = root.Remove(f.Path)
					if err == nil {
						parent, e := root.OpenRoot(filepath.ToSlash(filepath.Dir(f.Path)))
						if e != nil {
							return result, e
						}
						err = syncDir(parent)
						parent.Close()
					}
				}
			}
			if err != nil {
				return result, err
			}
		}
		if verifyFiles(root, p.Files, false) != nil {
			return result, errors.New("回滚后文件复核失败")
		}
		t.Data.Phase = "rolled_back"
		result.Status = "rolled_back"
		result.Code = "original_files_restored"
	} else {
		return result, errors.New("HEAD 已被外部修改，恢复不会重置分支")
	}
	if err = save(root, t); err != nil {
		return result, err
	}
	if err = removeActive(root, id); err != nil {
		return result, err
	}
	if err = release(names, o); err != nil {
		return result, err
	}
	return result, nil
}
