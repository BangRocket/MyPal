// Copyright (c) MyPal contributors. See LICENSE for details.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// ---------------------------------------------------------------------------
// heartbeat_create
// ---------------------------------------------------------------------------

// HeartbeatCreateTool creates a new heartbeat item via the bot.
type HeartbeatCreateTool struct{ Tools InternalTools }

func (t *HeartbeatCreateTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "heartbeat_create",
		Description: "Create a new heartbeat item (recurring or one-shot reminder/check-in). Use when the user asks to be reminded about something or wants a recurring action.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"title":          {"type": "string", "description": "Short title for the heartbeat item"},
				"description":    {"type": "string", "description": "Detailed description of what this heartbeat should do"},
				"schedule":       {"type": "string", "description": "Cron expression (5-field) for recurring, ISO 8601 / RFC3339 datetime for one-shot, or empty for immediate"},
				"priority":       {"type": "integer", "description": "Priority 1 (lowest) to 5 (highest), default 3", "minimum": 1, "maximum": 5},
				"target_user":    {"type": "string", "description": "Display name or identifier of the user to notify"},
				"target_channel": {"type": "string", "description": "Channel to deliver the heartbeat through (e.g. telegram, discord)"},
				"context":        {"type": "string", "description": "Additional context the bot should have when executing this heartbeat"}
			},
			"required": ["title"]
		}`),
	}
}

func (t *HeartbeatCreateTool) Execute(ctx context.Context, params map[string]interface{}) (json.RawMessage, error) {
	if t.Tools.Heartbeat == nil {
		return nil, fmt.Errorf("heartbeat service unavailable")
	}

	title, _ := params["title"].(string)
	if title == "" {
		return nil, fmt.Errorf("title is required")
	}

	description, _ := params["description"].(string)
	schedule, _ := params["schedule"].(string)
	priority := 3
	if p, ok := params["priority"].(float64); ok {
		priority = int(p)
		if priority < 1 {
			priority = 1
		}
		if priority > 5 {
			priority = 5
		}
	}
	targetUser, _ := params["target_user"].(string)
	targetChannel, _ := params["target_channel"].(string)
	itemContext, _ := params["context"].(string)

	item := &HeartbeatCreateParams{
		Title:         title,
		Description:   description,
		Schedule:      schedule,
		Priority:      priority,
		TargetUser:    targetUser,
		TargetChannel: targetChannel,
		Context:       itemContext,
	}

	id, err := t.Tools.Heartbeat.BotCreate(ctx, item, "Created by bot during conversation")
	if err != nil {
		return nil, fmt.Errorf("heartbeat_create: %w", err)
	}

	return json.Marshal(map[string]string{
		"status": "created",
		"id":     id,
	})
}

// ---------------------------------------------------------------------------
// heartbeat_list
// ---------------------------------------------------------------------------

// HeartbeatListTool lists all active heartbeat items.
type HeartbeatListTool struct{ Tools InternalTools }

func (t *HeartbeatListTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "heartbeat_list",
		Description: "List all active heartbeat items (reminders, recurring check-ins, scheduled actions).",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {}
		}`),
	}
}

func (t *HeartbeatListTool) Execute(ctx context.Context, params map[string]interface{}) (json.RawMessage, error) {
	if t.Tools.Heartbeat == nil {
		return nil, fmt.Errorf("heartbeat service unavailable")
	}

	items, err := t.Tools.Heartbeat.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("heartbeat_list: %w", err)
	}

	return json.Marshal(map[string]interface{}{
		"count": len(items),
		"items": items,
	})
}

// ---------------------------------------------------------------------------
// heartbeat_complete
// ---------------------------------------------------------------------------

// HeartbeatCompleteTool marks a heartbeat item as completed.
type HeartbeatCompleteTool struct{ Tools InternalTools }

func (t *HeartbeatCompleteTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "heartbeat_complete",
		Description: "Mark a heartbeat item as completed. Use when the user confirms a reminder has been handled.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"id": {"type": "string", "description": "The heartbeat item ID to complete"}
			},
			"required": ["id"]
		}`),
	}
}

func (t *HeartbeatCompleteTool) Execute(ctx context.Context, params map[string]interface{}) (json.RawMessage, error) {
	if t.Tools.Heartbeat == nil {
		return nil, fmt.Errorf("heartbeat service unavailable")
	}

	id, _ := params["id"].(string)
	if id == "" {
		return nil, fmt.Errorf("id is required")
	}

	if err := t.Tools.Heartbeat.BotComplete(ctx, id, "Completed by bot"); err != nil {
		return nil, fmt.Errorf("heartbeat_complete: %w", err)
	}

	return json.Marshal(map[string]string{
		"status": "completed",
		"id":     id,
	})
}

// ---------------------------------------------------------------------------
// heartbeat_snooze
// ---------------------------------------------------------------------------

// HeartbeatSnoozeTool snoozes a heartbeat item until a specified time.
type HeartbeatSnoozeTool struct{ Tools InternalTools }

func (t *HeartbeatSnoozeTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "heartbeat_snooze",
		Description: "Snooze a heartbeat item until a specified time. The item will not fire until the snooze expires.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"id":    {"type": "string", "description": "The heartbeat item ID to snooze"},
				"until": {"type": "string", "description": "ISO 8601 / RFC3339 datetime to snooze until (e.g. 2026-03-20T09:00:00Z)"}
			},
			"required": ["id", "until"]
		}`),
	}
}

func (t *HeartbeatSnoozeTool) Execute(ctx context.Context, params map[string]interface{}) (json.RawMessage, error) {
	if t.Tools.Heartbeat == nil {
		return nil, fmt.Errorf("heartbeat service unavailable")
	}

	id, _ := params["id"].(string)
	if id == "" {
		return nil, fmt.Errorf("id is required")
	}

	untilStr, _ := params["until"].(string)
	if untilStr == "" {
		return nil, fmt.Errorf("until is required")
	}

	until, err := time.Parse(time.RFC3339, untilStr)
	if err != nil {
		return nil, fmt.Errorf("heartbeat_snooze: invalid until datetime %q: %w", untilStr, err)
	}

	if err := t.Tools.Heartbeat.BotSnooze(ctx, id, until, "Snoozed by bot"); err != nil {
		return nil, fmt.Errorf("heartbeat_snooze: %w", err)
	}

	return json.Marshal(map[string]string{
		"status": "snoozed",
		"id":     id,
		"until":  until.Format(time.RFC3339),
	})
}

// ---------------------------------------------------------------------------
// heartbeat_cancel
// ---------------------------------------------------------------------------

// HeartbeatCancelTool cancels a heartbeat item.
type HeartbeatCancelTool struct{ Tools InternalTools }

func (t *HeartbeatCancelTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "heartbeat_cancel",
		Description: "Cancel a heartbeat item. Use when the user no longer needs a reminder or recurring action.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"id": {"type": "string", "description": "The heartbeat item ID to cancel"}
			},
			"required": ["id"]
		}`),
	}
}

func (t *HeartbeatCancelTool) Execute(ctx context.Context, params map[string]interface{}) (json.RawMessage, error) {
	if t.Tools.Heartbeat == nil {
		return nil, fmt.Errorf("heartbeat service unavailable")
	}

	id, _ := params["id"].(string)
	if id == "" {
		return nil, fmt.Errorf("id is required")
	}

	if err := t.Tools.Heartbeat.Cancel(ctx, id); err != nil {
		return nil, fmt.Errorf("heartbeat_cancel: %w", err)
	}

	return json.Marshal(map[string]string{
		"status": "cancelled",
		"id":     id,
	})
}
