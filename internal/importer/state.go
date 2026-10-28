package importer

import (
	"context"
	"fmt"

	"github.com/infrafile/infrafile/internal/observation"
	"github.com/infrafile/infrafile/internal/snapshot"
)

func State(ctx context.Context, dir string) ([]Entry, []Notice, error) {
	snapshots, err := snapshot.Read(ctx, dir, nil)
	if err != nil {
		return nil, nil, err
	}
	entries, notices := []Entry{}, []Notice{}
	for _, s := range snapshots {
		mapped, skipped, err := observation.Map(s.Plugin, s.Instance, s.File, s.Spec)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %s", s.File, err)
		}
		for _, r := range mapped {
			entries = append(entries, draft(s.File, r.Kind, r.Name, r.Spec))
		}
		notices = append(notices, skipped...)
	}
	return entries, notices, nil
}
