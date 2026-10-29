package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"time"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/plugin"
	"github.com/infrafile/infrafile/internal/validate"
	"github.com/infrafile/infrafile/protocol"
)

const (
	HistoryFile          = ".findings.json"
	MaxHistoryIssues     = 512
	MaxHistoryChanges    = 1024
	maxHistoryBytes      = 4 << 20
	HistoryRetentionDays = 180
)

type HistoryInput struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
}

type HistoryEvent struct {
	Sequence uint64 `json:"sequence"`
	IssueChange
}

type FindingHistory struct {
	Version string      `json:"version"`
	Kind    string      `json:"kind"`
	Data    HistoryData `json:"data"`
}

type HistoryData struct {
	IdentityVersion string         `json:"identity_version"`
	InputHash       string         `json:"input_hash"`
	Inputs          []HistoryInput `json:"inputs"`
	MaxAgeDays      int            `json:"max_age_days"`
	CheckedAt       string         `json:"checked_at"`
	Issues          []IssueState   `json:"issues"`
	Changes         []HistoryEvent `json:"changes"`
	LastSequence    uint64         `json:"last_sequence"`
	PrunedThrough   uint64         `json:"pruned_through"`
}

type HistoryRefresh struct {
	Version string          `json:"version"`
	Written bool            `json:"written"`
	History *FindingHistory `json:"history"`
	Changes []HistoryEvent  `json:"changes"`
}

// HistoryView is the saved state as of CheckedAt, not the current inspection's result.
type HistoryView struct {
	CheckedAt     string       `json:"checked_at"`
	InputHash     string       `json:"input_hash"`
	Issues        []IssueState `json:"issues"`
	LastSequence  uint64       `json:"last_sequence"`
	PrunedThrough uint64       `json:"pruned_through"`
}

func ReadHistoryView(ctx context.Context, dir string, checks []Check) (*HistoryView, error) {
	h, err := ReadHistory(ctx, dir)
	if err != nil || h == nil {
		return nil, err
	}
	view := &HistoryView{CheckedAt: h.Data.CheckedAt, InputHash: h.Data.InputHash, Issues: []IssueState{}, LastSequence: h.Data.LastSequence, PrunedThrough: h.Data.PrunedThrough}
	for _, state := range h.Data.Issues {
		keep := checks == nil
		for _, check := range checks {
			keep = keep || check.Target == state.Identity.Key.Target || issueMatchesCheck(state.Identity.Key, check)
		}
		if keep {
			view.Issues = append(view.Issues, state)
		}
	}
	return view, nil
}

func historyInputHash(inputs []HistoryInput, days int) string {
	return plugin.Hash(struct {
		Files      []HistoryInput `json:"files"`
		MaxAgeDays int            `json:"max_age_days"`
	}{inputs, days})
}

func decodeHistory(ctx context.Context, data []byte) (*FindingHistory, error) {
	if len(data) > maxHistoryBytes || !json.Valid(data) {
		return nil, errors.New("问题历史不是有效 JSON 或超过 4 MiB；保留原文件")
	}
	doc, problems, err := ledger.ParseDocument(ctx, "finding-history", data)
	if err != nil {
		return nil, err
	}
	if doc == nil || len(problems) != 0 || protocol.Validate("record", data) != nil {
		return nil, errors.New("问题历史含重复键、多文档、未知信封或超出解析限制；保留原文件")
	}
	var h FindingHistory
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&h) != nil {
		return nil, errors.New("问题历史含未知字段或错误类型；保留原文件")
	}
	encoded, err := json.Marshal(h)
	if err != nil {
		return nil, errors.New("问题历史无法编码")
	}
	canonical, ps, err := ledger.ParseDocument(ctx, "finding-history", encoded)
	if err != nil {
		return nil, err
	}
	if canonical == nil || len(ps) != 0 || !reflect.DeepEqual(doc.Value, canonical.Value) {
		return nil, errors.New("问题历史缺少字段或字段名不规范；保留原文件")
	}
	if !validate.SnapshotSafe(doc.Value) {
		return nil, errors.New("问题历史含敏感字段或疑似凭据，已拒绝读取")
	}
	if err := h.validate(ctx); err != nil {
		return nil, err
	}
	return &h, nil
}

