package change

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

type owner struct {
	Version string `json:"version"`
	ID      string `json:"id"`
	PID     int    `json:"pid"`
}

var operations sync.Map

func (r *repository) lockNames() []string {
	return []string{filepath.Join(r.dir, ".infrafile", "edit.lock"), filepath.Join(r.dir, "state", ".sync.lock"), filepath.Join(r.gitDir, "index.lock"), filepath.Join(r.gitDir, "HEAD.lock")}
}

func privateDirs(ctx context.Context, r *repository) error {
	for _, name := range []string{".infrafile", ".infrafile/transactions"} {
		ignored, err := r.git(ctx, nil, []byte(name+"/probe\n"), "check-ignore", "--no-index", "--stdin")
		if err != nil || len(ignored) == 0 {
			return errors.New("请先在 Git 中提交 /.infrafile/ 忽略规则，再运行变更")
		}
		info, err := os.Lstat(filepath.Join(r.dir, name))
		if os.IsNotExist(err) {
			err = os.Mkdir(filepath.Join(r.dir, name), 0700)
		} else if err == nil && (!info.IsDir() || info.Mode().Perm()&0077 != 0) {
			return errors.New("事务目录必须是私有普通目录（0700）")
		}
		if err != nil {
			return err
		}
	}
	tracked, err := r.git(ctx, nil, nil, "ls-files", "-z", "--", ".infrafile")
	if err != nil || len(tracked) != 0 {
		return errors.New(".infrafile 不得纳入 Git")
	}
	return nil
}

func acquire(r *repository, id string) (owner, []string, error) {
	o := owner{Version: "v1", ID: id, PID: os.Getpid()}
	b, _ := json.Marshal(o)
	acquired := []string{}
	state := filepath.Join(r.dir, "state")
	info, err := os.Lstat(state)
	if os.IsNotExist(err) {
		err = os.Mkdir(state, 0700)
	} else if err == nil && !info.IsDir() {
		err = errors.New("state 不是普通目录")
	}
	if err != nil {
		return o, acquired, err
	}
	for _, name := range r.lockNames() {
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return o, acquired, errors.New("仓库、采集或 Git 写锁已被占用")
		}
		acquired = append(acquired, name)
		err = writeAll(f, b)
		if err == nil {
			err = f.Sync()
		}
		ce := f.Close()
		if err != nil {
			return o, acquired, err
		}
		if ce != nil {
			return o, acquired, ce
		}
	}
	return o, acquired, nil
}

func checkLocks(names []string, o owner) error {
	expected, _ := json.Marshal(o)
	for _, name := range names {
		b, err := readExternal(name, 1024)
		if err != nil || !bytes.Equal(b, expected) {
			return errors.New("写锁所有者已变化")
		}
	}
	return nil
}
func release(names []string, o owner) error {
	for i := len(names) - 1; i >= 0; i-- {
		if err := checkLocks([]string{names[i]}, o); err != nil {
			return err
		}
		if err := os.Remove(names[i]); err != nil {
			return err
		}
	}
	return nil
}

func readOwner(ctx context.Context, r *repository) (owner, error) {
	b, err := readExternal(r.lockNames()[0], 1024)
	if err != nil {
		return owner{}, err
	}
	var o owner
	if err = decode(ctx, b, &o); err != nil {
		return o, err
	}
	if o.Version != "v1" || !digestPattern.MatchString(o.ID) || o.PID <= 0 {
		return o, errors.New("写锁所有者损坏")
	}
	return o, nil
}
