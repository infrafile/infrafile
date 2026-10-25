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
	Repo     *ledger.Repo     // 同一次加载的记录，供查询命令在校验通过后使用
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
		problems = append(problems, scanPlaintext(repo.Config)...)
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
	return &Result{Repo: repo, Records: len(repo.Records), Problems: problems}, nil
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
		for _, ref := range r.References(schemas) {
			target, ok := index[ref.Kind][ref.Name]
			switch {
			case !ok:
				problems = append(problems, ledger.Problem{Code: ledger.CodeRef, File: r.File,
					Line: r.Line(ref.Path), Path: ref.Path,
					Message: fmt.Sprintf("引用的 %s %s 不存在", ref.Kind, ref.Name)})
			case target.Archived() && !r.Archived():
				problems = append(problems, ledger.Problem{Code: ledger.CodeRefArchived, File: r.File,
					Line: r.Line(ref.Path), Path: ref.Path,
					Message: fmt.Sprintf("引用的 %s %s 已归档；在用的记录不能引用已归档的记录", ref.Kind, ref.Name)})
			}
		}
	}
	return problems
}
