// Copyright (c) MyPal contributors. See LICENSE for details.

package docker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/BangRocket/MyPal/apps/backend/internal/domain/ports"
)

// Backend implements ports.SandboxBackend using the Docker CLI.
type Backend struct {
	host string // docker host (DOCKER_HOST); empty = default socket
}

// NewBackend creates a Docker sandbox backend. Pass an empty host to use the
// default Docker socket.
func NewBackend(host string) *Backend {
	return &Backend{host: host}
}

// docker builds an exec.Cmd for a docker CLI invocation, respecting the
// configured host and the supplied context for cancellation/timeout.
func (b *Backend) docker(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "docker", args...)
	if b.host != "" {
		cmd.Env = append(cmd.Environ(), "DOCKER_HOST="+b.host)
	}
	return cmd
}

// containerName returns the deterministic container name for a sandbox ID.
func containerName(id string) string {
	return "mypal-sandbox-" + id
}

// networkFlag maps a NetPolicy string to the --network docker flag value.
// "none" and "restricted" both map to --network none (simplest for now).
// "full" (or anything else) uses the default bridge — no flag needed.
func networkFlag(policy string) string {
	switch policy {
	case "none", "restricted":
		return "none"
	default:
		return ""
	}
}

// Create provisions and starts a new sandbox container.
func (b *Backend) Create(ctx context.Context, cfg ports.SandboxConfig) (*ports.SandboxInstance, error) {
	id := uuid.New().String()
	name := containerName(id)

	image := cfg.Image
	if image == "" {
		image = "ubuntu:24.04"
	}

	// -- docker create -------------------------------------------------------
	args := []string{
		"create",
		"--name", name,
		"--label", "mypal.sandbox=true",
		"--label", "mypal.user=" + cfg.UserID,
		"--label", "mypal.net_policy=" + cfg.NetPolicy,
	}

	if cfg.MemLimit > 0 {
		args = append(args, "--memory", fmt.Sprintf("%d", cfg.MemLimit))
	}
	if cfg.CPULimit > 0 {
		args = append(args, "--cpus", fmt.Sprintf("%g", cfg.CPULimit))
	}

	if net := networkFlag(cfg.NetPolicy); net != "" {
		args = append(args, "--network", net)
	}

	for _, m := range cfg.Mounts {
		vol := m.HostPath + ":" + m.ContainerPath
		if m.ReadOnly {
			vol += ":ro"
		}
		args = append(args, "-v", vol)
	}

	args = append(args, image, "sleep", "infinity")

	var createOut, createErr bytes.Buffer
	createCmd := b.docker(ctx, args...)
	createCmd.Stdout = &createOut
	createCmd.Stderr = &createErr
	if err := createCmd.Run(); err != nil {
		return nil, fmt.Errorf("docker create: %s: %w", strings.TrimSpace(createErr.String()), err)
	}

	// -- docker start --------------------------------------------------------
	var startErr bytes.Buffer
	startCmd := b.docker(ctx, "start", name)
	startCmd.Stderr = &startErr
	if err := startCmd.Run(); err != nil {
		// Best-effort cleanup on failure.
		_ = b.docker(ctx, "rm", "-f", name).Run()
		return nil, fmt.Errorf("docker start: %s: %w", strings.TrimSpace(startErr.String()), err)
	}

	// -- install packages (if any) -------------------------------------------
	if len(cfg.Packages) > 0 {
		installCmd := "apt-get update -qq && apt-get install -y -qq " + strings.Join(cfg.Packages, " ")
		installCtx := ctx
		if cfg.Timeout > 0 {
			var cancel context.CancelFunc
			installCtx, cancel = context.WithTimeout(ctx, cfg.Timeout)
			defer cancel()
		}
		var installErr bytes.Buffer
		cmd := b.docker(installCtx, "exec", name, "sh", "-c", installCmd)
		cmd.Stderr = &installErr
		if err := cmd.Run(); err != nil {
			// Cleanup on failure.
			_ = b.docker(ctx, "rm", "-f", name).Run()
			return nil, fmt.Errorf("package install: %s: %w", strings.TrimSpace(installErr.String()), err)
		}
	}

	return &ports.SandboxInstance{
		ID:         id,
		Image:      image,
		Status:     "running",
		UserID:     cfg.UserID,
		CreatedAt:  time.Now(),
		MemLimit:   cfg.MemLimit,
		CPULimit:   cfg.CPULimit,
		NetPolicy:  cfg.NetPolicy,
		Persistent: cfg.Persistent,
	}, nil
}

// Execute runs a command inside the sandbox and returns its output.
func (b *Backend) Execute(ctx context.Context, id string, cmd ports.SandboxCommand) (*ports.SandboxResult, error) {
	name := containerName(id)

	if cmd.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cmd.Timeout)
		defer cancel()
	}

	args := []string{"exec"}
	for k, v := range cmd.Env {
		args = append(args, "-e", k+"="+v)
	}
	if cmd.WorkDir != "" {
		args = append(args, "-w", cmd.WorkDir)
	}
	args = append(args, name, "sh", "-c", cmd.Cmd)

	var stdout, stderr bytes.Buffer
	execCmd := b.docker(ctx, args...)
	execCmd.Stdout = &stdout
	execCmd.Stderr = &stderr

	if cmd.Stdin != "" {
		execCmd.Stdin = strings.NewReader(cmd.Stdin)
	}

	start := time.Now()
	exitCode := 0
	if err := execCmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("docker exec: %w", err)
		}
	}
	duration := time.Since(start)

	return &ports.SandboxResult{
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: duration,
	}, nil
}

