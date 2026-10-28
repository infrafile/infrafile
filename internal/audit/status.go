package audit

import (
	"context"
	"sort"
	"time"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/plugin"
	"github.com/infrafile/infrafile/internal/snapshot"
)

func syncNotices(ctx context.Context, repo *ledger.Repo, enabled map[string]map[string]any, all []snapshot.Snapshot, stamps map[string]map[string]time.Time) ([]Notice, map[string]bool, error) {
	status, err := snapshot.ReadStatus(ctx, repo.Dir)
	if err != nil || status == nil {
		return nil, nil, err
	}
	verified := map[string]bool{}
	hashes := map[string]string{}
	for _, s := range all {
		if !s.Skipped {
			hashes[s.Plugin+"/"+s.Instance] = s.Hash
		}
	}
	notices := []Notice{}
	for name, instances := range enabled {
		for instance, config := range instances {
			attempt, ok := status.Spec[name][instance]
			at, _ := time.Parse(time.RFC3339, attempt.AttemptedAt)
			stamp := stamps[name][instance]
			n := Notice{Code: "sync_status_unverified", File: "state/" + snapshot.StatusFile, Target: name + "/" + instance,
				Message: "执行回执缺失，或与当前配置、成功时间、快照不对应；不能据此判断本轮采集完整"}
			if ok && attempt.ConfigHash == plugin.Hash(config) && !stamp.After(at) {
				switch attempt.Status {
				case "failed":
					n.Code, n.Message = "sync_failed", "最近一次同步失败 ["+attempt.Code+"]；已有快照仅代表历史观测"
				case "running":
					n.Code, n.Message = "sync_incomplete", "最近一次同步未留下结束回执；请核对执行状态后重新采集"
				case "succeeded":
					if stamp.Equal(at) && hashes[n.Target] == attempt.SnapshotHash {
						verified[n.Target] = true
						continue
					}
				}
			}
			notices = append(notices, n)
		}
	}
	sort.Slice(notices, func(i, j int) bool { return notices[i].Target < notices[j].Target })
	return notices, verified, nil
}
