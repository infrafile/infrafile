package main

import (
	"context"
	"fmt"
	"time"

	"github.com/infrafile/infrafile/internal/audit"
	"github.com/infrafile/infrafile/internal/control"
)

func workbenchQueries() ([]queryTool, error) {
	tools, err := queryTools()
	if err != nil {
		return nil, err
	}
	tools = append(tools, queryTool{"changes", "只读保存的问题变化", map[string]any{"type": "object", "additionalProperties": false}})
	tools = append(tools,
		queryTool{"executions", "只读执行回执及所绑定计划的索引", map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"key": map[string]any{"type": "string", "pattern": `^Resource/[a-z0-9][a-z0-9.-]*$`, "maxLength": 262}}}},
		queryTool{"execution", "只读一份完整执行回执和计划", map[string]any{"type": "object", "additionalProperties": false, "required": []string{"id"}, "properties": map[string]any{"id": map[string]any{"type": "string", "pattern": `^[a-f0-9]{64}$`}}}},
	)
	return tools, nil
}

func workbenchCall(ctx context.Context, dir, tool string, args mcpArgs, now time.Time) (any, error) {
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
