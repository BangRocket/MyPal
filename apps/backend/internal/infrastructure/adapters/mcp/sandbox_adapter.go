// Copyright (c) MyPal contributors. See LICENSE for details.

package mcp

import (
	"context"
	"fmt"

	"github.com/BangRocket/MyPal/apps/backend/internal/domain/ports"
	"github.com/BangRocket/MyPal/apps/backend/internal/domain/services/sandbox"
)

// SandboxAdapter bridges the sandbox.Manager to the mcp.SandboxService
// interface expected by the internal tool layer.
type SandboxAdapter struct {
	Mgr *sandbox.Manager
}

func (a *SandboxAdapter) RunOnce(ctx context.Context, userID, image, cmd string) (*ports.SandboxResult, error) {
	if a.Mgr == nil {
		return nil, fmt.Errorf("sandbox: not configured")
	}
	return a.Mgr.RunOnce(ctx, userID, image, cmd)
}

func (a *SandboxAdapter) CreateSandbox(ctx context.Context, userID string, cfg ports.SandboxConfig) (*ports.SandboxInstance, error) {
	if a.Mgr == nil {
		return nil, fmt.Errorf("sandbox: not configured")
	}
	return a.Mgr.CreateSandbox(ctx, userID, cfg)
}

func (a *SandboxAdapter) Execute(ctx context.Context, sandboxID string, cmd ports.SandboxCommand) (*ports.SandboxResult, error) {
	if a.Mgr == nil {
		return nil, fmt.Errorf("sandbox: not configured")
	}
	return a.Mgr.Execute(ctx, sandboxID, cmd)
}

func (a *SandboxAdapter) ListUserSandboxes(ctx context.Context, userID string) ([]ports.SandboxInstance, error) {
	if a.Mgr == nil {
		return nil, fmt.Errorf("sandbox: not configured")
	}
	return a.Mgr.ListUserSandboxes(ctx, userID)
}

func (a *SandboxAdapter) DestroySandbox(ctx context.Context, id string) error {
	if a.Mgr == nil {
		return fmt.Errorf("sandbox: not configured")
	}
	return a.Mgr.DestroySandbox(ctx, id)
}
