package main

import (
	"fmt"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/validate"
)

func loadQueryRepo(dir string) (*ledger.Repo, error) {
	res, err := validate.Run(dir, validate.Options{})
	if err != nil {
		return nil, err
	}
	if len(res.Problems) != 0 {
		return nil, fmt.Errorf("数据仓有 %d 个问题，请先运行 infrafile validate 校验该目录", len(res.Problems))
	}
	return res.Repo, nil
}
