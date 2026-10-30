package main

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/infrafile/infrafile/internal/audit"
	"github.com/infrafile/infrafile/internal/control"
)

func workbenchQueries() ([]queryTool, error) {
	tools, err := queryTools()
	if err != nil {
		return nil, err
	}
	for _, tool := range tools {
		if tool.name == "inspect" {
			tools = append(tools, queryTool{"asset-summaries", "只读单资产巡检摘要；同一请求复用已准备输入", tool.schema})
			break
		}
	}
	tools = append(tools, queryTool{"changes", "只读保存的问题变化", map[string]any{"type": "object", "additionalProperties": false}})
	tools = append(tools,
		queryTool{"certificates", "只读保存的 TLS 证书元数据与应用归属", map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"max_age_days": map[string]any{"type": "integer", "minimum": 0}}}},
		queryTool{"executions", "只读执行回执及所绑定计划的索引", map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"key": map[string]any{"type": "string", "pattern": `^Resource/[a-z0-9][a-z0-9.-]*$`, "maxLength": 262}}}},
		queryTool{"execution", "只读一份完整执行回执和计划", map[string]any{"type": "object", "additionalProperties": false, "required": []string{"id"}, "properties": map[string]any{"id": map[string]any{"type": "string", "pattern": `^[a-f0-9]{64}$`}}}},
	)
	return tools, nil
}

func workbenchCall(ctx context.Context, dir, tool string, args mcpArgs, now time.Time) (any, error) {
	if tool == "certificates" {
		return workbenchCertificates(ctx, dir, args.MaxAgeDays, now)
	}
	if tool == "asset-summaries" {
		// Keep all ledger/state reads on one published directory generation, like mcpCall.
		pinned, err := filepath.EvalSymlinks(dir)
		if err != nil {
			return nil, err
		}
		repo, err := loadQueryRepo(ctx, pinned)
		if err != nil {
			return nil, err
		}
		matches := mcpSelect(repo, args, false)
		if args.Key != "" && len(matches) == 0 {
			return nil, fmt.Errorf("未找到目标")
		}
		keys := make([]string, 0, len(matches))
		for _, r := range matches {
			keys = append(keys, r.Kind+"/"+r.Name)
		}
		result, err := audit.SummarizeAssets(ctx, repo, audit.InspectOptions{Now: now, MaxAgeDays: args.MaxAgeDays, Keys: keys})
		if err != nil {
			return nil, err
		}
		return queryValue(result)
	}
	if tool == "executions" {
		r, err := control.ListExecutions(ctx, dir, args.Key)
		if err != nil {
			return nil, err
		}
		return queryValue(r)
	}
	if tool == "execution" {
		r, err := control.ReadReceipt(ctx, dir, args.ID)
		if err != nil {
			return nil, err
		}
		if r == nil {
			return nil, fmt.Errorf("没有此执行回执")
		}
		return queryValue(r)
	}
	if tool == "changes" {
		h, err := audit.ReadHistory(ctx, dir)
		if err != nil {
			return nil, err
		}
		var last, pruned uint64
		events := []audit.HistoryEvent{}
		if h != nil {
			last, pruned = h.Data.LastSequence, h.Data.PrunedThrough
			events, err = h.HistoryChanges(pruned)
			if err != nil {
				return nil, err
			}
		}
		return queryValue(map[string]any{"version": jsonVersion, "present": h != nil, "last_sequence": last, "pruned_through": pruned, "changes": events})
	}
	return mcpCall(ctx, dir, tool, args, now)
}
