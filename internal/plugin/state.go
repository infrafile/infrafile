package plugin

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"maps"
	"os"
	"time"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/observation"
	"github.com/infrafile/infrafile/internal/snapshot"
	"go.yaml.in/yaml/v3"
)

type syncLog struct {
	APIVersion string                       `yaml:"apiVersion"`
	Kind       string                       `yaml:"kind"`
	Spec       map[string]map[string]string `yaml:"spec"`
}

type state struct {
	root     *os.Root
	log      syncLog
	status   snapshot.Status
	presence snapshot.Presence
}

func openState(ctx context.Context, dir string) (*state, error) {
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
	status, err := snapshot.ReadStatusFile(ctx, root)
	if err != nil {
		s.Close()
		return nil, failure("write", "无法读取 state/"+snapshot.StatusFile+"；保留原文件，请检查格式")
	}
	s.status = snapshot.Status{APIVersion: ledger.APIVersion, Kind: "SyncStatus", Spec: map[string]map[string]snapshot.Attempt{}}
	if status != nil {
		s.status = *status
	}
	presence, err := snapshot.ReadPresenceFile(ctx, root)
	if err != nil {
		s.Close()
		return nil, failure("write", "无法读取 state/"+snapshot.PresenceFile+"；保留原文件，请检查格式")
	}
	s.presence = snapshot.Presence{APIVersion: ledger.APIVersion, Kind: "Presence", Spec: map[string]snapshot.PresenceEntry{}}
	if presence != nil {
		s.presence = *presence
	}
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

func (s *state) nextPresence(watches []observation.Watch, name, instance string, spec map[string]any, now time.Time) (snapshot.Presence, error) {
	updated := s.presence
	updated.Spec = map[string]snapshot.PresenceEntry{}
	records := map[string]map[observation.Identity]bool{}
	for _, w := range watches {
		binding := Hash(w)
		old := s.presence.Spec[w.Target]
		if old.BindingHash == binding {
			updated.Spec[w.Target] = old
		}
		if w.Plugin != name || w.Instance != instance {
			continue
		}
		if _, ok := records[w.Service]; !ok {
			mapped, _, err := observation.WatchRecords(name, instance, w.Service, spec)
			if err != nil {
				return updated, failure("protocol", "检查范围内的观测字段无效，无法更新正向基线")
			}
			records[w.Service] = mapped
		}
		if w.Seen(records[w.Service]) {
			updated.Spec[w.Target] = snapshot.PresenceEntry{BindingHash: binding, LastSeen: now.UTC().Format(time.RFC3339)}
		}
	}
	return updated, nil
}

func (s *state) WritePresence(updated snapshot.Presence) error {
	if maps.Equal(s.presence.Spec, updated.Spec) {
		return nil
	}
	if info, err := s.root.Lstat(snapshot.PresenceFile); err == nil && !info.Mode().IsRegular() {
		return failure("write", "正向基线路径必须是普通文件")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return failure("write", "无法检查正向基线")
	}
	data, err := yaml.Marshal(updated)
	if err == nil {
		_, err = snapshot.ParsePresence(context.Background(), data)
	}
	if err != nil {
		return failure("write", "无法编码正向基线或基线超过上限")
	}
	if err := atomicWrite(s.root, snapshot.PresenceFile, data); err != nil {
		return failure("write", "无法保存正向基线")
	}
	s.presence = updated
	return nil
}

func (s *state) WriteStatus(plugin, instance string, attempt snapshot.Attempt) error {
	if info, err := s.root.Lstat(snapshot.StatusFile); err == nil && !info.Mode().IsRegular() {
		return failure("write", "执行回执路径必须是普通文件")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return failure("write", "无法检查执行回执")
	}
	updated := s.status
	updated.Spec = maps.Clone(s.status.Spec)
	updated.Spec[plugin] = maps.Clone(s.status.Spec[plugin])
	if updated.Spec[plugin] == nil {
		updated.Spec[plugin] = map[string]snapshot.Attempt{}
	}
	updated.Spec[plugin][instance] = attempt
	data, err := yaml.Marshal(updated)
	if err == nil {
		// 取消后仍需保存当前实例失败；解析受固定大小和展开上限约束。
		_, err = snapshot.ParseStatus(context.Background(), data)
	}
	if err != nil {
		return failure("write", "无法编码执行回执或回执超过上限")
	}
	if err := atomicWrite(s.root, snapshot.StatusFile, data); err != nil {
		return failure("write", "无法保存执行回执")
	}
	s.status = updated
	return nil
}

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
