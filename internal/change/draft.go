package change

import (
	"context"
	"errors"
	"os"

	"github.com/infrafile/infrafile/internal/importer"
	"github.com/infrafile/infrafile/internal/validate"
)

// FromDraft rechecks source identity before freezing selected new records as an edit request.
func FromDraft(ctx context.Context, dir, draftPath, sourceFile string) (*Preview, error) {
	_, before, err := capture(ctx, dir)
	if err != nil {
		return nil, err
	}
	checked, err := validate.Run(ctx, dir, validate.Options{})
	if err != nil {
		return nil, err
	}
	if len(checked.Problems) != 0 {
		return nil, errors.New("原台账校验失败")
	}
	draft, err := importer.ReadDraft(ctx, draftPath)
	if err != nil {
		return nil, err
	}
	review, err := importer.ReviewDraft(ctx, checked.Repo, draft, sourceFile)
	if err != nil {
		return nil, err
	}
	if !review.OK {
		return nil, errors.New("导入来源、台账基准或候选校验未通过，请重新 import review")
	}
	selected := map[string]bool{}
	for _, entry := range review.Records {
		if entry.Status == "draft" {
			selected[entry.ID] = true
		}
	}
	request := Request{Version: "v1", Records: []map[string]any{}}
	for _, candidate := range draft.Candidates {
		if candidate.Selected && selected[candidate.ID] {
			request.Records = append(request.Records, candidate.Record)
		}
	}
	p, err := Prepare(ctx, dir, request, false)
	if err != nil {
		return nil, err
	}
	if p.Base.InputHash != before {
		return nil, errors.New("导入转换期间台账发生变化")
	}
	return p, nil
}

func ReadRequest(ctx context.Context, path string) (Request, error) {
	b, err := readExternal(path, maxBytes)
	if err != nil {
		return Request{}, err
	}
	return DecodeRequest(ctx, b)
}

func (p *Preview) Save(dir, out string) error {
	target, err := importer.ReviewPath(dir, out)
	if err != nil {
		return err
	}
	b, err := p.Encode()
	if err != nil {
		return err
	}
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("预览必须写到新的普通文件")
	}
	success := false
	defer func() {
		if !success {
			os.Remove(target)
		}
	}()
	err = writeAll(f, append(b, '\n'))
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err != nil {
		return err
	}
	if ce != nil {
		return ce
	}
	success = true
	return nil
}
