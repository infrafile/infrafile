package audit

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/snapshot"
	"github.com/infrafile/infrafile/internal/validate"
)

const (
	maxHistoryInputFiles = 1024
	maxHistoryInputBytes = 64 << 20
)

func historyBytes(root *os.Root, name string, limit int64) ([]byte, error) {
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("历史或输入必须是有界普通文件，不能是符号链接")
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, errors.New("无法打开历史或输入文件")
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("历史或输入在打开时发生变化")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("读取历史或输入失败，或文件超过上限")
	}
	return data, nil
}

func readHistoryRoot(ctx context.Context, root *os.Root) (*FindingHistory, []byte, error) {
	data, err := historyBytes(root, HistoryFile, maxHistoryBytes)
	if err != nil || data == nil {
		return nil, data, err
	}
	h, err := decodeHistory(ctx, data)
	return h, data, err
}

// ReadHistory never creates a directory, lock or history. Missing history is nil.
func ReadHistory(ctx context.Context, dir string) (*FindingHistory, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := snapshotState(dir)
	if err != nil || root == nil {
		return nil, err
	}
	defer root.Close()
	h, _, err := readHistoryRoot(ctx, root)
	return h, err
}

// ExportHistory returns the exact validated bytes required by PruneHistory.
func ExportHistory(ctx context.Context, dir string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := snapshotState(dir)
	if err != nil {
		return nil, err
	}
	if root == nil {
		return nil, errors.New("尚无问题历史，请先显式 refresh")
	}
	defer root.Close()
	h, data, err := readHistoryRoot(ctx, root)
	if err != nil {
		return nil, err
	}
	if h == nil {
		return nil, errors.New("尚无问题历史，请先显式 refresh")
	}
	return data, nil
}

func snapshotState(dir string) (*os.Root, error) {
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
		return nil, errors.New("state 必须是普通目录，不能是符号链接")
	}
	root, err := repo.OpenRoot("state")
	if err != nil {
		return nil, errors.New("无法打开 state")
	}
	opened, statErr := root.Lstat(".")
	if statErr != nil || !os.SameFile(info, opened) {
		root.Close()
		return nil, errors.New("state 在打开时发生变化")
	}
	if _, err := root.Lstat(".sync.lock"); !errors.Is(err, os.ErrNotExist) {
		root.Close()
		return nil, errors.New("state 正在同步/刷新或锁仍存在；先确认原进程再检查 state/.sync.lock")
	}
	return root, nil
}

type historyStore struct {
	repo, state *os.Root
	dir         string
	lockInfo    os.FileInfo
}

func openHistoryStore(ctx context.Context, dir string) (*historyStore, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	repo, err := os.OpenRoot(dir)
	if err != nil {
		return nil, errors.New("无法打开数据仓")
	}
	data, err := historyBytes(repo, ledger.ConfigFile, maxHistoryBytes)
	if err != nil {
		repo.Close()
		return nil, err
	}
	doc, ps, err := ledger.ParseDocument(ctx, ledger.ConfigFile, data)
	if err != nil || doc == nil || len(ps) != 0 || field(doc.Value, "storageVersion") != "v1" {
		repo.Close()
		return nil, errors.New("写问题历史前必须显式启用 storageVersion: v1；不会自动迁移配置")
	}
	if err := repo.Mkdir("state", 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		repo.Close()
		return nil, errors.New("无法创建 state")
	}
	info, err := repo.Lstat("state")
	if err != nil || !info.IsDir() {
		repo.Close()
		return nil, errors.New("state 必须是普通目录，不能是符号链接")
	}
	state, err := repo.OpenRoot("state")
	if err != nil {
		repo.Close()
		return nil, err
	}
	opened, statErr := state.Lstat(".")
	if statErr != nil || !os.SameFile(info, opened) {
		state.Close()
		repo.Close()
		return nil, errors.New("state 在打开时发生变化")
	}
	lock, err := state.OpenFile(".sync.lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		state.Close()
		repo.Close()
		return nil, errors.New("无法锁定 state；确认没有 sync/findings 写入进程后检查 state/.sync.lock")
	}
	lockInfo, statErr := lock.Stat()
	if err := lock.Close(); err != nil || statErr != nil {
		state.Remove(".sync.lock")
		state.Close()
		repo.Close()
		return nil, errors.New("无法完整创建 state 锁")
	}
	return &historyStore{repo: repo, state: state, dir: dir, lockInfo: lockInfo}, nil
}

