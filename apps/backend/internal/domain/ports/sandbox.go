// Copyright (c) MyPal contributors. See LICENSE for details.

package ports

import (
	"context"
	"time"
)

// SandboxBackend abstracts the underlying container/sandbox runtime.
type SandboxBackend interface {
	Create(ctx context.Context, cfg SandboxConfig) (*SandboxInstance, error)
	Execute(ctx context.Context, id string, cmd SandboxCommand) (*SandboxResult, error)
	Destroy(ctx context.Context, id string) error
	List(ctx context.Context) ([]SandboxInstance, error)
	Get(ctx context.Context, id string) (*SandboxInstance, error)
}

// Mount describes a host path to bind-mount into a sandbox container.
type Mount struct {
	HostPath      string `json:"host_path"`
	ContainerPath string `json:"container_path"`
	ReadOnly      bool   `json:"read_only"`
}

// SandboxConfig describes the desired sandbox environment.
type SandboxConfig struct {
	Image      string
	Packages   []string
	Mounts     []Mount
	MemLimit   int64         // bytes
	CPULimit   float64       // cores
	Timeout    time.Duration
	NetPolicy  string // "none", "restricted", "full"
	Persistent bool
	UserID     string
}

// SandboxCommand is a single command to execute inside a sandbox.
type SandboxCommand struct {
	Cmd     string
	Stdin   string
	Env     map[string]string
	WorkDir string
	Timeout time.Duration
}

// SandboxInstance represents a running or stopped sandbox.
type SandboxInstance struct {
	ID         string
	Image      string
	Status     string // "running", "stopped", "creating"
	UserID     string
	CreatedAt  time.Time
	MemLimit   int64
	CPULimit   float64
	NetPolicy  string
	Persistent bool
}

// SandboxResult holds the output of an executed command.
type SandboxResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Duration time.Duration
}
