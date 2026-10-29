// Package change previews, commits and recovers bounded inventory edits.
package change

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"

	"github.com/infrafile/infrafile/internal/ledger"
)

const maxBytes = 4 << 20
const maxFiles = 64

var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Request struct {
	Version string           `json:"version"`
	Records []map[string]any `json:"records"`
}

type Base struct {
	Repository string `json:"repository"`
	GitDir     string `json:"git_dir"`
	CommonDir  string `json:"common_dir"`
	Branch     string `json:"branch"`
	Head       string `json:"head"`
	IndexHash  string `json:"index_hash"`
	ConfigHash string `json:"config_hash"`
	InputHash  string `json:"input_hash"`
}

type File struct {
	Key        string `json:"key"`
	Path       string `json:"path"`
	Before     string `json:"before"`
	After      string `json:"after"`
	BeforeHash string `json:"before_hash"`
	AfterHash  string `json:"after_hash"`
	Existed    bool   `json:"existed"`
	Mode       uint32 `json:"mode"`
}

type Preview struct {
	Version         string   `json:"version"`
	Kind            string   `json:"kind"`
	Base            Base     `json:"base"`
	PreserveSecrets bool     `json:"preserve_secrets"`
	Request         Request  `json:"request"`
	OK              bool     `json:"ok"`
	Problems        []string `json:"problems"`
	Files           []File   `json:"files"`
	Digest          string   `json:"digest"`
}

type Result struct {
	Version     string `json:"version"`
	Status      string `json:"status"`
	Code        string `json:"code"`
	Transaction string `json:"transaction,omitempty"`
	Commit      string `json:"commit,omitempty"`
}

func hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func randomID() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func encoded(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil || len(b) > maxBytes {
		return nil, errors.New("变更超过 4 MiB 或无法编码，请缩小批次")
	}
	return b, nil
}

func decode(ctx context.Context, b []byte, dst any) error {
	if len(b) > maxBytes || !json.Valid(b) {
		return errors.New("变更不是有界 JSON")
	}
	doc, ps, err := ledger.ParseDocument(ctx, "edit", b)
	if err != nil || len(ps) != 0 || doc == nil {
		return errors.New("变更含重复键或超出解析限制")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	d.DisallowUnknownFields()
	if d.Decode(dst) != nil {
		return errors.New("变更字段或类型错误")
	}
	canonical, err := encoded(dst)
	if err != nil {
		return err
	}
	var a, c any
	for i, data := range [][]byte{b, canonical} {
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber()
		var value any
		if dec.Decode(&value) != nil {
			return errors.New("变更无法编码")
		}
		if i == 0 {
			a = value
		} else {
			c = value
		}
	}
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(c)
	if !bytes.Equal(x, y) {
		return errors.New("变更缺字段或字段名不规范")
	}
	return nil
}

func DecodeRequest(ctx context.Context, b []byte) (Request, error) {
	var r Request
	err := decode(ctx, b, &r)
	if err == nil && (r.Version != "v1" || r.Records == nil || len(r.Records) > maxFiles) {
		err = errors.New("请求需要 v1 和最多 64 条记录")
	}
	return r, err
}
func ReadPreview(ctx context.Context, path string) (*Preview, error) {
	b, err := readExternal(path, maxBytes)
	if err != nil {
		return nil, err
	}
	var p Preview
	if err = decode(ctx, b, &p); err != nil {
		return nil, err
	}
	if p.Version != "v1" || p.Kind != "EditPreview" || !digestPattern.MatchString(p.Digest) {
		return nil, errors.New("预览版本或摘要错误")
	}
	return &p, nil
}
func (p *Preview) Encode() ([]byte, error) { return encoded(p) }
func (p *Preview) sum() (string, error) {
	copy := *p
	copy.Digest = ""
	b, err := encoded(&copy)
	if err != nil {
		return "", err
	}
	return hash(b), nil
}
func writeAll(w io.Writer, b []byte) error {
	n, err := w.Write(b)
	if err == nil && n != len(b) {
		return io.ErrShortWrite
	}
	return err
}
