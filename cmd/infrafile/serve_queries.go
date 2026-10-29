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
	tools = append(tools, queryTool{"changes", "只读保存的问题变化", map[string]any{"type": "object", "additionalProperties": false}})
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
	return mcpCall(ctx, dir, tool, args, now)
}
