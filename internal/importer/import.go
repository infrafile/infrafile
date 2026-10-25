// Package importer 把离线观测转成可审查的记录，只创建不存在的目标。
package importer

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/validate"
	"go.yaml.in/yaml/v3"
)

type Entry struct {
	Source string         `json:"source"`
	Kind   string         `json:"kind"`
	Name   string         `json:"name"`
	File   string         `json:"file"`
	Status string         `json:"status"`
	Record map[string]any `json:"record,omitempty"`
}

type Notice struct {
	Source string `json:"source"`
	Reason string `json:"reason"`
}

type Result struct {
	Records []Entry  `json:"records"`
	Skipped []Notice `json:"skipped"`
}

func draft(source, kind, name string, spec map[string]any) Entry {
	k, _ := ledger.KindByName(kind)
	return Entry{Source: source, Kind: kind, Name: name, File: k.Dir + "/" + name + ".yaml", Status: "draft", Record: map[string]any{"apiVersion": ledger.APIVersion, "kind": kind, "metadata": map[string]any{"name": name}, "spec": spec}}
}

// Prepare 在任何输出或写入前校验全部候选；已存在主键（含归档）始终优先。
func Prepare(repo *ledger.Repo, entries []Entry, notices []Notice) (*Result, error) {
	schemas, err := ledger.LoadSchemas()
	if err != nil {
		return nil, err
	}
	existing := map[string]bool{}
	for _, record := range repo.Records {
		existing[record.Kind+"/"+record.Name] = true
	}
	result := &Result{Records: []Entry{}, Skipped: append([]Notice{}, notices...)}
	seen := map[string][]byte{}
	for _, entry := range entries {
		if !validate.SnapshotSafe(map[string]any{"source": entry.Source, "record": entry.Record}) {
			return nil, errors.New("导入候选含敏感字段或疑似凭据，已拒绝输出")
		}
		if schemas.Validate(entry.Kind, entry.Record) != nil {
			return nil, errors.New("导入候选不符合记录 Schema，请检查源数据的名称和字段")
		}
		if len(entry.Name+".yaml") > 255 {
			return nil, errors.New("导入记录名称加 .yaml 后不能超过 255 字节，请缩短源别名或人工处理长域名")
		}
		key := entry.Kind + "/" + entry.Name
		if existing[key] {
			entry.Status, entry.Record = "exists", nil
		} else {
			encoded, _ := json.Marshal(entry.Record)
			if previous, ok := seen[key]; ok {
				if !bytes.Equal(previous, encoded) {
					return nil, fmt.Errorf("多个来源对 %s 给出不同草稿；请先人工确认并建立该记录", key)
				}
				result.Skipped = append(result.Skipped, Notice{entry.Source, "duplicate"})
				continue
			}
			seen[key] = encoded
		}
		result.Records = append(result.Records, entry)
	}
	sort.Slice(result.Records, func(i, j int) bool {
		a, b := result.Records[i], result.Records[j]
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Source < b.Source
	})
	return result, nil
}

func (r *Result) YAML() ([]byte, error) {
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	for _, entry := range r.Records {
		if entry.Status == "draft" {
			if err := enc.Encode(entry.Record); err != nil {
				return nil, err
			}
		}
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// Publish 用硬链接发布完整文件，已存在的名字永远不会被替换。
func (r *Result) Publish(dir string) (err error) {
	defer func() {
		if err != nil {
			for _, entry := range r.Records {
				if entry.Status == "created" {
					err = fmt.Errorf("%w；此前已创建的记录保留，重试不会覆盖已有文件", err)
					break
				}
			}
		}
	}()
	needed := false
	for _, entry := range r.Records {
		needed = needed || entry.Status == "draft"
	}
	if !needed {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return errors.New("无法创建导入目标目录")
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return errors.New("导入目标必须是目录，不能是符号链接")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return errors.New("无法打开导入目标目录")
	}
	defer root.Close()
	for i := range r.Records {
		entry := &r.Records[i]
		if entry.Status != "draft" {
			continue
		}
		kind, _ := ledger.KindByName(entry.Kind)
		if err := root.Mkdir(kind.Dir, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
			return errors.New("无法创建记录目录")
		}
		info, err := root.Lstat(kind.Dir)
		if err != nil || !info.IsDir() {
			return errors.New("记录目录不能是符号链接或普通文件")
		}
		child, err := root.OpenRoot(kind.Dir)
		if err != nil {
			return errors.New("无法打开记录目录")
		}
		created, err := createRecord(child, entry.Name+".yaml", entry.Record)
		child.Close()
		if err != nil {
			return errors.New("无法发布记录文件，请检查权限及文件系统硬链接支持")
		}
		entry.Status = "created"
		if !created {
			entry.Status, entry.Record = "exists", nil
		}
	}
	return nil
}

func createRecord(root *os.Root, name string, record map[string]any) (bool, error) {
	if _, err := root.Lstat(name); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	data, err := yaml.Marshal(record)
	if err != nil {
		return false, err
	}
	tmp := ".import-" + rand.Text()
	f, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return false, err
	}
	defer root.Remove(tmp)
	_, err = f.Write(data)
	closeErr := f.Close()
	if err != nil {
		return false, err
	}
	if closeErr != nil {
		return false, closeErr
	}
	if err := root.Link(tmp, name); errors.Is(err, os.ErrExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return true, nil
}

// ReviewPath 不允许借待审目录选项绕过 --write（包括符号链接别名）。
func ReviewPath(repoDir, out string) (string, error) {
	canonical, err := resolvePath(out)
	if err != nil {
		return "", err
	}
	inventory, err := resolvePath(filepath.Join(repoDir, ledger.InventoryDir))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(inventory, canonical)
	if err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))) {
		return "", errors.New("待审目录不能位于 inventory/ 内；写入台账必须使用 --write")
	}
	rootInfo, err := os.Stat(repoDir)
	if err != nil {
		return "", errors.New("无法检查数据仓目录身份")
	}
	inventoryInfo, inventoryErr := os.Stat(inventory)
	for current := canonical; ; current = filepath.Dir(current) {
		info, statErr := os.Stat(current)
		if inventoryErr == nil && statErr == nil && os.SameFile(inventoryInfo, info) {
			return "", errors.New("待审目录不能通过路径别名指向 inventory/")
		}
		// inventory 尚不存在时也保留大小写变体，避免在大小写不敏感卷上创建同一目录。
		if strings.EqualFold(filepath.Base(current), ledger.InventoryDir) {
			parent, parentErr := os.Stat(filepath.Dir(current))
			if parentErr == nil && os.SameFile(rootInfo, parent) {
				return "", errors.New("待审目录不能使用 inventory/ 的大小写变体")
			}
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	return canonical, nil
}

func resolvePath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err == nil {
		return resolved, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("无法检查目标路径")
	}
	parent := filepath.Dir(absolute)
	if parent == absolute {
		return "", errors.New("无法定位目标目录")
	}
	resolved, err = resolvePath(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, filepath.Base(absolute)), nil
}
