package control

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/plugin"
	"github.com/infrafile/infrafile/protocol"
)

const maxReceipts = 256

type Receipt struct {
	Version string      `json:"version"`
	Kind    string      `json:"kind"`
	Data    ReceiptData `json:"data"`
}

type ReceiptData struct {
	ID               string `json:"id"`
	Plan             *Plan  `json:"plan"`
	Actor            string `json:"actor"`
	AuthorizationRef string `json:"authorization_ref"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
	Phase            string `json:"phase"`
	WriteStartedAt   string `json:"write_started_at,omitempty"`
	BeforeHash       string `json:"before_hash,omitempty"`
	ReadbackHash     string `json:"readback_hash,omitempty"`
	Result           Result `json:"result"`
}

type ExecutionSummary struct {
	ID               string `json:"id"`
	Target           string `json:"target"`
	Operation        string `json:"operation"`
	Actor            string `json:"actor"`
	AuthorizationRef string `json:"authorization_ref"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
	ExpiresAt        string `json:"expires_at"`
	Phase            string `json:"phase"`
	Status           string `json:"status"`
}

type Executions struct {
	Version    string             `json:"version"`
	Present    bool               `json:"present"`
	Executions []ExecutionSummary `json:"executions"`
}

func attributionText(value string, limit int) bool {
	if strings.TrimSpace(value) == "" || len(value) > limit {
		return false
	}
	for _, c := range value {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}

func decodeReceipt(ctx context.Context, data []byte) (*Receipt, error) {
	if len(data) > maxPlanBytes || !json.Valid(data) {
		return nil, errors.New("执行回执无效或超过 4 MiB")
	}
	_, ps, err := ledger.ParseDocument(ctx, "execution-receipt", data)
	if err != nil {
		return nil, err
	}
	if len(ps) > 0 || protocol.Validate("record", data) != nil {
		return nil, errors.New("执行回执重复键、信封或解析限制无效")
	}
	var r Receipt
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	d.DisallowUnknownFields()
	if d.Decode(&r) != nil {
		return nil, errors.New("执行回执载荷无效")
	}
	canonical, err := json.Marshal(r)
	if err != nil || !sameJSON(data, canonical) {
		return nil, errors.New("执行回执含非规范字段")
	}
	if _, err = plugin.Object(canonical); err != nil {
		return nil, err
	}
	v := r.Data
	if r.Version != "v1" || r.Kind != "ExecutionReceipt" || v.Plan == nil || v.Plan.Version != "v2" || v.Plan.Validate() != nil || v.ID != v.Plan.Digest || !attributionText(v.Actor, 128) || !attributionText(v.AuthorizationRef, 256) {
		return nil, errors.New("执行回执身份、计划或授权引用无效")
	}
	created, e1 := time.Parse(time.RFC3339Nano, v.CreatedAt)
	updated, e2 := time.Parse(time.RFC3339Nano, v.UpdatedAt)
	generated, _ := time.Parse(time.RFC3339, v.Plan.GeneratedAt)
	expires, _ := time.Parse(time.RFC3339, v.Plan.ExpiresAt)
	if e1 != nil || e2 != nil || updated.Before(created) || created.Before(generated) || !created.Before(expires) {
		return nil, errors.New("执行回执时间无效")
	}
	if v.WriteStartedAt != "" {
		started, err := time.Parse(time.RFC3339Nano, v.WriteStartedAt)
		if err != nil || started.Before(created) || started.After(updated) || !started.Before(expires) || v.BeforeHash != v.Plan.Actions[0].BeforeHash {
			return nil, errors.New("执行回执写前时间无效")
		}
	}
	for _, h := range []string{v.BeforeHash, v.ReadbackHash} {
		if h != "" && !digest.MatchString(h) {
			return nil, errors.New("执行回执观测哈希无效")
		}
	}
	result := v.Result
	if result.Version != "v1" || result.Receipt != v.ID || result.Target != v.Plan.Actions[0].Target || result.Replayed || result.Verification != "" || result.ObservedHash != "" {
		return nil, errors.New("执行回执结果身份无效")
	}
	if v.Phase != "prepared" && v.Phase != "write_started" && v.Phase != "completed" {
		return nil, errors.New("执行回执阶段无效")
	}
	if (v.Phase == "write_started" && v.WriteStartedAt == "") || (v.Phase != "completed" && (result.Status != "uncertain" || result.OK)) {
		return nil, errors.New("未完成执行不能标为成功")
	}
	switch result.Status {
	case "applied", "unchanged":
		if !result.OK {
			return nil, errors.New("成功回执状态不一致")
		}
	case "stale", "mismatch", "uncertain", "rejected":
		if result.OK {
			return nil, errors.New("失败回执不能标为成功")
		}
	default:
		return nil, errors.New("执行回执状态未知")
	}
	if result.Status == "applied" && (v.WriteStartedAt == "" || v.ReadbackHash != plugin.Hash(v.Plan.Actions[0].After)) {
		return nil, errors.New("成功回执缺少写前或匹配回读")
	}
	if result.Status == "unchanged" && (v.WriteStartedAt != "" || v.BeforeHash != v.Plan.Actions[0].BeforeHash || v.ReadbackHash != plugin.Hash(v.Plan.Actions[0].After)) {
		return nil, errors.New("无需修改回执缺少匹配观测或含写入标记")
	}
	if result.Status == "mismatch" && (v.WriteStartedAt == "" || v.ReadbackHash == "" || v.ReadbackHash == plugin.Hash(v.Plan.Actions[0].After)) {
		return nil, errors.New("不符回执缺少写入及不同回读")
	}
	if v.Phase == "prepared" && (v.WriteStartedAt != "" || v.BeforeHash != "" || v.ReadbackHash != "") {
		return nil, errors.New("执行准备阶段包含后续证据")
	}
	if v.Phase == "write_started" && (v.BeforeHash != v.Plan.Actions[0].BeforeHash || v.ReadbackHash != "") {
		return nil, errors.New("写前记录缺少前提哈希或包含后置证据")
	}
	return &r, nil
}

func receiptBytes(root *os.Root, name string) ([]byte, error) {
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxPlanBytes {
		return nil, errors.New("执行回执须为有界普通文件，不能是符号链接")
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("执行回执打开时变化")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxPlanBytes+1))
	if err != nil || len(data) > maxPlanBytes {
		return nil, errors.New("执行回执读取失败或超限")
	}
	return data, nil
}