func (h *FindingHistory) validate(ctx context.Context) error {
	d := &h.Data
	if h.Version != "v1" || h.Kind != "FindingHistory" || d.IdentityVersion != IssueVersion || d.MaxAgeDays < 0 || d.Inputs == nil || d.Issues == nil || d.Changes == nil || len(d.Inputs) > maxHistoryInputFiles || len(d.Issues) > MaxHistoryIssues || len(d.Changes) > MaxHistoryChanges || d.PrunedThrough > d.LastSequence {
		return errors.New("问题历史版本、结构或容量不合法；请导出后审阅清理，活动问题不能裁掉")
	}
	at, err := time.Parse(time.RFC3339, d.CheckedAt)
	if err != nil || at.IsZero() || d.CheckedAt != at.UTC().Format(time.RFC3339) {
		return errors.New("问题历史检查时间不合法")
	}
	previous := ""
	for _, input := range d.Inputs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if input.Path <= previous || !safeHistoryInput(input.Path) || !validIssueDigest(input.Hash) {
			return errors.New("问题历史输入清单不合法")
		}
		previous = input.Path
	}
	if len(d.Inputs) == 0 || !validIssueDigest(d.InputHash) || historyInputHash(d.Inputs, d.MaxAgeDays) != d.InputHash {
		return errors.New("问题历史输入摘要不匹配")
	}
	issues := map[string]IssueState{}
	previous = ""
	for _, state := range d.Issues {
		if err := ctx.Err(); err != nil {
			return err
		}
		key := state.Identity.storageKey()
		if key <= previous {
			return errors.New("问题历史身份重复或未排序")
		}
		previous = key
		if err := validateIssueState(state, at); err != nil {
			return err
		}
		if state.LastChecked != d.CheckedAt {
			return errors.New("问题历史未完成同一时刻的完整求值")
		}
		if state.Lifecycle == "out_of_scope" && state.Assessment != "unknown" || state.Assessment == "clear" && state.Resume == "open" && state.Waiver == nil {
			return errors.New("问题历史资格与状态矛盾")
		}
		if state.Assessment == "confirmed" && (state.Resume != "open" || state.Occurrences == 0 || state.LastConfirmed != state.LastChecked) {
			return errors.New("问题历史确认事实不完整")
		}
		if state.Waiver != nil {
			if state.Waiver.Reason == "" || state.Waiver.Source == "" {
				return errors.New("问题历史豁免不完整")
			}
			if state.Waiver.Until != "" {
				if _, err := time.Parse(time.DateOnly, state.Waiver.Until); err != nil {
					return errors.New("问题历史豁免时间无效")
				}
			}
		}
		issues[key] = state
	}
	sequence, lastAt := d.PrunedThrough, time.Time{}
	for _, event := range d.Changes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if sequence == math.MaxUint64 || event.Sequence != sequence+1 {
			return errors.New("问题历史变化序号不连续")
		}
		sequence = event.Sequence
		if err := event.Identity.Validate(); err != nil {
			return err
		}
		if !contains([]string{"opened", "unconfirmed", "confirmed", "recovered", "reopened", "out_of_scope", "returned", "unknown", "clear", "waived", "unwaived"}, event.Type) {
			return errors.New("问题历史变化类型未知")
		}
		stamp, err := time.Parse(time.RFC3339, event.At)
		if err != nil || stamp.IsZero() || stamp.After(at) || stamp.Before(lastAt) || event.At != stamp.UTC().Format(time.RFC3339) {
			return errors.New("问题历史变化时间无效")
		}
		if state, exists := issues[event.Identity.storageKey()]; exists && event.At < state.FirstSeen {
			return errors.New("问题历史变化早于首次发现")
		}
		lastAt = stamp
	}
	if sequence != d.LastSequence {
		return errors.New("问题历史变化尾序号不匹配")
	}
	return nil
}

func (h *FindingHistory) Encode(ctx context.Context) ([]byte, error) {
	data, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	if _, err := decodeHistory(ctx, data); err != nil {
		return nil, err
	}
	return data, nil
}

