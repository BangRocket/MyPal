// Copyright (c) MyPal contributors. See LICENSE for details.

// Package sandbox implements the sandbox Manager which orchestrates
// container lifecycle, command execution, and pre-warmed pool management.
package sandbox

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/BangRocket/MyPal/apps/backend/internal/domain/ports"
)

// poolEntry tracks a single pre-warmed container in the pool.
type poolEntry struct {
	ID    string
	Image string
}

// Manager coordinates sandbox lifecycle through a SandboxBackend and maintains
// an optional pool of pre-warmed instances.
type Manager struct {
	backend    ports.SandboxBackend
	timeout    time.Duration
	memDefault int64
	cpuDefault float64
	netDefault string
	poolSize   int
	poolMu     sync.Mutex
	pool       []poolEntry
}

// NewManager creates a Manager with the given backend and default resource
// limits. poolSize controls how many pre-warmed instances WarmPool will
// create per image.
func NewManager(
	backend ports.SandboxBackend,
	timeout time.Duration,
	memDefault int64,
	cpuDefault float64,
	netDefault string,
	poolSize int,
) *Manager {
	return &Manager{
		backend:    backend,
		timeout:    timeout,
		memDefault: memDefault,
		cpuDefault: cpuDefault,
		netDefault: netDefault,
		poolSize:   poolSize,
	}
}

// CreateSandbox creates a new sandbox, filling zero-value config fields from
// the manager's defaults. If a pre-warmed container matching the requested
// image is available in the pool it is claimed instead of creating fresh.
func (m *Manager) CreateSandbox(ctx context.Context, userID string, cfg ports.SandboxConfig) (*ports.SandboxInstance, error) {
	if cfg.Image != "" {
		inst, err := m.ClaimFromPool(ctx, userID, cfg.Image)
		if err != nil {
			return nil, err
		}
		if inst != nil {
			return inst, nil
		}
	}
	cfg.UserID = userID
	m.applyDefaults(&cfg)
	return m.backend.Create(ctx, cfg)
}

// Execute runs a command inside an existing sandbox. If the command has no
// timeout set, the manager's default timeout is applied.
func (m *Manager) Execute(ctx context.Context, sandboxID string, cmd ports.SandboxCommand) (*ports.SandboxResult, error) {
	if cmd.Timeout == 0 {
		cmd.Timeout = m.timeout
	}
	execCtx, cancel := context.WithTimeout(ctx, cmd.Timeout)
	defer cancel()
	return m.backend.Execute(execCtx, sandboxID, cmd)
}

// RunOnce creates an ephemeral sandbox, executes a single command, and
// destroys the sandbox regardless of the outcome.
func (m *Manager) RunOnce(ctx context.Context, userID, image, cmd string) (*ports.SandboxResult, error) {
	inst, err := m.CreateSandbox(ctx, userID, ports.SandboxConfig{
		Image:      image,
		Persistent: false,
	})
	if err != nil {
		return nil, fmt.Errorf("sandbox create: %w", err)
	}
	defer func() {
		// Best-effort cleanup; use background context so cancellation of the
		// caller does not prevent teardown.
		_ = m.backend.Destroy(context.Background(), inst.ID)
	}()

	return m.Execute(ctx, inst.ID, ports.SandboxCommand{Cmd: cmd})
}

// DestroySandbox tears down the sandbox identified by id.
func (m *Manager) DestroySandbox(ctx context.Context, id string) error {
	return m.backend.Destroy(ctx, id)
}

// ListSandboxes returns every sandbox known to the backend.
func (m *Manager) ListSandboxes(ctx context.Context) ([]ports.SandboxInstance, error) {
	return m.backend.List(ctx)
}

// ListUserSandboxes returns only the sandboxes belonging to the given user.
func (m *Manager) ListUserSandboxes(ctx context.Context, userID string) ([]ports.SandboxInstance, error) {
	all, err := m.backend.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []ports.SandboxInstance
	for _, s := range all {
		if s.UserID == userID {
			out = append(out, s)
		}
	}
	return out, nil
}

// GetSandbox retrieves a single sandbox by ID.
func (m *Manager) GetSandbox(ctx context.Context, id string) (*ports.SandboxInstance, error) {
	return m.backend.Get(ctx, id)
}

// WarmPool pre-creates count containers for the given image.
func (m *Manager) WarmPool(ctx context.Context, image string, count int) error {
	for i := 0; i < count; i++ {
		instance, err := m.backend.Create(ctx, ports.SandboxConfig{
			Image:     image,
			MemLimit:  m.memDefault,
			CPULimit:  m.cpuDefault,
			NetPolicy: m.netDefault,
			UserID:    "__pool__",
		})
		if err != nil {
			return err
		}
		m.poolMu.Lock()
		m.pool = append(m.pool, poolEntry{ID: instance.ID, Image: image})
		m.poolMu.Unlock()
	}
	return nil
}

// ClaimFromPool takes a pre-warmed container from the pool for the given user and image.
// Returns nil if no matching container is available.
func (m *Manager) ClaimFromPool(ctx context.Context, userID, image string) (*ports.SandboxInstance, error) {
	m.poolMu.Lock()
	for i, entry := range m.pool {
		if entry.Image == image {
			m.pool = append(m.pool[:i], m.pool[i+1:]...)
			m.poolMu.Unlock()
			// Retrieve the instance from the backend and reassign ownership.
			inst, err := m.backend.Get(ctx, entry.ID)
			if err != nil {
				return nil, err
			}
			inst.UserID = userID
			return inst, nil
		}
	}
	m.poolMu.Unlock()
	return nil, nil // no match, caller should create fresh
}

// applyDefaults fills zero-value fields in cfg with the manager's defaults.
func (m *Manager) applyDefaults(cfg *ports.SandboxConfig) {
	if cfg.MemLimit == 0 {
		cfg.MemLimit = m.memDefault
	}
	if cfg.CPULimit == 0 {
		cfg.CPULimit = m.cpuDefault
	}
	if cfg.NetPolicy == "" {
		cfg.NetPolicy = m.netDefault
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = m.timeout
	}
}
