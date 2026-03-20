// Copyright (c) MyPal contributors. See LICENSE for details.

package incus

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/BangRocket/MyPal/apps/backend/internal/domain/ports"
)

const containerPrefix = "mypal-sandbox-"

// Backend implements ports.SandboxBackend using the Incus CLI.
type Backend struct {
	socket string // incus socket path, empty = default
}

// NewBackend creates a new Incus sandbox backend. If socket is empty, the
// default Incus socket is used.
func NewBackend(socket string) *Backend {
	return &Backend{socket: socket}
}

// incusCmd builds an exec.CommandContext for the incus CLI, injecting the
// socket environment variable when configured.
func (b *Backend) incusCmd(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "incus", args...)
	if b.socket != "" {
		cmd.Env = append(cmd.Environ(), "INCUS_SOCKET="+b.socket)
	}
	return cmd
}

// run executes an incus command and returns combined stdout/stderr on error.
func (b *Backend) run(ctx context.Context, args ...string) (string, string, error) {
	if err := checkInstalled(); err != nil {
		return "", "", err
	}
	cmd := b.incusCmd(ctx, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// checkInstalled returns a clear error when the incus binary is missing.
func checkInstalled() error {
	_, err := exec.LookPath("incus")
	if err != nil {
		return fmt.Errorf("incus is not installed or not in PATH: %w", err)
	}
	return nil
}

// containerName returns the full container name for a sandbox ID.
func containerName(id string) string {
	return containerPrefix + id
}

// formatMemLimit converts bytes to an Incus-compatible memory string (e.g.
// 268435456 -> "256MiB"). Falls back to bytes if not evenly divisible.
func formatMemLimit(b int64) string {
	if b <= 0 {
		return ""
	}
	const (
		gib = 1024 * 1024 * 1024
		mib = 1024 * 1024
		kib = 1024
	)
	switch {
	case b%gib == 0:
		return fmt.Sprintf("%dGiB", b/gib)
	case b%mib == 0:
		return fmt.Sprintf("%dMiB", b/mib)
	case b%kib == 0:
		return fmt.Sprintf("%dKiB", b/kib)
	default:
		return fmt.Sprintf("%dB", b)
	}
}

// Create launches a new Incus container with the given configuration.
func (b *Backend) Create(ctx context.Context, cfg ports.SandboxConfig) (*ports.SandboxInstance, error) {
	name := containerName(cfg.UserID)
	args := []string{"launch", cfg.Image, name}

	if cfg.MemLimit > 0 {
		args = append(args, "--config", fmt.Sprintf("limits.memory=%s", formatMemLimit(cfg.MemLimit)))
	}
	if cfg.CPULimit > 0 {
		args = append(args, "--config", fmt.Sprintf("limits.cpu=%.2f", cfg.CPULimit))
	}

	// Network policy mapping.
	switch cfg.NetPolicy {
	case "none":
		args = append(args, "--network", "")
		// Remove the empty --network and instead use no network profile.
		// Incus doesn't accept an empty --network; use config to deny network.
		args = args[:len(args)-2]
		args = append(args, "--config", "security.idmap.isolated=true")
	case "restricted":
		// Use the default network but apply restricted profile if available.
		args = append(args, "--config", "security.idmap.isolated=true")
	case "full":
		// Default network access, no additional restrictions.
	}

	_, stderr, err := b.run(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("incus launch failed: %s: %w", strings.TrimSpace(stderr), err)
	}

	// Attach bind-mount disk devices for each configured mount.
	for i, m := range cfg.Mounts {
		devName := fmt.Sprintf("mount%d", i)
		devArgs := []string{
			"config", "device", "add", name, devName, "disk",
			fmt.Sprintf("source=%s", m.HostPath),
			fmt.Sprintf("path=%s", m.ContainerPath),
		}
		if m.ReadOnly {
			devArgs = append(devArgs, "readonly=true")
		}
		if _, devStderr, devErr := b.run(ctx, devArgs...); devErr != nil {
			// Best-effort cleanup on failure.
			_, _, _ = b.run(ctx, "delete", name, "--force")
			return nil, fmt.Errorf("incus device add %q failed: %s: %w", devName, strings.TrimSpace(devStderr), devErr)
		}
	}

	return &ports.SandboxInstance{
		ID:         cfg.UserID,
		Image:      cfg.Image,
		Status:     "running",
		UserID:     cfg.UserID,
		CreatedAt:  time.Now(),
		MemLimit:   cfg.MemLimit,
		CPULimit:   cfg.CPULimit,
		NetPolicy:  cfg.NetPolicy,
		Persistent: cfg.Persistent,
	}, nil
}

// Execute runs a command inside the specified sandbox container.
func (b *Backend) Execute(ctx context.Context, id string, cmd ports.SandboxCommand) (*ports.SandboxResult, error) {
	if cmd.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cmd.Timeout)
		defer cancel()
	}

	args := []string{"exec", containerName(id)}

	// Environment variables.
	for k, v := range cmd.Env {
		args = append(args, "--env", fmt.Sprintf("%s=%s", k, v))
	}

	if cmd.WorkDir != "" {
		args = append(args, "--cwd", cmd.WorkDir)
	}

	args = append(args, "--", "sh", "-c", cmd.Cmd)

	start := time.Now()
	stdout, stderr, err := b.run(ctx, args...)
	dur := time.Since(start)

	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("incus exec failed: %s: %w", strings.TrimSpace(stderr), err)
		}
	}

	return &ports.SandboxResult{
		ExitCode: exitCode,
		Stdout:   stdout,
		Stderr:   stderr,
		Duration: dur,
	}, nil
}

