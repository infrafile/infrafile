package importer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/validate"
)

const (
	maxDraftBytes      = 4 << 20
	maxDraftCandidates = 4096
	maxDraftFiles      = 1024
)

var draftDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (d *Draft) shape() error {
	if d.Version != "v1" || d.Kind != "ImportDraft" || (d.Source != "state" && d.Source != "ssh-config") || !draftDigest.MatchString(d.RepoHash) || d.Files == nil || d.Candidates == nil {
		return errors.New("导入草稿版本或结构不合法")
	}
	if len(d.Candidates) > maxDraftCandidates || len(d.Files) > maxDraftFiles {
		return errors.New("导入草稿超过 4096 个候选或 1024 个来源文件上限")
	}
	files, ids := map[string]bool{}, map[string]bool{}
	for _, f := range d.Files {
		if f.Path == "" || !draftDigest.MatchString(f.Hash) || files[f.Path] {
			return errors.New("来源文件清单不合法")
		}
		files[f.Path] = true
	}
	for _, c := range d.Candidates {
		if !draftDigest.MatchString(c.ID) || ids[c.ID] || c.Source == "" || c.Record == nil {
			return errors.New("候选身份重复或结构不合法")
		}
		ids[c.ID] = true
	}
	return nil
}

func decodeDraft(ctx context.Context, data []byte) (*Draft, error) {
	if len(data) > maxDraftBytes {
		return nil, errors.New("导入草稿超过 4 MiB")
	}
	doc, problems, err := ledger.ParseDocument(ctx, "import-draft", data)
	if err != nil {
		return nil, err
	}
	if doc == nil || len(problems) != 0 {
		return nil, errors.New("导入草稿含重复键、多文档或超出解析限制")
	}
	raw, err := json.Marshal(doc.Value)
	if err != nil {
		return nil, errors.New("导入草稿不是有效 JSON 对象")
	}
	var d Draft
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if decoder.Decode(&d) != nil {
		return nil, errors.New("导入草稿含未知字段或类型错误")
	}
	encoded, err := json.Marshal(&d)
	if err != nil {
		return nil, errors.New("导入草稿不能编码")
	}
	canonical, ps, err := ledger.ParseDocument(ctx, "import-draft", encoded)
	if err != nil {
		return nil, err
	}
	if len(ps) != 0 || canonical == nil || !reflect.DeepEqual(canonical.Value, doc.Value) {
		return nil, errors.New("导入草稿缺少字段或使用非规范字段名")
	}
	if err := d.shape(); err != nil {
		return nil, err
	}
	return &d, nil
}

func ReadDraft(ctx context.Context, path string) (*Draft, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("导入草稿必须是可读的普通文件，不能是符号链接")
	}
	if info.Size() > maxDraftBytes {
		return nil, errors.New("导入草稿超过 4 MiB")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("无法打开导入草稿")
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("导入草稿在打开时发生变化")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxDraftBytes+1))
	if err != nil {
		return nil, errors.New("无法完整读取导入草稿")
	}
	return decodeDraft(ctx, data)
}

func (d *Draft) Encode(ctx context.Context) ([]byte, error) {
	if d == nil {
		return nil, errors.New("导入草稿为空")
	}
	for _, candidate := range d.Candidates {
		if len(validate.PlaintextProblems(&ledger.Document{File: "candidate", Value: candidate.Record})) > 0 {
			return nil, errors.New("草稿含疑似明文凭据，已拒绝保存")
		}
	}
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return nil, errors.New("导入草稿不能编码")
	}
	data = append(data, '\n')
	if _, err := decodeDraft(ctx, data); err != nil {
		return nil, err
	}
	return data, nil
}

// SaveDraft creates one complete private file without replacing a previous review.
func (d *Draft) Save(ctx context.Context, repoDir, path string) error {
	data, err := d.Encode(ctx)
	if err != nil {
		return err
	}
	canonical, err := ReviewPath(repoDir, path)
	if err != nil {
		return err
	}
	if _, err := reviewPath(repoDir, canonical, "state"); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	parent := filepath.Dir(canonical)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return errors.New("无法创建草稿目录")
	}
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() {
		return errors.New("草稿目录不能是符号链接")
	}
	root, err := os.OpenRoot(parent)
	if err != nil {
		return errors.New("无法打开草稿目录")
	}
	defer root.Close()
	opened, err := root.Lstat(".")
	if err != nil || !os.SameFile(info, opened) {
		return errors.New("草稿目录在打开时发生变化")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	created, err := createFile(ctx, root, filepath.Base(canonical), data, 0o600)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("草稿保存失败；请检查权限及文件系统硬链接支持")
	}
	if !created {
		return errors.New("草稿文件已存在，保留原审阅内容；请指定新文件")
	}
	return nil
}
