package audit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/infrafile/infrafile/internal/observation"
	"github.com/infrafile/infrafile/internal/plugin"
	"github.com/infrafile/infrafile/internal/snapshot"
)

var missing = Rule{
	ID: "missing", Description: "曾观测到的在用资产在指定查询范围内不再可见",
	Ready: func(in *Input) error {
		if in.MaxAge == nil || *in.MaxAge < 0 {
			return errors.New("规则 missing 需要非负 --max-age")
		}
		return nil
	},
	Eval: func(in *Input) []Finding { return in.missingFindings },
}

func prepareMissing(ctx context.Context, in *Input) error {
	watches, problems := observation.Watches(in.Repo)
	if len(problems) != 0 {
		return errors.New("audit.watch 检查范围无效，请先校验台账")
	}
	presence, err := snapshot.ReadPresence(ctx, in.Repo.Dir)
	if err != nil {
		return err
	}
	baselines := map[string]snapshot.PresenceEntry{}
	if presence != nil {
		baselines = presence.Spec
	}
	snaps := map[string]Snapshot{}
	for _, s := range in.Snapshots {
		snaps[s.Plugin+"/"+s.Instance] = s
	}
	type collection struct {
		records  map[observation.Identity]bool
		complete bool
	}
	collections := map[string]collection{}
	in.missingFindings, in.missingNotices = nil, nil
	for _, w := range watches {
		if err := ctx.Err(); err != nil {
			return err
		}
		s := snaps[w.Plugin+"/"+w.Instance]
		key := w.Plugin + "/" + w.Instance + "/" + w.Service
		c, ok := collections[key]
		if !ok {
			c.records, c.complete, err = observation.WatchRecords(w.Plugin, w.Instance, w.Service, s.Spec)
			if err != nil {
				return fmt.Errorf("%s: %w", s.File, err)
			}
			collections[key] = c
		}
		base := baselines[w.Target]
		_, message := missingQualification(in, w, s, c.complete, base)
		if message != "" {
			in.missingNotices = append(in.missingNotices, Notice{Code: "missing_unknown", Rule: "missing", Target: w.Target, File: w.Record.File,
				Message: message + "（" + key + "）；不能判断查询范围内是否缺失"})
			continue
		}
		if w.Seen(c.records) {
			continue
		}
		in.missingFindings = append(in.missingFindings, Finding{Rule: "missing", Target: w.Target, File: w.Record.File,
			Message: "曾观测到此资产，本次在指定凭据的查询范围内未找到；请核对权限和范围，不能据此断言已删除",
			Evidence: map[string]any{"identity": w.Identity, "plugin": w.Plugin, "instance": w.Instance, "service": w.Service,
				"snapshot_file": s.File, "observed_at": s.ObservedAt.UTC().Format(time.RFC3339), "last_seen": base.LastSeen, "max_age_days": *in.MaxAge}})
	}
	return nil
}

// missingQualification is shared by audit and inspect; a negative result never proves absence.
func missingQualification(in *Input, w observation.Watch, s Snapshot, complete bool, base snapshot.PresenceEntry) (string, string) {
	lastSeen, _ := time.Parse(time.RFC3339, base.LastSeen)
	switch {
	case !s.verified || s.ObservedAt == nil:
		return "unverified", "没有与当前配置、时间、快照对应的成功回执"
	case s.ObservedAt.After(in.Now):
		return "future", "观测时间在未来"
	case *s.AgeDays(in.Now) > *in.MaxAge || *s.AgeDays(in.Now) == *in.MaxAge && in.Now.Sub(*s.ObservedAt)%(24*time.Hour) > 0:
		return "stale", "观测时间超过 --max-age"
	case !complete:
		return "incomplete", "快照没有返回声明服务的完整集合"
	case base.BindingHash != plugin.Hash(w):
		return "not_baselined", "当前身份、范围和配置尚未建立正向存在基线"
	case lastSeen.After(*s.ObservedAt):
		return "baseline_future", "正向基线晚于本次观测，不能核对证据顺序"
	}
	return "checked", ""
}