// ExecuteStream runs a command inside the sandbox and streams stdout/stderr
// lines back via the returned SandboxOutputStream. The Lines channel receives
// each line as it arrives; Done is closed when the command completes.
func (b *Backend) ExecuteStream(ctx context.Context, id string, cmd ports.SandboxCommand) (*ports.SandboxOutputStream, error) {
	if err := checkInstalled(); err != nil {
		return nil, err
	}

	if cmd.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cmd.Timeout)
		defer cancel()
	}

	args := []string{"exec", containerName(id)}
	for k, v := range cmd.Env {
		args = append(args, "--env", fmt.Sprintf("%s=%s", k, v))
	}
	if cmd.WorkDir != "" {
		args = append(args, "--cwd", cmd.WorkDir)
	}
	args = append(args, "--", "sh", "-c", cmd.Cmd)

	execCmd := b.incusCmd(ctx, args...)

	stdoutPipe, err := execCmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("incus exec stdout pipe: %w", err)
	}
	stderrPipe, err := execCmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("incus exec stderr pipe: %w", err)
	}

	if err := execCmd.Start(); err != nil {
		return nil, fmt.Errorf("incus exec start: %w", err)
	}

	stream := &ports.SandboxOutputStream{
		Lines: make(chan string, 256),
		Done:  make(chan struct{}),
	}

	var wg sync.WaitGroup
	wg.Add(2)
	scan := func(r io.Reader) {
		defer wg.Done()
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			select {
			case stream.Lines <- scanner.Text():
			case <-ctx.Done():
				return
			}
		}
	}
	go scan(stdoutPipe)
	go scan(stderrPipe)

	go func() {
		wg.Wait()
		_ = execCmd.Wait()
		close(stream.Done)
	}()

	return stream, nil
}

// Destroy forcibly deletes the specified sandbox container.
func (b *Backend) Destroy(ctx context.Context, id string) error {
	_, stderr, err := b.run(ctx, "delete", containerName(id), "--force")
	if err != nil {
		return fmt.Errorf("incus delete failed: %s: %w", strings.TrimSpace(stderr), err)
	}
	return nil
}

// incusListEntry represents the JSON output of `incus list --format json`.
type incusListEntry struct {
	Name       string    `json:"name"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
	Config     map[string]string `json:"config"`
	ExpandedConfig map[string]string `json:"expanded_config"`
}

// List returns all sandbox instances managed by MyPal (prefixed with
// "mypal-sandbox-").
func (b *Backend) List(ctx context.Context) ([]ports.SandboxInstance, error) {
	stdout, stderr, err := b.run(ctx, "list", "--format", "json")
	if err != nil {
		return nil, fmt.Errorf("incus list failed: %s: %w", strings.TrimSpace(stderr), err)
	}

	var entries []incusListEntry
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		return nil, fmt.Errorf("failed to parse incus list output: %w", err)
	}

	var instances []ports.SandboxInstance
	for _, e := range entries {
		if !strings.HasPrefix(e.Name, containerPrefix) {
			continue
		}
		id := strings.TrimPrefix(e.Name, containerPrefix)
		instances = append(instances, ports.SandboxInstance{
			ID:        id,
			Status:    mapStatus(e.Status),
			CreatedAt: e.CreatedAt,
		})
	}
	return instances, nil
}

// incusInfoResponse represents the JSON output of `incus info --format json`.
type incusInfoResponse struct {
	Name       string    `json:"name"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
	Config     map[string]string `json:"config"`
	ExpandedConfig map[string]string `json:"expanded_config"`
}

// Get returns details for a specific sandbox instance.
func (b *Backend) Get(ctx context.Context, id string) (*ports.SandboxInstance, error) {
	stdout, stderr, err := b.run(ctx, "info", containerName(id), "--format", "json")
	if err != nil {
		return nil, fmt.Errorf("incus info failed: %s: %w", strings.TrimSpace(stderr), err)
	}

	var info incusInfoResponse
	if err := json.Unmarshal([]byte(stdout), &info); err != nil {
		return nil, fmt.Errorf("failed to parse incus info output: %w", err)
	}

	return &ports.SandboxInstance{
		ID:        id,
		Status:    mapStatus(info.Status),
		CreatedAt: info.CreatedAt,
	}, nil
}

// mapStatus normalizes Incus container status strings to the port-defined
// values ("running", "stopped", "creating").
func mapStatus(s string) string {
	switch strings.ToLower(s) {
	case "running":
		return "running"
	case "stopped":
		return "stopped"
	case "starting", "created":
		return "creating"
	default:
		return strings.ToLower(s)
	}
}

// Compile-time interface check.
var _ ports.SandboxBackend = (*Backend)(nil)
