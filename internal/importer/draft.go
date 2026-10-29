package importer

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/observation"
	"github.com/infrafile/infrafile/internal/validate"
)

type SourceFile struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
}

type Candidate struct {
	ID       string               `json:"id"`
	Source   string               `json:"source"`
	Identity observation.Identity `json:"identity"`
	Selected bool                 `json:"selected"`
	Record   map[string]any       `json:"record"`
}

type Draft struct {
	Version    string       `json:"version"`
	Kind       string       `json:"kind"`
	Source     string       `json:"source"`
	Files      []SourceFile `json:"files"`
	RepoHash   string       `json:"repo_hash"`
	Candidates []Candidate  `json:"candidates"`
}

// PrepareDraft records exact source bytes; it neither publishes assets nor changes sources.
func PrepareDraft(ctx context.Context, repo *ledger.Repo, source, file string) (*Draft, *ReviewResult, error) {
	draft, notices, err := newDraft(ctx, repo, source, file)
	if err != nil {
		return nil, nil, err
	}
	review, err := reviewAgainst(ctx, repo, draft, draft, notices)
	return draft, review, err
}

// ReviewDraft never opens paths supplied by the draft. SSH input is selected by the caller.
func ReviewDraft(ctx context.Context, repo *ledger.Repo, draft *Draft, file string) (*ReviewResult, error) {
	if err := draft.shape(); err != nil {
		return nil, err
	}
	fresh, notices, err := newDraft(ctx, repo, draft.Source, file)
	if err != nil {
		return nil, err
	}
	return reviewAgainst(ctx, repo, draft, fresh, notices)
}

func newDraft(ctx context.Context, repo *ledger.Repo, source, file string) (*Draft, []Notice, error) {
	var entries []Entry
	notices := []Notice{}
	files := []SourceFile{}
	var err error
	switch source {
	case "state":
		if file != "" {
			return nil, nil, errors.New("state 草稿不接受 SSH 来源路径")
		}
		entries, notices, files, err = draftStateSource(ctx, repo.Dir)
	case "ssh-config":
		if file == "" {
			return nil, nil, errors.New("SSH 草稿必须显式指定 --file；不从草稿内选择本机路径")
		}
		inputs := map[string]string{}
		entries, err = sshConfig(ctx, file, inputs)
		for path, hash := range inputs {
			files = append(files, SourceFile{path, hash})
		}
	default:
		return nil, nil, errors.New("不支持的导入来源")
	}
	if err != nil {
		return nil, nil, err
	}
	if len(entries) > maxDraftCandidates || len(files) > maxDraftFiles {
		return nil, nil, errors.New("导入草稿超过 4096 个候选或 1024 个来源文件上限，请缩小来源范围")
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	base, err := repoHash(repo)
	if err != nil {
		return nil, nil, err
	}
	d := &Draft{Version: "v1", Kind: "ImportDraft", Source: source, Files: files, RepoHash: base, Candidates: []Candidate{}}
	schemas, err := ledger.LoadSchemas()
	if err != nil {
		return nil, nil, err
	}
	seen := map[string]Candidate{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if !validate.SnapshotSafe(map[string]any{"source": entry.Source, "record": entry.Record}) || schemas.Validate(entry.Kind, entry.Record) != nil || len(entry.Name+".yaml") > 255 {
			return nil, nil, errors.New("来源不能安全映射为记录；请检查名称、字段、日期和敏感数据")
		}
		identity := observation.IdentityOf(entry.Kind, entry.Name, entry.Record["spec"].(map[string]any))
		encoded, _ := json.Marshal(map[string]any{"source": entry.Source, "identity": identity})
		id := fmt.Sprintf("%x", sha256.Sum256(encoded))
		candidate := Candidate{id, entry.Source, identity, true, entry.Record}
		if previous, ok := seen[id]; ok {
			if !equalJSON(previous.Record, entry.Record) {
				return nil, nil, errors.New("同一来源自然身份存在不同记录；请先处置源冲突")
			}
			notices = append(notices, Notice{Source: entry.Source, Reason: "duplicate"})
			continue
		}
		seen[id] = candidate
		d.Candidates = append(d.Candidates, candidate)
	}
	sort.Slice(d.Candidates, func(i, j int) bool { return d.Candidates[i].ID < d.Candidates[j].ID })
	return d, notices, nil
}

func repoHash(repo *ledger.Repo) (string, error) {
	values := map[string]any{ledger.ConfigFile: repo.Config.Value}
	for _, r := range repo.Records {
		values[r.File] = r.Value
	}
	data, err := json.Marshal(values)
	if err != nil {
		return "", errors.New("数据仓不能编码为版本基准")
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

func equalJSON(a, b any) bool {
	x, e1 := json.Marshal(a)
	y, e2 := json.Marshal(b)
	return e1 == nil && e2 == nil && string(x) == string(y)
}
