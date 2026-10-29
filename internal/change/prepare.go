package change

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/validate"
	"go.yaml.in/yaml/v3"
)

var namePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?$`)

func recordPath(record map[string]any) (string, string, error) {
	kind, _ := record["kind"].(string)
	k, ok := ledger.KindByName(kind)
	md, _ := record["metadata"].(map[string]any)
	name, _ := md["name"].(string)
	if !ok || !namePattern.MatchString(name) || len(name+".yaml") > 255 {
		return "", "", errors.New("记录 kind/name 无效")
	}
	return kind + "/" + name, "inventory/" + k.Dir + "/" + name + ".yaml", nil
}

// Prepare binds an entire validated candidate to a clean Git baseline; it writes no repository files.
// preserveSecrets is for the browser: it rejects supplied secrets and retains existing references.
func Prepare(ctx context.Context, dir string, request Request, preserveSecrets bool) (*Preview, error) {
	return prepare(ctx, dir, request, preserveSecrets, false)
}

func prepare(ctx context.Context, dir string, request Request, preserveSecrets, locked bool) (*Preview, error) {
	if err := supportsEdits(); err != nil {
		return nil, err
	}
	b, err := encoded(request)
	if err != nil {
		return nil, err
	}
	request, err = DecodeRequest(ctx, b)
	if err != nil {
		return nil, err
	}
	r, err := openRepository(ctx, dir)
	if err != nil {
		return nil, err
	}
	if !locked {
		if err = ledger.CheckEdit(r.dir); err != nil {
			return nil, err
		}
	}
	inputs, inputHash, err := capture(ctx, r.dir)
	if err != nil {
		return nil, err
	}
	base, err := r.base(ctx, inputHash, locked)
	if err != nil {
		return nil, err
	}
	p := &Preview{Version: "v1", Kind: "EditPreview", Base: base, PreserveSecrets: preserveSecrets, Request: request, Problems: []string{}, Files: []File{}}
	temp, err := os.MkdirTemp("", "infrafile-edit-preview-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(temp)
	for name, data := range inputs {
		target := filepath.Join(temp, filepath.FromSlash(name))
		if strings.HasSuffix(name, "/") {
			err = os.MkdirAll(target, 0700)
		} else {
			err = os.MkdirAll(filepath.Dir(target), 0700)
			if err == nil {
				err = os.WriteFile(target, data, 0600)
			}
		}
		if err != nil {
			return nil, err
		}
	}
	initial, err := validate.Run(ctx, temp, validate.Options{})
	if err != nil {
		return nil, err
	}
	if len(initial.Problems) > 0 {
		return nil, errors.New("原台账校验失败，请先通过 validate 修复")
	}
	cfg, _ := initial.Repo.Config.Value.(map[string]any)
	if cfg["storageVersion"] != "v1" {
		return nil, errors.New("台账变更须先显式启用 storageVersion: v1")
	}
	seen := map[string]bool{}
	for _, record := range request.Records {
		key, name, e := recordPath(record)
		if e != nil {
			p.Problems = append(p.Problems, "record_identity")
			continue
		}
		if seen[name] {
			p.Problems = append(p.Problems, "duplicate_record")
			continue
		}
		seen[name] = true
		before, exists := inputs[name]
		var old map[string]any
		if exists {
			doc, ps, e := ledger.ParseDocument(ctx, name, before)
			if e != nil || len(ps) > 0 {
				return nil, errors.New("原记录解析失败")
			}
			old, _ = doc.Value.(map[string]any)
		}
		data, e := encoded(record)
		if e != nil {
			return nil, e
		}
		doc, ps, e := ledger.ParseDocument(ctx, name, data)
		if e != nil || len(ps) > 0 {
			return nil, errors.New("候选解析失败")
		}
		replacement, _ := doc.Value.(map[string]any)
		if preserveSecrets {
			if !validate.SnapshotSafe(record) {
				p.Problems = append(p.Problems, "sensitive_input")
				continue
			}
			spec, _ := replacement["spec"].(map[string]any)
			oldSpec, _ := old["spec"].(map[string]any)
			if refs, ok := oldSpec["secrets"]; ok {
				if spec == nil {
					p.Problems = append(p.Problems, "record_spec")
					continue
				}
				spec["secrets"] = refs
			}
		}
		if reflect.DeepEqual(old, replacement) {
			continue
		}
		after, e := yaml.Marshal(replacement)
		if e != nil {
			return nil, e
		}
		mode := uint32(0600)
		if exists {
			info, e := os.Lstat(filepath.Join(r.dir, filepath.FromSlash(name)))
			if e != nil || !info.Mode().IsRegular() {
				return nil, errors.New("原记录已变化")
			}
			mode = uint32(info.Mode().Perm())
		}
		p.Files = append(p.Files, File{Key: key, Path: name, Before: string(before), After: string(after), BeforeHash: hash(before), AfterHash: hash(after), Existed: exists, Mode: mode})
		if e = os.MkdirAll(filepath.Dir(filepath.Join(temp, name)), 0700); e == nil {
			e = os.WriteFile(filepath.Join(temp, name), after, 0600)
		}
		if e != nil {
			return nil, e
		}
	}
	candidate, err := validate.Run(ctx, temp, validate.Options{})
	if err != nil {
		return nil, err
	}
	for _, problem := range candidate.Problems {
		p.Problems = append(p.Problems, string(problem.Code))
	}
	sort.Slice(p.Files, func(i, j int) bool { return p.Files[i].Path < p.Files[j].Path })
	sort.Strings(p.Problems)
	p.OK = len(p.Problems) == 0
	if !p.OK {
		p.Request.Records = []map[string]any{}
		p.Files = []File{}
	}
	_, current, err := capture(ctx, r.dir)
	if err != nil || current != inputHash {
		return nil, errors.New("预览期间台账发生变化，请重新预览")
	}
	fresh, err := openRepository(ctx, r.dir)
	if err != nil {
		return nil, err
	}
	again, err := fresh.base(ctx, current, locked)
	if err != nil || again != base {
		return nil, errors.New("预览期间 Git 基准发生变化")
	}
	p.Digest, err = p.sum()
	if err != nil {
		return nil, err
	}
	return p, nil
}
