// Copyright (c) MyPal contributors. See LICENSE for details.

// Package sandbox implements the sandbox Manager which orchestrates
// container lifecycle, command execution, and pre-warmed pool management.
package sandbox

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/BangRocket/MyPal/apps/backend/internal/domain/ports"
)

// poolEntry tracks a single pre-warmed container in the pool.
type poolEntry struct {
	ID    string
	Image string
}

// StreamingExecution tracks a sandbox command that was spawned for streaming
// output via a poll-based pattern (sandbox_spawn + sandbox_get_output).
type StreamingExecution struct {
	ID          string
	SandboxID   string
	Command     string
	StartedAt   time.Time
	stream      *ports.SandboxOutputStream
	mu          sync.Mutex
	outputLines []string
	running     bool
	exitCode    int
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

	execMu     sync.Mutex
	executions map[string]*StreamingExecution
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
		executions: make(map[string]*StreamingExecution),
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

// ExecuteStream starts a streaming command in an existing sandbox and returns
// the raw SandboxOutputStream. If the command has no timeout set, the
// manager's default is applied.
func (m *Manager) ExecuteStream(ctx context.Context, sandboxID string, cmd ports.SandboxCommand) (*ports.SandboxOutputStream, error) {
	if cmd.Timeout == 0 {
		cmd.Timeout = m.timeout
	}
	execCtx, cancel := context.WithTimeout(ctx, cmd.Timeout)
	_ = cancel // kept alive by the background goroutine in the backend
	return m.backend.ExecuteStream(execCtx, sandboxID, cmd)
}

// SpawnExecution starts a streaming command and registers it for later polling
// via GetExecutionOutput. Returns the execution ID.
func (m *Manager) SpawnExecution(ctx context.Context, sandboxID string, cmd ports.SandboxCommand) (string, error) {
	stream, err := m.ExecuteStream(ctx, sandboxID, cmd)
	if err != nil {
		return "", err
	}

	execID := uuid.New().String()
	exec := &StreamingExecution{
		ID:        execID,
		SandboxID: sandboxID,
		Command:   cmd.Cmd,
		StartedAt: time.Now(),
		stream:    stream,
		running:   true,
	}

	m.execMu.Lock()
	m.executions[execID] = exec
	m.execMu.Unlock()

	// Background goroutine: drain lines from the stream into the buffer.
	go func() {
		for {
			select {
			case line, ok := <-stream.Lines:
				if !ok {
					// Channel closed; command finished.
					exec.mu.Lock()
					exec.running = false
					exec.mu.Unlock()
					return
				}
				exec.mu.Lock()
				exec.outputLines = append(exec.outputLines, line)
				exec.mu.Unlock()
			case <-stream.Done:
				// Drain any remaining lines.
				for {
					select {
					case line, ok := <-stream.Lines:
						if !ok {
							break
						}
						exec.mu.Lock()
						exec.outputLines = append(exec.outputLines, line)
						exec.mu.Unlock()
						continue
					default:
					}
					break
				}
				exec.mu.Lock()
				exec.running = false
				exec.mu.Unlock()
				return
			}
		}
	}()

	return execID, nil
}

// ExecutionOutput holds the current state of a streaming execution for polling.
type ExecutionOutput struct {
	Output   string
	Running  bool
	ExitCode int
}

// GetExecutionOutput returns the accumulated output and status of a spawned
// execution. If tail > 0, only the last tail lines are returned.
func (m *Manager) GetExecutionOutput(executionID string, tail int) (*ExecutionOutput, error) {
	m.execMu.Lock()
	exec, ok := m.executions[executionID]
	m.execMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("execution %q not found", executionID)
	}

	exec.mu.Lock()
	defer exec.mu.Unlock()

	lines := exec.outputLines
	if tail > 0 && tail < len(lines) {
		lines = lines[len(lines)-tail:]
	}

	return &ExecutionOutput{
		Output:   strings.Join(lines, "\n"),
		Running:  exec.running,
		ExitCode: exec.exitCode,
	}, nil
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
