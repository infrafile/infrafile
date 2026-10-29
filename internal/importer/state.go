package importer

import (
	"context"
	"fmt"

	"github.com/infrafile/infrafile/internal/observation"
	"github.com/infrafile/infrafile/internal/snapshot"
)

func State(ctx context.Context, dir string) ([]Entry, []Notice, error) {
	entries, notices, _, err := stateSource(ctx, dir)
	return entries, notices, err
}

func stateSource(ctx context.Context, dir string) ([]Entry, []Notice, []SourceFile, error) {
	snapshots, err := snapshot.Read(ctx, dir, nil)
	return mapState(snapshots, err)
}

func draftStateSource(ctx context.Context, dir string) ([]Entry, []Notice, []SourceFile, error) {
	snapshots, err := snapshot.ReadLimited(ctx, dir, maxDraftFiles, 64<<20)
	return mapState(snapshots, err)
}

func mapState(snapshots []snapshot.Snapshot, err error) ([]Entry, []Notice, []SourceFile, error) {
	if err != nil {
		return nil, nil, nil, err
	}
	entries, notices := []Entry{}, []Notice{}
	files := []SourceFile{}
	for _, s := range snapshots {
		files = append(files, SourceFile{Path: s.File, Hash: s.Hash})
		mapped, skipped, err := observation.Map(s.Plugin, s.Instance, s.File, s.Spec)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("%s: %s", s.File, err)
		}
		for _, r := range mapped {
			entries = append(entries, draft(s.File, r.Kind, r.Name, r.Spec))
		}
		notices = append(notices, skipped...)
	}
	return entries, notices, files, nil
}
