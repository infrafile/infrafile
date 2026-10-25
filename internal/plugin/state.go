package plugin

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"maps"
	"os"
	"time"

	"github.com/infrafile/infrafile/internal/ledger"
	"go.yaml.in/yaml/v3"
)

type syncLog struct {
	APIVersion string                       `yaml:"apiVersion"`
	Kind       string                       `yaml:"kind"`
	Spec       map[string]map[string]string `yaml:"spec"`
}

type state struct {
	root *os.Root
	log  syncLog
}

func openState(dir string) (*state, error) {
	repo, err := os.OpenRoot(dir)
	if err != nil {
		return nil, failure("write", "无法打开数据仓")
	}
	defer repo.Close()
	if err := directory(repo, "state"); err != nil {
		return nil, err
	}
	root, err := repo.OpenRoot("state")
	if err != nil {
		return nil, failure("write", "无法打开 state 目录")
	}
	lock, err := root.OpenFile(".sync.lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		root.Close()
		return nil, failure("write", "无法锁定 state；确认没有其他 sync 运行后检查 state/.sync.lock")
	}
	lock.Close()
	s := &state{root: root, log: syncLog{ledger.APIVersion, "Sync", map[string]map[string]string{}}}
	info, err := root.Lstat("_sync.yaml")
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil || !info.Mode().IsRegular() {
		s.Close()
		return nil, failure("write", "state/_sync.yaml 必须是可读的普通文件")
	}
	data, err := root.ReadFile("_sync.yaml")
	if err == nil {
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		decoder.KnownFields(true)
		s.log = syncLog{}
		err = decoder.Decode(&s.log)
		if err == nil {
			var extra any
			if decoder.Decode(&extra) != io.EOF {
				err = errors.New("multiple documents")
			}
		}
	}
	if err == nil && s.log.APIVersion == ledger.APIVersion && s.log.Kind == "Sync" && s.log.Spec != nil {
		for _, instances := range s.log.Spec {
			for _, stamp := range instances {
				if _, parseErr := time.Parse(time.RFC3339, stamp); parseErr != nil {
					err = parseErr
				}
			}
		}
		if err == nil {
			return s, nil
		}
	}
	s.Close()
	return nil, failure("write", "无法读取 state/_sync.yaml；保留原文件，请检查格式")
}

func (s *state) Close() { s.root.Remove(".sync.lock"); s.root.Close() }

// directory 拒绝符号链接，不能让 state 指回 inventory 或仓外。
func directory(root *os.Root, name string) error {
	if err := root.Mkdir(name, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return failure("write", "无法创建 state 目录")
	}
	info, err := root.Lstat(name)
	if err != nil || !info.IsDir() {
		return failure("write", "state 路径必须是目录，不能是符号链接")
	}
	return nil
}

func atomicWrite(root *os.Root, file string, data []byte) error {
	tmp := ".sync-" + rand.Text()
	f, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer root.Remove(tmp)
	_, err = f.Write(data)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return root.Rename(tmp, file)
}

func (s *state) Write(plugin, instance string, data []byte, now time.Time) (bool, error) {
	if err := directory(s.root, plugin); err != nil {
		return false, err
	}
	root, err := s.root.OpenRoot(plugin)
	if err != nil {
		return false, failure("write", "无法打开插件快照目录")
	}
	defer root.Close()
	file := instance + ".yaml"
	if info, err := root.Lstat(file); err == nil && !info.Mode().IsRegular() {
		return false, failure("write", "快照路径必须是普通文件")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, failure("write", "无法检查旧快照")
	}
	old, err := root.ReadFile(file)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, failure("write", "无法读取旧快照")
	}
	changed := !bytes.Equal(old, data)
	if changed {
		if err := atomicWrite(root, file, data); err != nil {
			return false, failure("write", "无法写入快照")
		}
	}
	updated := s.log
	updated.Spec = maps.Clone(s.log.Spec)
	updated.Spec[plugin] = maps.Clone(s.log.Spec[plugin])
	if updated.Spec[plugin] == nil {
		updated.Spec[plugin] = map[string]string{}
	}
	updated.Spec[plugin][instance] = now.UTC().Format(time.RFC3339)
	logData, err := yaml.Marshal(updated)
	if err != nil {
		return changed, failure("write", "无法编码同步时间")
	}
	if err := atomicWrite(s.root, "_sync.yaml", logData); err != nil {
		return changed, failure("write", "快照已保存，但同步时间写入失败")
	}
	s.log = updated
	return changed, nil
}
