package audit

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/infrafile/infrafile/internal/ledger"
)

type AssetSummaries struct {
	Version    string         `json:"version"`
	AsOf       string         `json:"as_of"`
	MaxAgeDays *int           `json:"max_age_days"`
	Rows       []AssetSummary `json:"rows"`
}

type SummaryCoverage struct {
	Expected      int `json:"expected"`
	Checked       int `json:"checked"`
	Unknown       int `json:"unknown"`
	NotApplicable int `json:"not_applicable"`
}

type SummaryRisk struct {
	Confirmed int `json:"confirmed"`
	Unknown   int `json:"unknown"`
	Waived    int `json:"waived"`
}

type SummaryReason struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type AssetSummary struct {
	Key       string             `json:"key"`
	Kind      string             `json:"kind"`
	Name      string             `json:"name"`
	Labels    map[string]string  `json:"labels"`
	Archived  bool               `json:"archived"`
	OK        bool               `json:"ok"`
	Category  string             `json:"category"`
	Hardware  map[string]string  `json:"hardware,omitempty"`
	Coverage  SummaryCoverage    `json:"coverage"`
	Risk      SummaryRisk        `json:"risk"`
	Execution []InspectExecution `json:"execution"`
	Reason    SummaryReason      `json:"reason"`
	Evidence  []InspectEvidence  `json:"evidence"`
}

// SummarizeAssets uses the same scoped inspector as Inspect, with one input preparation.
func SummarizeAssets(ctx context.Context, repo *ledger.Repo, opts InspectOptions) (*AssetSummaries, error) {
	prepared, err := prepareInspection(ctx, repo, opts)
	if err != nil {
		return nil, err
	}
	// Match Inspect's strict history validation without copying history into each row.
	if _, err := ReadHistory(ctx, repo.Dir); err != nil {
		return nil, err
	}
	records, err := prepared.selectRecords(opts.Keys)
	if err != nil {
		return nil, err
	}
	result := &AssetSummaries{Version: "v1", AsOf: opts.Now.UTC().Format(time.RFC3339), MaxAgeDays: opts.MaxAgeDays, Rows: []AssetSummary{}}
	for _, record := range records {
		key := record.Kind + "/" + record.Name
		i, err := inspectPrepared(ctx, prepared, []string{key})
		if err != nil {
			return nil, err
		}
		report := i.report
		row := AssetSummary{Key: key, Kind: record.Kind, Name: record.Name, Labels: map[string]string{}, Archived: record.Archived(), OK: report.OK,
			Category: summaryCategory(report),
			Coverage: SummaryCoverage{report.Coverage.Expected, report.Coverage.Checked, report.Coverage.Unknown, report.Coverage.NotApplicable},
			Risk:     SummaryRisk{report.Risk.Confirmed, report.Risk.Unknown, report.Risk.Waived}, Execution: report.Execution, Reason: summaryReason(report), Evidence: []InspectEvidence{}}
		if record.Kind == "Server" {
			row.Hardware = map[string]string{}
			for _, key := range []string{"cpu", "memory", "disk", "os"} {
				if value, ok := field(field(field(record.Value, "spec"), "hardware"), key).(string); ok {
					row.Hardware[key] = value
				}
			}
		}
		labels, _ := field(field(record.Value, "metadata"), "labels").(map[string]any)
		for k, value := range labels {
			if text, ok := value.(string); ok {
				row.Labels[k] = text
			}
		}
		seen := map[string]bool{}
		for _, check := range report.Coverage.Checks {
			for _, evidence := range check.Evidence {
				encoded, _ := json.Marshal(evidence)
				if !seen[string(encoded)] {
					row.Evidence = append(row.Evidence, evidence)
					seen[string(encoded)] = true
				}
			}
		}
		result.Rows = append(result.Rows, row)
	}
	sort.Slice(result.Rows, func(a, b int) bool { return result.Rows[a].Key < result.Rows[b].Key })
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func summaryCategory(report *Inspection) string {
	if report.Risk.Confirmed > 0 {
		return "risk"
	}
	if report.OK {
		return "passed"
	}
	if report.Risk.Unknown > 0 || report.Risk.Waived > 0 {
		return "needs_attention"
	}
	for _, execution := range report.Execution {
		if execution.Status != "succeeded" || execution.Code != "checked" {
			return "needs_attention"
		}
	}
	for _, check := range report.Coverage.Checks {
		if check.Status != "not_applicable" && !(check.Status == "unknown" && check.Code == "unsupported") {
			return "needs_attention"
		}
	}
	return "no_checks"
}

func summaryReason(report *Inspection) SummaryReason {
	for _, finding := range report.Risk.Findings {
		if finding.Assessment == "confirmed" && finding.Waiver == nil {
			return SummaryReason{finding.Rule, finding.Message}
		}
	}
	for _, check := range report.Coverage.Checks {
		if check.Status != "checked" && check.Status != "not_applicable" {
			return SummaryReason{check.Code, check.Message}
		}
	}
	for _, finding := range report.Risk.Findings {
		if finding.Assessment == "unknown" {
			return SummaryReason{finding.Rule, finding.Message}
		}
	}
	for _, execution := range report.Execution {
		if execution.Code != "checked" || execution.Status != "succeeded" {
			return SummaryReason{execution.Code, "执行证据资格不足，待核实"}
		}
	}
	if report.OK {
		return SummaryReason{"passed", "当前配置范围内检查通过"}
	}
	if report.Coverage.Expected > 0 && report.Coverage.NotApplicable == report.Coverage.Expected {
		return SummaryReason{"not_applicable", "当前范围全部不适用，不能据此判为通过"}
	}
	return SummaryReason{"no_checks", "当前范围没有可证明通过的检查"}
}