// HistoryChanges rejects an expired cursor instead of silently skipping pruned notifications.
func (h *FindingHistory) HistoryChanges(after uint64) ([]HistoryEvent, error) {
	if after < h.Data.PrunedThrough || after > h.Data.LastSequence {
		return nil, errors.New("变化游标已被清理或超出历史，请先核对完整导出")
	}
	events := []HistoryEvent{}
	for _, e := range h.Data.Changes {
		if e.Sequence > after {
			events = append(events, e)
		}
	}
	return events, nil
}

func nextHistory(ctx context.Context, old *FindingHistory, inputs []HistoryInput, opts InspectOptions, assessment *IssueAssessment) (*HistoryRefresh, error) {
	at := opts.Now.UTC().Truncate(time.Second)
	h := &FindingHistory{Version: "v1", Kind: "FindingHistory", Data: HistoryData{IdentityVersion: IssueVersion, InputHash: historyInputHash(inputs, *opts.MaxAgeDays), Inputs: inputs, MaxAgeDays: *opts.MaxAgeDays, CheckedAt: at.Format(time.RFC3339), Issues: []IssueState{}, Changes: []HistoryEvent{}}}
	var previous []IssueState
	if old != nil {
		previous = old.Data.Issues
		h.Data.Changes = append(h.Data.Changes, old.Data.Changes...)
		h.Data.LastSequence, h.Data.PrunedThrough = old.Data.LastSequence, old.Data.PrunedThrough
		checked, _ := time.Parse(time.RFC3339, old.Data.CheckedAt)
		if at.Before(checked) {
			return nil, errors.New("检查时间倒退，保留原问题历史")
		}
	}
	states, changes, err := AdvanceIssues(ctx, previous, assessment.Evaluations, at)
	if err != nil {
		return nil, err
	}
	h.Data.Issues = states
	result := &HistoryRefresh{Version: "v1", History: h, Changes: []HistoryEvent{}}
	for _, change := range changes {
		if h.Data.LastSequence == math.MaxUint64 {
			return nil, errors.New("问题变化序号已达上限，保留原历史")
		}
		h.Data.LastSequence++
		event := HistoryEvent{h.Data.LastSequence, change}
		h.Data.Changes = append(h.Data.Changes, event)
		result.Changes = append(result.Changes, event)
	}
	if err := h.validate(ctx); err != nil {
		return nil, err
	}
	result.Written = !reflect.DeepEqual(old, h)
	return result, nil
}

func safeHistoryInput(path string) bool {
	if path != ledger.ConfigFile && !strings.HasPrefix(path, "inventory/") && !strings.HasPrefix(path, "state/") {
		return false
	}
	for _, part := range strings.Split(strings.TrimSuffix(path, "/"), "/") {
		if part == "" || part == "." || part == ".." || strings.Contains(part, "\\") {
			return false
		}
	}
	return path != "state/"+HistoryFile && path != "state/.sync.lock"
}

func pruneHistory(ctx context.Context, old *FindingHistory, before, now time.Time) (*FindingHistory, error) {
	if before.IsZero() || before.After(now) {
		return nil, errors.New("清理界限必须是过去的时间")
	}
	h := *old
	h.Data.Issues, h.Data.Changes = []IssueState{}, []HistoryEvent{}
	for _, state := range old.Data.Issues {
		recovered, _ := time.Parse(time.RFC3339, state.RecoveredAt)
		// An out-of-scope issue may still be unresolved; never prune it by age.
		if state.Lifecycle != "recovered" || !recovered.Before(before) {
			h.Data.Issues = append(h.Data.Issues, state)
		}
	}
	for _, event := range old.Data.Changes {
		at, _ := time.Parse(time.RFC3339, event.At)
		if at.Before(before) {
			h.Data.PrunedThrough = event.Sequence
		} else {
			h.Data.Changes = append(h.Data.Changes, event)
		}
	}
	if err := h.validate(ctx); err != nil {
		return nil, fmt.Errorf("清理结果无效: %w", err)
	}
	return &h, nil
}
