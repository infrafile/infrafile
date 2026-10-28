package snapshot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
	"slices"
	"time"

	"github.com/infrafile/infrafile/internal/ledger"
)

// StatusFile 被旧版快照读取器跳过，不改变 _sync.yaml 的成功时间格式。
const StatusFile = ".sync-status.yaml"

type Attempt struct {
	AttemptedAt  string `json:"attempted_at" yaml:"attempted_at"`
	Status       string `json:"status" yaml:"status"`
	ConfigHash   string `json:"config_hash" yaml:"config_hash"`
	SnapshotHash string `json:"snapshot_hash,omitempty" yaml:"snapshot_hash,omitempty"`
	Code         string `json:"code,omitempty" yaml:"code,omitempty"`
}

type Status struct {
	APIVersion string                        `json:"apiVersion" yaml:"apiVersion"`
	Kind       string                        `json:"kind" yaml:"kind"`
	Spec       map[string]map[string]Attempt `json:"spec" yaml:"spec"`
}

var digest = regexp.MustCompile(`^[a-f0-9]{64}$`)

var syncCodes = []string{"not-found", "start", "timeout", "protocol", "capability", "plugin", "sensitive", "write", "config", "credential", "http", "provider", "probe"}

// ParseStatus 不回显输入；错误原文和任意插件字段不允许进入执行回执。
func ParseStatus(ctx context.Context, data []byte) (*Status, error) {
	broken := errors.New("无法读取 state/" + StatusFile + "，请检查格式")
	var status Status
	if err := decodeState(ctx, StatusFile, data, &status); err != nil {
		return nil, err
	}
	if status.APIVersion != ledger.APIVersion || status.Kind != "SyncStatus" || status.Spec == nil {
		return nil, broken
	}
	for plugin, instances := range status.Spec {
		if !pluginName.MatchString(plugin) || len(plugin) > 64 || instances == nil {
			return nil, broken
		}
		for instance, attempt := range instances {
			_, err := time.Parse(time.RFC3339, attempt.AttemptedAt)
			if err != nil || !pluginName.MatchString(instance) || len(instance) > 64 || !digest.MatchString(attempt.ConfigHash) {
				return nil, broken
			}
			switch attempt.Status {
			case "running":
				if attempt.Code != "" || attempt.SnapshotHash != "" {
					return nil, broken
				}
			case "succeeded":
				if attempt.Code != "" || !digest.MatchString(attempt.SnapshotHash) {
					return nil, broken
				}
			case "failed":
				if !slices.Contains(syncCodes, attempt.Code) || attempt.SnapshotHash != "" {
					return nil, broken
				}
			default:
				return nil, broken
			}
		}
	}
	return &status, nil
}

// ReadStatusFile 供持有同步锁的写入器复用；不存在返回 nil，不把未知状态当成功。
func ReadStatusFile(ctx context.Context, root *os.Root) (*Status, error) {
	data, err := readStateFile(root, StatusFile)
	if err != nil || data == nil {
		return nil, err
	}
	return ParseStatus(ctx, data)
}

func decodeState(ctx context.Context, file string, data []byte, value any) error {
	broken := errors.New("无法读取 state/" + file + "，请检查格式")
	if len(data) > 4<<20 {
		return broken
	}
	doc, problems, err := ledger.ParseDocument(ctx, "state/"+file, data)
	if err != nil {
		return err
	}
	if doc == nil || len(problems) != 0 {
		return broken
	}
	raw, err := json.Marshal(doc.Value)
	if err != nil {
		return broken
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(value) != nil {
		return broken
	}
	return nil
}

func readStateFile(root *os.Root, file string) ([]byte, error) {
	info, err := root.Lstat(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	broken := errors.New("无法读取 state/" + file + "，必须是可读的普通文件")
	if err != nil || !info.Mode().IsRegular() {
		return nil, broken
	}
	f, err := root.Open(file)
	if err != nil {
		return nil, broken
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (4<<20)+1))
	if err != nil {
		return nil, broken
	}
	return data, nil
}

func ReadStatus(ctx context.Context, dir string) (*Status, error) {
	root, err := openState(dir)
	if err != nil || root == nil {
		return nil, err
	}
	defer root.Close()
	return ReadStatusFile(ctx, root)
}
