// Package audit 对照台账和插件快照做只读审计：规则只读取 Input，不访问网络，不写任何文件。
package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/observation"
	"github.com/infrafile/infrafile/internal/snapshot"
)

// Snapshot 是一份已启用插件实例的快照；ObservedAt 取自 state/_sync.yaml，没有同步记录时为 nil。
type Snapshot struct {
	Plugin     string
	Instance   string
	File       string
	ObservedAt *time.Time
	Spec       map[string]any
	verified   bool
	hash       string
}

// AgeDays 返回观测距 now 的整天数；没有同步记录时返回 nil。
func (s Snapshot) AgeDays(now time.Time) *int {
	if s.ObservedAt == nil {
		return nil
	}
	days := int(now.Sub(*s.ObservedAt).Hours() / 24)
	return &days
}

// Input 是规则求值所需的全部输入。MaxAge 为观测时间上限（天），未设置时为 nil。
type Input struct {
	Repo              *ledger.Repo
	Snapshots         []Snapshot
	Now               time.Time
	MaxAge            *int
	lifecycle         *lifecycleInput
	origins           map[string][]originEvidence
	originFindings    []Finding
	originNotices     []Notice
	cdnFindings       []Finding
	cdnNotices        []Notice
	cdnOriginCoverage map[string]map[string]bool
	missingFindings   []Finding
	missingNotices    []Notice
}

// Rule 是一条审计规则。Ready 检查所需观测和参数是否齐备，Eval 只读地求值。
type Rule struct {
	ID           string
	Description  string
	Ready        func(*Input) error
	Eval         func(*Input) []Finding
	Observations []observation.Requirement
}

// Finding 是一条偏差；Target 为 Kind/name 或 plugin/instance。
type Finding struct {
	Rule             string          `json:"rule"`
	Target           string          `json:"target"`
	File             string          `json:"file"`
	Message          string          `json:"message"`
	Evidence         map[string]any  `json:"evidence"`
	Waiver           *Waiver         `json:"waiver"`
	Issues           []IssueIdentity `json:"issues,omitempty"`
	identity         *observation.Identity
	plugin, instance string
}

// Waiver 是命中某条偏差的豁免；Source 为豁免所在文件。
type Waiver struct {
	Reason string `json:"reason"`
	Until  string `json:"until,omitempty"`
	Source string `json:"source"`
}

// Notice 是不影响退出码的提示。
type Notice struct {
	Code    string `json:"code"`
	File    string `json:"file"`
	Target  string `json:"target"`
	Rule    string `json:"rule,omitempty"`
	Message string `json:"message"`
}

// Rules 返回已实现的规则，按 ID 排序。
func Rules() []Rule {
	return []Rule{cdnDrift, missing, originExposure, orphan, staleState, zombie}
}

// Select 按 ID 选出要运行的规则；ids 为空时选出全部已就绪的规则。
// 显式点名的规则未就绪、未知或尚未实现时返回错误。
func Select(ids []string, in *Input) ([]Rule, error) {
	wanted := map[string]bool{}
	for _, id := range ids {
		i := slices.IndexFunc(Rules(), func(r Rule) bool { return r.ID == id })
		switch {
		case i >= 0:
			if err := Rules()[i].Ready(in); err != nil {
				return nil, err
			}
			wanted[id] = true
		default:
			return nil, fmt.Errorf("未知规则 %s", id)
		}
	}
	rules := []Rule{}
	for _, r := range Rules() {
		if len(ids) == 0 && r.Ready(in) == nil || wanted[r.ID] {
			rules = append(rules, r)
		}
	}
	return rules, nil
}

// ReadSnapshots 读取已在 infrafile.yaml 启用的插件实例的快照和同步时间；
// 未启用实例的快照不参与审计，各给一条提示。
func ReadSnapshots(ctx context.Context, repo *ledger.Repo) ([]Snapshot, []Notice, error) {
	enabled := map[string]map[string]any{}
	if repo.Config != nil {
		plugins, _ := field(repo.Config.Value, "plugins").(map[string]any)
		for name, p := range plugins {
			enabled[name], _ = field(p, "instances").(map[string]any)
		}
	}
	all, err := snapshot.Read(ctx, repo.Dir, func(plugin, instance string) bool {
		_, ok := enabled[plugin][instance]
		return !ok
	})
	if err != nil {
		return nil, nil, err
	}
	stamps, err := snapshot.ReadSync(repo.Dir)
	if err != nil {
		return nil, nil, err
	}
	execution, verified, err := syncNotices(ctx, repo, enabled, all, stamps)
	if err != nil {
		return nil, nil, err
	}
	snapshots, notices := []Snapshot{}, []Notice{}
	for _, s := range all {
		if s.Skipped {
			notices = append(notices, Notice{Code: "instance_disabled", File: s.File, Target: s.Plugin + "/" + s.Instance,
				Message: "插件实例未在 infrafile.yaml 启用，已忽略此快照"})
			continue
		}
		snap := Snapshot{Plugin: s.Plugin, Instance: s.Instance, File: s.File, Spec: s.Spec, verified: verified[s.Plugin+"/"+s.Instance], hash: s.Hash}
		if at, ok := stamps[s.Plugin][s.Instance]; ok {
			snap.ObservedAt = &at
		}
		snapshots = append(snapshots, snap)
	}
	return snapshots, append(notices, execution...), nil
}

