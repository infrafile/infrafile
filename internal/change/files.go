package change

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func readExternal(name string, limit int64) ([]byte, error) {
	info, err := os.Lstat(name)
	if os.IsNotExist(err) {
		return nil, os.ErrNotExist
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("输入必须是有界普通文件，不能是符号链接")
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("文件在打开时变化")
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(b)) > limit {
		return nil, errors.New("文件读取失败或超限")
	}
	return b, nil
}

func read(root *os.Root, name string, limit int64) ([]byte, error) {
	parts := strings.Split(name, "/")
	for i := 1; i < len(parts); i++ {
		info, err := root.Lstat(strings.Join(parts[:i], "/"))
		if os.IsNotExist(err) {
			return nil, nil
		}
		if err != nil || !info.IsDir() {
			return nil, errors.New("目录缺失或不是普通目录")
		}
	}
	info, err := root.Lstat(name)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("文件不是有界普通文件")
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("文件在打开时变化")
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(b)) > limit {
		return nil, errors.New("读取失败或超限")
	}
	return b, nil
}

func capture(ctx context.Context, dir string) (map[string][]byte, string, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, "", err
	}
	defer root.Close()
	out := map[string][]byte{}
	remaining := int64(64 << 20)
	for _, base := range []string{"infrafile.yaml", "inventory"} {
		err = fs.WalkDir(root.FS(), base, func(name string, e fs.DirEntry, walkErr error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if walkErr != nil {
				return walkErr
			}
			if len(out) >= 1024 {
				return errors.New("台账超过 1024 个输入")
			}
			if strings.HasPrefix(e.Name(), ".") {
				if e.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if e.IsDir() {
				out[name+"/"] = nil
				return nil
			}
			if !e.Type().IsRegular() {
				return errors.New("台账不能包含符号链接或特殊文件")
			}
			b, err := read(root, name, remaining)
			if err != nil {
				return err
			}
			remaining -= int64(len(b))
			out[name] = b
			return nil
		})
		if err != nil {
			return nil, "", err
		}
	}
	values := map[string]string{}
	for name, b := range out {
		info, e := root.Lstat(strings.TrimSuffix(name, "/"))
		if e != nil {
			return nil, "", e
		}
		values[name] = hash(b) + ":" + info.Mode().String()
	}
	b, err := encoded(values)
	if err != nil {
		return nil, "", err
	}
	return out, hash(b), nil
}

func syncDir(root *os.Root) error {
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func atomicWrite(root *os.Root, name string, data []byte, mode os.FileMode, check func() error) error {
	parent := path.Dir(name)
	if err := root.MkdirAll(parent, 0700); err != nil {
		return err
	}
	info, err := root.Lstat(parent)
	if err != nil || !info.IsDir() {
		return errors.New("写入目录不是普通目录")
	}
	child, err := root.OpenRoot(parent)
	if err != nil {
		return err
	}
	defer child.Close()
	opened, err := child.Lstat(".")
	if err != nil || !os.SameFile(info, opened) {
		return errors.New("写入目录已变化")
	}
	tmp := ".edit-" + randomID()
	f, err := child.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer child.Remove(tmp)
	if err = f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	err = writeAll(f, data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if check != nil {
		if err = check(); err != nil {
			return err
		}
	}
	current, err := root.Lstat(parent)
	if err != nil || !os.SameFile(info, current) {
		return errors.New("写入目录在替换前变化")
	}
	if err = child.Rename(tmp, path.Base(name)); err != nil {
		return err
	}
	return syncDir(child)
}

func identical(a, b []byte) bool { return bytes.Equal(a, b) }
func privatePath(dir, id, suffix string) string {
	return filepath.Join(dir, ".infrafile", "transactions", id+suffix)
}