// ExecuteStream runs a command inside the sandbox and streams stdout/stderr
// lines back via the returned SandboxOutputStream. The Lines channel receives
// each line as it arrives; Done is closed when the command completes.
func (b *Backend) ExecuteStream(ctx context.Context, id string, cmd ports.SandboxCommand) (*ports.SandboxOutputStream, error) {
	name := containerName(id)

	if cmd.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cmd.Timeout)
		defer cancel()
	}

	args := []string{"exec"}
	for k, v := range cmd.Env {
		args = append(args, "-e", k+"="+v)
	}
	if cmd.WorkDir != "" {
		args = append(args, "-w", cmd.WorkDir)
	}
	args = append(args, name, "sh", "-c", cmd.Cmd)

	execCmd := b.docker(ctx, args...)

	stdoutPipe, err := execCmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("docker exec stdout pipe: %w", err)
	}
	stderrPipe, err := execCmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("docker exec stderr pipe: %w", err)
	}

	if cmd.Stdin != "" {
		execCmd.Stdin = strings.NewReader(cmd.Stdin)
	}

	if err := execCmd.Start(); err != nil {
		return nil, fmt.Errorf("docker exec start: %w", err)
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

// Destroy force-removes the sandbox container.
func (b *Backend) Destroy(ctx context.Context, id string) error {
	name := containerName(id)
	var stderr bytes.Buffer
	cmd := b.docker(ctx, "rm", "-f", name)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker rm: %s: %w", strings.TrimSpace(stderr.String()), err)
	}
	return nil
}

// dockerPSEntry represents one row from `docker ps --format json`.
type dockerPSEntry struct {
	ID     string `json:"ID"`
	Names  string `json:"Names"`
	Image  string `json:"Image"`
	State  string `json:"State"`
	Labels string `json:"Labels"`
}

// List returns all sandbox containers managed by MyPal.
func (b *Backend) List(ctx context.Context) ([]ports.SandboxInstance, error) {
	var stdout, stderr bytes.Buffer
	cmd := b.docker(ctx, "ps", "-a",
		"--filter", "label=mypal.sandbox=true",
		"--format", "json",
	)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("docker ps: %s: %w", strings.TrimSpace(stderr.String()), err)
	}

	// docker ps --format json outputs one JSON object per line (NDJSON).
	var instances []ports.SandboxInstance
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		if line == "" {
			continue
		}
		var entry dockerPSEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue // skip malformed lines
		}

		id := strings.TrimPrefix(entry.Names, "mypal-sandbox-")
		userID := extractLabel(entry.Labels, "mypal.user")

		instances = append(instances, ports.SandboxInstance{
			ID:     id,
			Image:  entry.Image,
			Status: mapState(entry.State),
			UserID: userID,
		})
	}
	return instances, nil
}

// dockerInspect is the subset of `docker inspect` output we care about.
type dockerInspect struct {
	ID      string `json:"Id"`
	Name    string `json:"Name"`
	Created string `json:"Created"`
	State   struct {
		Status string `json:"Status"`
	} `json:"State"`
	Config struct {
		Image  string            `json:"Image"`
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	HostConfig struct {
		Memory   int64   `json:"Memory"`
		NanoCPUs float64 `json:"NanoCpus"`
	} `json:"HostConfig"`
}

// Get returns details for a single sandbox container.
func (b *Backend) Get(ctx context.Context, id string) (*ports.SandboxInstance, error) {
	name := containerName(id)
	var stdout, stderr bytes.Buffer
	cmd := b.docker(ctx, "inspect", name)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("docker inspect: %s: %w", strings.TrimSpace(stderr.String()), err)
	}

	var inspects []dockerInspect
	if err := json.Unmarshal(stdout.Bytes(), &inspects); err != nil {
		return nil, fmt.Errorf("docker inspect: parse: %w", err)
	}
	if len(inspects) == 0 {
		return nil, fmt.Errorf("sandbox %q not found", id)
	}

	info := inspects[0]
	createdAt, _ := time.Parse(time.RFC3339Nano, info.Created)

	return &ports.SandboxInstance{
		ID:        id,
		Image:     info.Config.Image,
		Status:    mapState(info.State.Status),
		UserID:    info.Config.Labels["mypal.user"],
		CreatedAt: createdAt,
		MemLimit:  info.HostConfig.Memory,
		CPULimit:  float64(info.HostConfig.NanoCPUs) / 1e9,
		NetPolicy: inferNetPolicy(info.Config.Labels),
	}, nil
}

// extractLabel parses a "key=value,key2=value2" label string (from docker ps
// --format json) and returns the value for the requested key.
func extractLabel(labels, key string) string {
	for _, pair := range strings.Split(labels, ",") {
		k, v, ok := strings.Cut(pair, "=")
		if ok && strings.TrimSpace(k) == key {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// mapState normalises Docker state strings to the domain vocabulary.
func mapState(state string) string {
	switch strings.ToLower(state) {
	case "running":
		return "running"
	case "created", "restarting":
		return "creating"
	default:
		return "stopped"
	}
}

// inferNetPolicy attempts to derive the original net policy from labels. We
// don't store the policy in Docker metadata, so this is best-effort.
func inferNetPolicy(labels map[string]string) string {
	if p, ok := labels["mypal.net_policy"]; ok {
		return p
	}
	return ""
}

// Compile-time interface check.
var _ ports.SandboxBackend = (*Backend)(nil)