// receiptRoot never follows history directory symlinks, and readers never create files.
func receiptRoot(dir string, create bool) (*os.Root, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	for _, name := range []string{"history", "executions"} {
		if create {
			if err := root.Mkdir(name, 0700); err != nil && !errors.Is(err, os.ErrExist) {
				root.Close()
				return nil, err
			} else if err == nil {
				if err := syncReceiptDir(root); err != nil {
					root.Close()
					return nil, err
				}
			}
		}
		info, err := root.Lstat(name)
		if errors.Is(err, os.ErrNotExist) && !create {
			root.Close()
			return nil, nil
		}
		if err != nil || !info.IsDir() || (create && info.Mode().Perm()&0077 != 0) {
			root.Close()
			return nil, errors.New("执行历史目录须为普通目录；写入要求 0700")
		}
		next, err := root.OpenRoot(name)
		if err == nil {
			opened, e := next.Lstat(".")
			if e != nil || !os.SameFile(info, opened) {
				next.Close()
				err = errors.New("执行历史目录打开时变化")
			}
		}
		root.Close()
		if err != nil {
			return nil, err
		}
		root = next
	}
	return root, nil
}

func ReadReceipt(ctx context.Context, dir, id string) (*Receipt, error) {
	if !digest.MatchString(id) {
		return nil, errors.New("回执 ID 须为 64 位小写十六进制")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := receiptRoot(dir, false)
	if err != nil || root == nil {
		return nil, err
	}
	defer root.Close()
	data, err := receiptBytes(root, id+".json")
	if err != nil || data == nil {
		return nil, err
	}
	r, err := decodeReceipt(ctx, data)
	if err == nil && r.Data.ID != id {
		return nil, errors.New("回执 ID 与文件名不符")
	}
	return r, err
}

func listReceipts(ctx context.Context, root *os.Root, key string) (*Executions, error) {
	view := &Executions{Version: "v1", Present: root != nil, Executions: []ExecutionSummary{}}
	if root == nil {
		return view, nil
	}
	dir, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(maxReceipts + 128)
	if err != nil && err != io.EOF {
		return nil, err
	}
	// Include staging/lock entries in the directory budget; never return a silent prefix.
	if len(entries) == maxReceipts+128 {
		return nil, errors.New("执行历史目录超过条目上限")
	}
	total, count := 0, 0
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		if !digest.MatchString(id) || name != id+".json" {
			return nil, errors.New("执行历史包含未知条目")
		}
		count++
		if count > maxReceipts {
			return nil, errors.New("执行回执超过 256 条；请先独立归档，不自动裁剪")
		}
		data, err := receiptBytes(root, name)
		if err != nil || data == nil {
			return nil, errors.New("执行历史在读取时变化或损坏")
		}
		total += len(data)
		if total > 64<<20 {
			return nil, errors.New("执行历史读取超过 64 MiB")
		}
		r, err := decodeReceipt(ctx, data)
		if err != nil {
			return nil, err
		}
		v := r.Data
		if v.ID != id {
			return nil, errors.New("执行回执文件名与载荷不符")
		}
		a := v.Plan.Actions[0]
		if key != "" && key != a.Target {
			continue
		}
		view.Executions = append(view.Executions, ExecutionSummary{id, a.Target, a.Operation, v.Actor, v.AuthorizationRef, v.CreatedAt, v.UpdatedAt, v.Plan.ExpiresAt, v.Phase, v.Result.Status})
	}
	sort.Slice(view.Executions, func(i, j int) bool {
		a, b := view.Executions[i], view.Executions[j]
		if a.CreatedAt == b.CreatedAt {
			return a.ID < b.ID
		}
		return a.CreatedAt > b.CreatedAt
	})
	return view, nil
}

