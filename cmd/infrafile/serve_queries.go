package main

import (
	"context"
	"time"

	"github.com/infrafile/infrafile/internal/audit"
)

func workbenchQueries() ([]queryTool, error) {
	tools, err := queryTools()
	if err != nil {
		return nil, err
	}
	var contextSchema map[string]any
	for _, tool := range tools {
		if tool.name == "inspect" {
			contextSchema = tool.schema
		}
	}
	contextSchema = map[string]any{"type": "object", "additionalProperties": false, "required": []string{"key"}, "properties": map[string]any{"key": contextSchema["properties"].(map[string]any)["key"], "max_age_days": map[string]any{"type": "integer", "minimum": 0}}}
	tools = append(tools, queryTool{"context", "读取单个资产的证据上下文", contextSchema}, queryTool{"changes", "只读保存的问题变化", map[string]any{"type": "object", "additionalProperties": false}})
	return tools, nil
}

func workbenchCall(ctx context.Context, dir, tool string, args mcpArgs, now time.Time) (any, error) {
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
	if tool == "context" {
		repo, err := loadQueryRepo(ctx, dir)
		if err != nil {
			return nil, err
		}
		value, err := audit.Context(ctx, repo, audit.InspectOptions{Now: now, MaxAgeDays: args.MaxAgeDays, Keys: []string{args.Key}})
		if err != nil {
			return nil, err
		}
		return queryValue(value)
	}
	return mcpCall(ctx, dir, tool, args, now)
}