func (s *historyStore) close() {
	if info, err := s.state.Lstat(".sync.lock"); err == nil && os.SameFile(info, s.lockInfo) {
		s.state.Remove(".sync.lock")
	}
	s.state.Close()
	s.repo.Close()
}

func (s *historyStore) check(old []byte) error {
	if info, err := s.state.Lstat(".sync.lock"); err != nil || !os.SameFile(info, s.lockInfo) {
		return errors.New("state 写锁已丢失或被替换，保留原历史")
	}
	current, err := os.OpenRoot(s.dir)
	if err != nil {
		return errors.New("数据仓路径已变化，保留原历史")
	}
	defer current.Close()
	for _, p := range []string{".", "state"} {
		before, e1 := s.repo.Lstat(p)
		after, e2 := current.Lstat(p)
		if e1 != nil || e2 != nil || !os.SameFile(before, after) {
			return errors.New("数据仓或 state 已被替换，保留原历史")
		}
	}
	opened, err := s.state.Lstat(".")
	currentState, currentErr := s.repo.Lstat("state")
	if err != nil || currentErr != nil || !os.SameFile(opened, currentState) {
		return errors.New("state 已被替换，保留原历史")
	}
	currentBytes, err := historyBytes(s.state, HistoryFile, maxHistoryBytes)
	if err != nil || !bytes.Equal(currentBytes, old) {
		return errors.New("问题历史被外部修改，拒绝覆盖")
	}
	return nil
}

