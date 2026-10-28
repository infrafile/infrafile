package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/infrafile/infrafile/internal/observation"
)

func prepareCDNOrigins(ctx context.Context, in *Input) error {
	watches, problems := observation.Watches(in.Repo)
	if len(problems) != 0 {
		return errors.New("audit.watch 检查范围无效，请先校验台账")
	}
	bindings := map[string]observation.Watch{}
	for _, w := range watches {
		bindings[w.Target] = w
	}
	snaps := map[string]Snapshot{}
	for _, s := range in.Snapshots {
		snaps[s.Plugin+"/"+s.Instance] = s
	}
	for _, r := range in.Repo.Records {
		if err := ctx.Err(); err != nil {
			return err
		}
		if r.Kind != "Resource" || r.Archived() {
			continue
		}
		spec, _ := field(r.Value, "spec").(map[string]any)
		value, exists := spec["origin"]
		if !exists {
			continue
		}
		target := "Resource/" + r.Name
		notice := func(message string) {
			in.cdnNotices = append(in.cdnNotices, Notice{Code: "cdn_origin_unknown", Rule: "cdn_drift", Target: target, File: r.File, Message: message})
		}
		w, bound := bindings[target]
		s := snaps[w.Plugin+"/"+w.Instance]
		switch {
		case !bound:
			notice("回源检查需要 audit.watch 显式绑定插件实例与服务")
			continue
		case w.Config["collect_origin"] != true:
			notice("检查实例尚未显式开启 collect_origin")
			continue
		case in.MaxAge == nil || *in.MaxAge < 0:
			notice("回源检查需要非负 --max-age")
			continue
		case !s.verified || s.ObservedAt == nil:
			notice("没有与当前配置、时间、快照对应的成功回执，不能判断回源漂移")
			continue
		case s.ObservedAt.After(in.Now):
			notice("观测时间在未来，不能判断回源漂移")
			continue
		case *s.AgeDays(in.Now) > *in.MaxAge || *s.AgeDays(in.Now) == *in.MaxAge && in.Now.Sub(*s.ObservedAt)%(24*time.Hour) > 0:
			notice("观测时间超过 --max-age，不能判断回源漂移")
			continue
		}
		items, ok := s.Spec[w.Service].([]any)
		if !ok {
			notice("快照没有返回声明服务的完整集合")
			continue
		}
		var origins []any
		for _, item := range items {
			identities, _, err := observation.WatchRecords(w.Plugin, w.Instance, w.Service, map[string]any{w.Service: []any{item}})
			if err != nil {
				return fmt.Errorf("%s: %w", s.File, err)
			}
			if w.Seen(identities) {
				origins = append(origins, item.(map[string]any)["origin"])
			}
		}
		if len(origins) != 1 {
			notice("未找到唯一匹配的回源观测；缺失资产或多个产品、区域不能合并判断")
			continue
		}
		var observed observation.Origin
		data, _ := json.Marshal(origins[0])
		if err := json.Unmarshal(data, &observed); err != nil {
			notice("回源观测结构无效")
			continue
		}
		observed.Normalize()
		if observed.Scope != "global" {
			notice("回源字段缺失或包含尚未支持的条件、区域、规则，不能判断回源漂移")
			continue
		}
		var expected observation.ExpectedOrigin
		data, _ = json.Marshal(value)
		if err := json.Unmarshal(data, &expected); err != nil || len(expected.Hosts) == 0 {
			return errors.New("期望回源结构无效，请先校验台账")
		}
		for i, host := range expected.Hosts {
			var ok bool
			expected.Hosts[i], ok = observation.OriginHost(host)
			if !ok {
				return errors.New("期望回源主机无效，请先校验台账")
			}
		}
		slices.Sort(expected.Hosts)
		expected.Hosts = slices.Compact(expected.Hosts)
		hosts := []string{}
		portKnown, protocolKnown := true, true
		portEqual, protocolEqual := true, true
		for _, source := range observed.Sources {
			hosts = append(hosts, source.Host)
			portKnown = portKnown && source.Port != 0
			protocolKnown = protocolKnown && source.Protocol != ""
			portEqual = portEqual && source.Port == expected.Port
			protocolEqual = protocolEqual && source.Protocol == expected.Protocol
		}
		slices.Sort(hosts)
		hosts = slices.Compact(hosts)
		differences, unknown := []string{}, []string{}
		if !slices.Equal(hosts, expected.Hosts) {
			differences = append(differences, "hosts")
		}
		if expected.Port != 0 {
			if !portKnown {
				unknown = append(unknown, "port")
			} else if !portEqual {
				differences = append(differences, "port")
			}
		}
		if expected.Protocol != "" {
			if !protocolKnown {
				unknown = append(unknown, "protocol")
			} else if !protocolEqual {
				differences = append(differences, "protocol")
			}
		}
		if len(unknown) > 0 {
			notice("观测未明确返回期望字段：" + strings.Join(unknown, ", ") + "；未推断默认值")
		}
		if len(differences) == 0 {
			continue
		}
		in.cdnFindings = append(in.cdnFindings, Finding{Rule: "cdn_drift", Target: target, File: r.File,
			Message: "观测回源与台账期望不一致：" + strings.Join(differences, ", "),
			Evidence: map[string]any{"aspect": "origin", "identity": w.Identity, "plugin": w.Plugin, "instance": w.Instance, "service": w.Service,
				"snapshot_file": s.File, "observed_at": s.ObservedAt.UTC().Format(time.RFC3339), "max_age_days": *in.MaxAge,
				"expected": expected, "observed": observed, "differences": differences}})
	}
	return nil
}