func ListExecutions(ctx context.Context, dir, key string) (*Executions, error) {
	if key != "" && !resourceKey.MatchString(key) {
		return nil, errors.New("执行范围须为 Resource 主键")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := receiptRoot(dir, false)
	if err != nil {
		return nil, err
	}
	if root != nil {
		defer root.Close()
	}
	return listReceipts(ctx, root, key)
}

type receiptStore struct {
	root *os.Root
	lock *os.File
	dir  string
}

func openReceiptStore(ctx context.Context, dir string) (*receiptStore, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := receiptRoot(dir, true)
	if err != nil {
		return nil, err
	}
	lock, err := root.OpenFile(".execution.lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		root.Close()
		return nil, err
	}
	info, err := root.Lstat(".execution.lock")
	opened, e := lock.Stat()
	if err != nil || e != nil || !info.Mode().IsRegular() || !os.SameFile(info, opened) || info.Mode().Perm()&0077 != 0 {
		lock.Close()
		root.Close()
		return nil, errors.New("执行锁不是私有普通文件")
	}
	if err := lockExecution(lock); err != nil {
		lock.Close()
		root.Close()
		return nil, errors.New("执行正在进行或当前平台不支持持久回执写入")
	}
	s := &receiptStore{root, lock, dir}
	if err := s.check(); err != nil {
		s.close()
		return nil, err
	}
	return s, nil
}
func (s *receiptStore) close() { s.lock.Close(); s.root.Close() }
func (s *receiptStore) check() error {
	current, err := receiptRoot(s.dir, false)
	if err != nil {
		return err
	}
	if current == nil {
		return errors.New("执行历史目录已移走")
	}
	defer current.Close()
	a, e1 := current.Lstat(".")
	b, e2 := s.root.Lstat(".")
	l, e3 := s.root.Lstat(".execution.lock")
	held, e4 := s.lock.Stat()
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || !os.SameFile(a, b) || !os.SameFile(l, held) {
		return errors.New("执行历史或写锁被替换")
	}
	return nil
}
func (s *receiptStore) save(ctx context.Context, r *Receipt, old []byte) ([]byte, error) {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	if _, err := decodeReceipt(ctx, data); err != nil {
		return nil, err
	}
	directory, err := s.root.Open(".")
	if err != nil {
		return nil, err
	}
	entries, err := directory.ReadDir(maxReceipts + 128)
	directory.Close()
	if err != nil && err != io.EOF {
		return nil, err
	}
	if len(entries) == maxReceipts+128 {
		return nil, errors.New("执行历史目录条目超限")
	}
	total := int64(len(data) - len(old))
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".") {
			info, err := entry.Info()
			if err != nil {
				return nil, err
			}
			total += info.Size()
		}
	}
	if total > 64<<20 {
		return nil, errors.New("执行历史超过 64 MiB；拒绝新增或扩大记录")
	}
	name := ".receipt-" + rand.Text()
	f, err := s.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	defer s.root.Remove(name)
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err != nil {
		return nil, err
	}
	if ce != nil {
		return nil, ce
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.check(); err != nil {
		return nil, err
	}
	id := r.Data.ID + ".json"
	current, err := receiptBytes(s.root, id)
	if err != nil || !bytes.Equal(current, old) {
		return nil, errors.New("原执行回执已变化，拒绝覆盖")
	}
	if err := s.root.Rename(name, id); err != nil {
		return nil, err
	}
	if err := syncReceiptDir(s.root); err != nil {
		return nil, err
	}
	return data, nil
}

func syncReceiptDir(root *os.Root) error {
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
