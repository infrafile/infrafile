// Package snapshot 只读地读取 state/ 下的插件快照和集中同步时间，供导入和审计共用。
package snapshot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/validate"
	"github.com/infrafile/infrafile/protocol"
	"go.yaml.in/yaml/v3"
)

var pluginName = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

// Snapshot 是一份通过信封和敏感字段检查的快照；File 为仓内相对路径。
// Skipped 的快照只检查了路径，未读取内容，Spec 为 nil。
type Snapshot struct {
	Plugin   string
	Instance string
	File     string
	Spec     map[string]any
	Skipped  bool
}

// openState 打开 state 目录；不存在时返回 nil。存在同步锁时拒绝读取。
func openState(dir string) (*os.Root, error) {
	repo, err := os.OpenRoot(dir)
	if err != nil {
		return nil, errors.New("无法打开数据仓")
	}
	defer repo.Close()
	info, err := repo.Lstat("state")
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil || !info.IsDir() {
		return nil, errors.New("state 必须是目录，不能是符号链接")
	}
	root, err := repo.OpenRoot("state")
	if err != nil {
		return nil, err
	}
	if _, err := root.Lstat(".sync.lock"); !errors.Is(err, os.ErrNotExist) {
		root.Close()
		return nil, errors.New("state 正在同步或锁仍存在，请在同步结束后重试")
	}
	return root, nil
}

// Read 按插件、实例的目录顺序读取快照，不判断插件当前是否启用。
// skip 为 nil 时读取全部；对 skip 返回 true 的实例只检查路径，不读取内容。
func Read(ctx context.Context, dir string, skip func(plugin, instance string) bool) ([]Snapshot, error) {
	root, err := openState(dir)
	if err != nil || root == nil {
		return []Snapshot{}, err
	}
	defer root.Close()
	plugins, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return nil, errors.New("无法列出 state")
	}
	snapshots := []Snapshot{}
	for _, p := range plugins {
		if strings.HasPrefix(p.Name(), ".") || p.Name() == "_sync.yaml" {
			continue
		}
		if !p.IsDir() || !pluginName.MatchString(p.Name()) || len(p.Name()) > 64 {
			return nil, errors.New("state 插件路径必须是合法名称的目录")
		}
		files, err := fs.ReadDir(root.FS(), p.Name())
		if err != nil {
			return nil, errors.New("无法列出插件快照")
		}
		for _, f := range files {
			if strings.HasPrefix(f.Name(), ".") {
				continue
			}
			if !strings.HasSuffix(f.Name(), ".yaml") {
				return nil, errors.New("快照必须使用 .yaml 扩展名")
			}
			rel := p.Name() + "/" + f.Name()
			info, err := root.Lstat(rel)
			if err != nil || !info.Mode().IsRegular() {
				return nil, errors.New("快照必须是普通文件，不能是符号链接")
			}
			source := "state/" + rel
			if name := strings.TrimSuffix(f.Name(), ".yaml"); skip != nil && skip(p.Name(), name) {
				snapshots = append(snapshots, Snapshot{Plugin: p.Name(), Instance: name, File: source, Skipped: true})
				continue
			}
			data, err := root.ReadFile(rel)
			if err != nil {
				return nil, errors.New("无法读取快照")
			}
			doc, problems, err := ledger.ParseDocument(ctx, source, data)
			if err != nil {
				return nil, err
			}
			if doc == nil || len(problems) != 0 {
				return nil, fmt.Errorf("%s: 快照 YAML 格式错误", source)
			}
			if !validate.SnapshotSafe(doc.Value) {
				return nil, errors.New("快照含敏感字段或疑似凭据，已拒绝读取")
			}
			raw, err := json.Marshal(doc.Value)
			if err != nil || protocol.Validate("snapshot", raw) != nil {
				return nil, fmt.Errorf("%s: 快照信封不符合协议", source)
			}
			value := doc.Value.(map[string]any)
			instance := value["metadata"].(map[string]any)["name"].(string)
			if f.Name() != instance+".yaml" {
				return nil, errors.New("快照实例名与文件名不符")
			}
			snapshots = append(snapshots, Snapshot{Plugin: p.Name(), Instance: instance, File: source, Spec: value["spec"].(map[string]any)})
		}
	}
	return snapshots, nil
}

// ReadSync 读取 state/_sync.yaml 中各实例最近一次成功同步的时间；文件不存在时返回空表。
// 格式与 sync 写入的一致，任何偏差都按损坏处理。
func ReadSync(dir string) (map[string]map[string]time.Time, error) {
	stamps := map[string]map[string]time.Time{}
	root, err := openState(dir)
	if err != nil || root == nil {
		return stamps, err
	}
	defer root.Close()
	info, err := root.Lstat("_sync.yaml")
	if errors.Is(err, os.ErrNotExist) {
		return stamps, nil
	}
	broken := errors.New("无法读取 state/_sync.yaml，请检查格式")
	if err != nil || !info.Mode().IsRegular() {
		return nil, broken
	}
	data, err := root.ReadFile("_sync.yaml")
	if err != nil {
		return nil, broken
	}
	var log struct {
		APIVersion string                       `yaml:"apiVersion"`
		Kind       string                       `yaml:"kind"`
		Spec       map[string]map[string]string `yaml:"spec"`
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if decoder.Decode(&log) != nil {
		return nil, broken
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || log.APIVersion != ledger.APIVersion || log.Kind != "Sync" || log.Spec == nil {
		return nil, broken
	}
	for plugin, instances := range log.Spec {
		stamps[plugin] = map[string]time.Time{}
		for instance, stamp := range instances {
			at, err := time.Parse(time.RFC3339, stamp)
			if err != nil {
				return nil, broken
			}
			stamps[plugin][instance] = at
		}
	}
	return stamps, nil
}
