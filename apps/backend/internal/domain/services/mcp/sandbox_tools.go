// Copyright (c) MyPal contributors. See LICENSE for details.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/BangRocket/MyPal/apps/backend/internal/domain/ports"
)

// ---------------------------------------------------------------------------
// sandbox_execute
// ---------------------------------------------------------------------------

// SandboxExecuteTool runs code in an ephemeral sandbox.
type SandboxExecuteTool struct{ Tools InternalTools }

func (t *SandboxExecuteTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "sandbox_execute",
		Description: "Run a command in an ephemeral sandbox container. The sandbox is created, the command executes, and the sandbox is destroyed automatically. Use this for one-off code execution.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"image":   {"type": "string", "description": "Container image to use (e.g. python:3.12, node:20, ubuntu:22.04)"},
				"command": {"type": "string", "description": "Shell command to execute inside the sandbox"},
				"timeout": {"type": "integer", "description": "Maximum execution time in seconds (optional, uses server default if omitted)"}
			},
			"required": ["image", "command"]
		}`),
	}
}

func (t *SandboxExecuteTool) Execute(ctx context.Context, params map[string]interface{}) (json.RawMessage, error) {
	if t.Tools.Sandbox == nil {
		return nil, fmt.Errorf("sandbox service unavailable")
	}

	image, _ := params["image"].(string)
	if image == "" {
		return nil, fmt.Errorf("image is required")
	}
	command, _ := params["command"].(string)
	if command == "" {
		return nil, fmt.Errorf("command is required")
	}

	userID, _ := ctx.Value(ContextKeyUserID).(string)

	if timeoutSec, ok := params["timeout"].(float64); ok && timeoutSec > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
		defer cancel()
	}

	result, err := t.Tools.Sandbox.RunOnce(ctx, userID, image, command)
	if err != nil {
		return nil, fmt.Errorf("sandbox_execute: %w", err)
	}

	return json.Marshal(map[string]interface{}{
		"stdout":    result.Stdout,
		"stderr":    result.Stderr,
		"exit_code": result.ExitCode,
	})
}

// ---------------------------------------------------------------------------
// sandbox_create
// ---------------------------------------------------------------------------

// SandboxCreateTool creates a persistent sandbox.
type SandboxCreateTool struct{ Tools InternalTools }

func (t *SandboxCreateTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "sandbox_create",
		Description: "Create a persistent sandbox container that stays running for multiple commands. Use this when you need to install packages, build state incrementally, or run a series of related commands.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"image":    {"type": "string", "description": "Container image to use (e.g. python:3.12, node:20, ubuntu:22.04)"},
				"packages": {"type": "array", "items": {"type": "string"}, "description": "Packages to pre-install in the sandbox (optional)"}
			},
			"required": ["image"]
		}`),
	}
}

func (t *SandboxCreateTool) Execute(ctx context.Context, params map[string]interface{}) (json.RawMessage, error) {
	if t.Tools.Sandbox == nil {
		return nil, fmt.Errorf("sandbox service unavailable")
	}

	image, _ := params["image"].(string)
	if image == "" {
		return nil, fmt.Errorf("image is required")
	}

	var packages []string
	if raw, ok := params["packages"].([]interface{}); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok {
				packages = append(packages, s)
			}
		}
	}

	userID, _ := ctx.Value(ContextKeyUserID).(string)

	cfg := ports.SandboxConfig{
		Image:      image,
		Packages:   packages,
		Persistent: true,
	}

	inst, err := t.Tools.Sandbox.CreateSandbox(ctx, userID, cfg)
	if err != nil {
		return nil, fmt.Errorf("sandbox_create: %w", err)
	}

	return json.Marshal(map[string]interface{}{
		"status":     "created",
		"id":         inst.ID,
		"image":      inst.Image,
		"persistent": inst.Persistent,
	})
}

// ---------------------------------------------------------------------------
// sandbox_run
// ---------------------------------------------------------------------------

// SandboxRunTool executes a command in an existing sandbox.
type SandboxRunTool struct{ Tools InternalTools }

func (t *SandboxRunTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "sandbox_run",
		Description: "Execute a command in an existing persistent sandbox. Use after sandbox_create to run commands in a sandbox that retains state between calls.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"id":      {"type": "string", "description": "The sandbox ID returned by sandbox_create"},
				"command": {"type": "string", "description": "Shell command to execute inside the sandbox"}
			},
			"required": ["id", "command"]
		}`),
	}
}

func (t *SandboxRunTool) Execute(ctx context.Context, params map[string]interface{}) (json.RawMessage, error) {
	if t.Tools.Sandbox == nil {
		return nil, fmt.Errorf("sandbox service unavailable")
	}

	id, _ := params["id"].(string)
	if id == "" {
		return nil, fmt.Errorf("id is required")
	}
	command, _ := params["command"].(string)
	if command == "" {
		return nil, fmt.Errorf("command is required")
	}

	cmd := ports.SandboxCommand{Cmd: command}

	result, err := t.Tools.Sandbox.Execute(ctx, id, cmd)
	if err != nil {
		return nil, fmt.Errorf("sandbox_run: %w", err)
	}

	return json.Marshal(map[string]interface{}{
		"stdout":    result.Stdout,
		"stderr":    result.Stderr,
		"exit_code": result.ExitCode,
	})
}

// ---------------------------------------------------------------------------
// sandbox_list
// ---------------------------------------------------------------------------

// SandboxListTool lists the current user's sandboxes.
type SandboxListTool struct{ Tools InternalTools }

func (t *SandboxListTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "sandbox_list",
		Description: "List all sandboxes belonging to the current user, including their IDs, images, and status.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {}
		}`),
	}
}

func (t *SandboxListTool) Execute(ctx context.Context, params map[string]interface{}) (json.RawMessage, error) {
	if t.Tools.Sandbox == nil {
		return nil, fmt.Errorf("sandbox service unavailable")
	}

	userID, _ := ctx.Value(ContextKeyUserID).(string)

	sandboxes, err := t.Tools.Sandbox.ListUserSandboxes(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("sandbox_list: %w", err)
	}

	return json.Marshal(map[string]interface{}{
		"count":     len(sandboxes),
		"sandboxes": sandboxes,
	})
}

// ---------------------------------------------------------------------------
// sandbox_destroy
// ---------------------------------------------------------------------------

// SandboxDestroyTool destroys a sandbox.
type SandboxDestroyTool struct{ Tools InternalTools }

func (t *SandboxDestroyTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "sandbox_destroy",
		Description: "Destroy a sandbox container, releasing all resources. Use when done with a persistent sandbox.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"id": {"type": "string", "description": "The sandbox ID to destroy"}
			},
			"required": ["id"]
		}`),
	}
}

func (t *SandboxDestroyTool) Execute(ctx context.Context, params map[string]interface{}) (json.RawMessage, error) {
	if t.Tools.Sandbox == nil {
		return nil, fmt.Errorf("sandbox service unavailable")
	}

	id, _ := params["id"].(string)
	if id == "" {
		return nil, fmt.Errorf("id is required")
	}

	if err := t.Tools.Sandbox.DestroySandbox(ctx, id); err != nil {
		return nil, fmt.Errorf("sandbox_destroy: %w", err)
	}

	return json.Marshal(map[string]string{
		"status": "destroyed",
		"id":     id,
	})
}