type waiverEntry struct {
	rule, target     string
	waiver           Waiver
	identity         *observation.Identity
	plugin, instance string
}

// waivers 收集记录的 metadata.waivers 和 audit.ignore；归档记录只保留 zombie 豁免。
func waivers(repo *ledger.Repo) []waiverEntry {
	entries := []waiverEntry{}
	add := func(item any, target, source string) *waiverEntry {
		rule, _ := field(item, "rule").(string)
		reason, _ := field(item, "reason").(string)
		until, _ := field(item, "until").(string)
		entries = append(entries, waiverEntry{rule: rule, target: target, waiver: Waiver{reason, until, source}})
		return &entries[len(entries)-1]
	}
	if repo.Config != nil {
		ignore, _ := field(field(repo.Config.Value, "audit"), "ignore").([]any)
		for _, item := range ignore {
			plugin, _ := field(item, "plugin").(string)
			instance, _ := field(item, "instance").(string)
			w := add(item, plugin+"/"+instance, repo.Config.File)
			if match := field(item, "match"); match != nil {
				data, _ := json.Marshal(match)
				var id observation.Identity
				json.Unmarshal(data, &id)
				id = id.Normalized()
				w.identity, w.plugin, w.instance = &id, plugin, instance
				w.target += "/" + id.String()
			}
		}
	}
	for _, r := range repo.Records {
		list, _ := field(field(r.Value, "metadata"), "waivers").([]any)
		for _, item := range list {
			if r.Archived() && field(item, "rule") != "zombie" {
				continue
			}
			add(item, r.Kind+"/"+r.Name, r.File)
		}
	}
	return entries
}

// Run 运行规则并应用豁免；生命周期规则须先 Prepare。仅检查本次运行规则的豁免。
func Run(in *Input, rules []Rule) ([]Finding, []Notice) {
	findings := []Finding{}
	ran := map[string]bool{}
	for _, r := range rules {
		ran[r.ID] = true
		findings = append(findings, r.Eval(in)...)
	}
	for index := range findings {
		findings[index].Issues = issueIdentities(in, findings[index])
	}
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Rule != findings[j].Rule {
			return findings[i].Rule < findings[j].Rule
		}
		return findings[i].Target < findings[j].Target
	})
	year, month, day := in.Now.Date()
	today := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	notices := []Notice{}
	if ran["cdn_drift"] {
		notices = append(notices, in.cdnNotices...)
	}
	if ran["origin_exposure"] {
		notices = append(notices, in.originNotices...)
	}
	if ran["missing"] {
		notices = append(notices, in.missingNotices...)
	}
	for _, w := range waivers(in.Repo) {
		if !ran[w.rule] {
			continue
		}
		notice := Notice{File: w.waiver.Source, Target: w.target, Rule: w.rule}
		if until, err := time.Parse(time.DateOnly, w.waiver.Until); err == nil && until.Before(today) {
			notice.Code, notice.Message = "waiver_expired", "豁免已于 "+w.waiver.Until+" 过期，审计重新报告"
			notices = append(notices, notice)
			continue
		}
		used := false
		for i := range findings {
			f := &findings[i]
			matches := f.Target == w.target
			if w.identity != nil {
				matches = f.identity != nil && *w.identity == *f.identity && w.plugin == f.plugin && w.instance == f.instance
			}
			if f.Rule == w.rule && matches && f.Waiver == nil {
				waiver := w.waiver
				f.Waiver, used = &waiver, true
			}
		}
		if !used {
			notice.Code, notice.Message = "waiver_unused", "豁免未命中任何偏差，可以删除"
			notices = append(notices, notice)
		}
	}
	return findings, notices
}

var staleState = Rule{
	ID:          "stale_state",
	Description: "快照观测时间超过上限或未知",
	Ready: func(in *Input) error {
		if in.MaxAge == nil {
			return errors.New("规则 stale_state 需要 --max-age")
		}
		return nil
	},
	Eval: func(in *Input) []Finding {
		findings := []Finding{}
		for _, s := range in.Snapshots {
			f := Finding{Rule: "stale_state", Target: s.Plugin + "/" + s.Instance, File: s.File,
				Evidence: map[string]any{"observed_at": nil, "age_days": nil, "max_age_days": *in.MaxAge}}
			age := s.AgeDays(in.Now)
			switch {
			case age == nil:
				f.Message = "没有同步记录，无法确认观测时间"
			// 先比较整天数，避免把很大的天数阈值乘成 time.Duration 时溢出。
			case *age > *in.MaxAge || *age == *in.MaxAge && in.Now.Sub(*s.ObservedAt)%(24*time.Hour) > 0:
				f.Message = fmt.Sprintf("观测已 %d 天未更新（上限 %d 天）", *age, *in.MaxAge)
				f.Evidence["observed_at"] = s.ObservedAt.UTC().Format(time.RFC3339)
				f.Evidence["age_days"] = *age
			default:
				continue
			}
			findings = append(findings, f)
		}
		return findings
	},
}

func field(v any, key string) any {
	m, _ := v.(map[string]any)
	return m[key]
}
