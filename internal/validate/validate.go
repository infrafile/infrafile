// Package validate 校验数据仓：结构、外键、文件名、归档引用、明文凭据和防删除。
package validate

import (
	"fmt"

	"github.com/infrafile/infrafile/internal/ledger"
)

// Options 是 validate 的可选项。
type Options struct {
	// Base 非空时，与这个 git 引用对比，报告被删除的记录。
	Base string
}

// Result 是一次校验的结果。
type Result struct {
	Records  int              // 成功加载的记录数
	Problems []ledger.Problem // 已排序；没有问题时为空切片
}

// Run 校验 dir 下的数据仓。数据的问题放在 Result.Problems 里；
// 不是数据仓、读不到基线等命令无法执行的情况返回 error。
func Run(dir string, opts Options) (*Result, error) {
	repo, problems, err := ledger.Load(dir)
	if err != nil {
		return nil, err
	}
	schemas, err := ledger.LoadSchemas()
	if err != nil {
		return nil, err
	}

	if repo.Config != nil {
		problems = append(problems, schemaProblems(repo.Config, schemas.Validate(ledger.ConfigKind, repo.Config.Value))...)
	}
	for _, r := range repo.Records {
		problems = append(problems, schemaProblems(r.Document, schemas.Validate(r.Kind, r.Value))...)
		if r.Name != "" && r.Name != r.Stem() {
			problems = append(problems, ledger.Problem{Code: ledger.CodeName, File: r.File,
				Line: r.Line("metadata.name"), Path: "metadata.name",
				Message: fmt.Sprintf("metadata.name 是 %s，与文件名 %s.yaml 不一致", r.Name, r.Stem())})
		}
		problems = append(problems, scanPlaintext(r.Document)...)
	}
	problems = append(problems, checkRefs(repo.Records, schemas)...)

	if opts.Base != "" {
		ps, err := checkDeleted(repo, opts.Base)
		if err != nil {
			return nil, err
		}
		problems = append(problems, ps...)
	}

	if problems == nil {
		problems = []ledger.Problem{}
	}
	ledger.SortProblems(problems)
	return &Result{Records: len(repo.Records), Problems: problems}, nil
}

func checkRefs(records []*ledger.Record, schemas *ledger.Schemas) []ledger.Problem {
	index := map[string]map[string]*ledger.Record{}
	for _, r := range records {
		if r.Name == "" {
			continue
		}
		if index[r.Kind] == nil {
			index[r.Kind] = map[string]*ledger.Record{}
		}
		index[r.Kind][r.Name] = r
	}

	var problems []ledger.Problem
	for _, r := range records {
		for _, ref := range schemas.Refs(r.Kind) {
			matchPath(r.Value, ref.Pattern, "", func(path string, v any) {
				name, ok := v.(string)
				if !ok {
					return // 类型不对由 schema 报
				}
				target, ok := index[ref.Target][name]
				switch {
				case !ok:
					problems = append(problems, ledger.Problem{Code: ledger.CodeRef, File: r.File,
						Line: r.Line(path), Path: path,
						Message: fmt.Sprintf("引用的 %s %s 不存在", ref.Target, name)})
				case target.Archived() && !r.Archived():
					problems = append(problems, ledger.Problem{Code: ledger.CodeRefArchived, File: r.File,
						Line: r.Line(path), Path: path,
						Message: fmt.Sprintf("引用的 %s %s 已归档；在用的记录不能引用已归档的记录", ref.Target, name)})
				}
			})
		}
	}
	return problems
}

// matchPath 找出 v 中符合 pattern 的所有值，"[]" 匹配数组的每个元素。
func matchPath(v any, pattern []string, path string, fn func(path string, v any)) {
	if len(pattern) == 0 {
		fn(path, v)
		return
	}
	if pattern[0] == "[]" {
		items, _ := v.([]any)
		for i, item := range items {
			matchPath(item, pattern[1:], ledger.JoinIndex(path, i), fn)
		}
		return
	}
	m, _ := v.(map[string]any)
	child, ok := m[pattern[0]]
	if !ok {
		return
	}
	matchPath(child, pattern[1:], ledger.JoinKey(path, pattern[0]), fn)
}