// captureHistoryInputs copies only the offline evaluator's inputs, never secret files.
func captureHistoryInputs(ctx context.Context, source, destination *os.Root) ([]HistoryInput, error) {
	inputs := []HistoryInput{}
	remaining := int64(maxHistoryInputBytes)
	for _, base := range []string{ledger.ConfigFile, "inventory", "state"} {
		if _, err := source.Lstat(base); errors.Is(err, os.ErrNotExist) && base != ledger.ConfigFile {
			continue
		}
		err := fs.WalkDir(source.FS(), base, func(name string, entry fs.DirEntry, walkErr error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if walkErr != nil {
				return errors.New("无法枚举固定输入")
			}
			if strings.HasPrefix(entry.Name(), ".") && name != "state/"+snapshot.StatusFile && name != "state/"+snapshot.PresenceFile {
				if entry.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if len(inputs) >= maxHistoryInputFiles {
				return errors.New("固定输入超过 1024 个文件/目录上限")
			}
			if entry.IsDir() {
				if destination != nil {
					if err := destination.MkdirAll(name, 0o700); err != nil {
						return err
					}
				}
				inputs = append(inputs, HistoryInput{name + "/", fmt.Sprintf("%x", sha256.Sum256(nil))})
				return nil
			}
			if !entry.Type().IsRegular() {
				return errors.New("固定输入不能是符号链接或特殊文件")
			}
			data, err := historyBytes(source, name, remaining)
			if err != nil || data == nil {
				return errors.New("固定输入读取失败、发生变化或超过 64 MiB")
			}
			remaining -= int64(len(data))
			if destination != nil {
				if err := destination.WriteFile(name, data, 0o600); err != nil {
					return err
				}
			}
			inputs = append(inputs, HistoryInput{name, fmt.Sprintf("%x", sha256.Sum256(data))})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].Path < inputs[j].Path })
	return inputs, nil
}

// publishHistory checks preconditions after syncing the staged bytes, immediately before rename.
func publishHistory(ctx context.Context, root *os.Root, data []byte, check func() error) error {
	name := ".findings-" + rand.Text()
	f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return errors.New("无法创建历史临时文件；保留原历史")
	}
	defer root.Remove(name)
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return errors.New("无法完整保存历史临时文件；保留原历史")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := check(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := root.Rename(name, HistoryFile); err != nil {
		return errors.New("历史原子替换失败；保留原历史")
	}
	dir, err := root.Open(".")
	if err == nil {
		err = dir.Sync()
		dir.Close()
	}
	if err != nil {
		return errors.New("历史已替换，但目录持久化失败；结果未确认，请核对现存历史，勿盲目重试")
	}
	return nil
}

// RefreshHistory locks against collectors and refreshers, evaluates a private fixed copy,
// then verifies both the original inputs and history before publishing one complete file.
func RefreshHistory(ctx context.Context, dir string, opts InspectOptions) (*HistoryRefresh, error) {
	return refreshHistory(ctx, dir, opts, nil)
}

func refreshHistory(ctx context.Context, dir string, opts InspectOptions, beforePublish func() error) (*HistoryRefresh, error) {
	if opts.MaxAgeDays == nil || *opts.MaxAgeDays < 0 || opts.Keys != nil || opts.Now.IsZero() {
		return nil, errors.New("刷新历史需要非负 max-age、有效时间和完整台账范围")
	}
	opts.Now = opts.Now.UTC().Truncate(time.Second)
	s, err := openHistoryStore(ctx, dir)
	if err != nil {
		return nil, err
	}
	defer s.close()
	old, oldBytes, err := readHistoryRoot(ctx, s.state)
	if err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp("", "infrafile-findings-")
	if err != nil {
		return nil, errors.New("无法创建固定输入副本")
	}
	defer os.RemoveAll(tmp)
	stage, err := os.OpenRoot(tmp)
	if err != nil {
		return nil, err
	}
	defer stage.Close()
	inputs, err := captureHistoryInputs(ctx, s.repo, stage)
	if err != nil {
		return nil, err
	}
	validated, err := validate.Run(ctx, tmp, validate.Options{})
	if err != nil {
		return nil, err
	}
	if len(validated.Problems) != 0 || field(validated.Repo.Config.Value, "storageVersion") != "v1" {
		return nil, errors.New("固定输入校验失败；先校验台账及 v1 标记，保留原历史")
	}
	known := []IssueIdentity{}
	if old != nil {
		for _, state := range old.Data.Issues {
			known = append(known, state.Identity)
		}
	}
	assessment, err := EvaluateIssues(ctx, validated.Repo, opts, known)
	if err != nil {
		return nil, err
	}
	result, err := nextHistory(ctx, old, inputs, opts, assessment)
	if err != nil {
		return nil, err
	}
	data, err := result.History.Encode(ctx)
	if err != nil {
		return nil, err
	}
	check := func() error {
		if beforePublish != nil {
			if err := beforePublish(); err != nil {
				return err
			}
		}
		current, err := captureHistoryInputs(ctx, s.repo, nil)
		if err != nil || !reflect.DeepEqual(inputs, current) {
			return errors.New("固定输入在评估期间变化，拒绝覆盖问题历史")
		}
		return s.check(oldBytes)
	}
	if !result.Written {
		if err := check(); err != nil {
			return nil, err
		}
		return result, nil
	}
	if err := publishHistory(ctx, s.state, data, check); err != nil {
		return nil, err
	}
	return result, nil
}

// PruneHistory requires a byte-for-byte independent export and only removes old
// recovered states and an old event prefix. Active/out-of-scope issues are retained.
func PruneHistory(ctx context.Context, dir, exported string, before, now time.Time) (*HistoryRefresh, error) {
	s, err := openHistoryStore(ctx, dir)
	if err != nil {
		return nil, err
	}
	defer s.close()
	old, oldBytes, err := readHistoryRoot(ctx, s.state)
	if err != nil {
		return nil, err
	}
	if old == nil {
		return nil, errors.New("尚无问题历史，无需清理")
	}
	exportRoot, err := os.OpenRoot(path.Dir(exported))
	if err != nil {
		return nil, errors.New("无法打开完整导出")
	}
	defer exportRoot.Close()
	exportInfo, e1 := exportRoot.Lstat(path.Base(exported))
	historyInfo, e2 := s.state.Lstat(HistoryFile)
	if e1 != nil || e2 != nil || os.SameFile(exportInfo, historyInfo) {
		return nil, errors.New("清理前需要独立的完整导出副本")
	}
	exportBytes, err := historyBytes(exportRoot, path.Base(exported), maxHistoryBytes)
	if err != nil || !bytes.Equal(exportBytes, oldBytes) {
		return nil, errors.New("导出与当前历史不一致，保留原历史；请先重新完整导出")
	}
	h, err := pruneHistory(ctx, old, before, now)
	if err != nil {
		return nil, err
	}
	result := &HistoryRefresh{Version: "v1", History: h, Changes: []HistoryEvent{}, Written: !reflect.DeepEqual(old, h)}
	if !result.Written {
		return result, s.check(oldBytes)
	}
	data, err := h.Encode(ctx)
	if err != nil {
		return nil, err
	}
	if err := publishHistory(ctx, s.state, data, func() error {
		currentExport, err := historyBytes(exportRoot, path.Base(exported), maxHistoryBytes)
		if err != nil || !bytes.Equal(currentExport, oldBytes) {
			return errors.New("导出在清理期间变化，保留原历史")
		}
		return s.check(oldBytes)
	}); err != nil {
		return nil, err
	}
	return result, nil
}
