# Phase 3: Sandbox Code Execution

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add isolated code execution via Docker and Incus containers, replacing the current bare host execution for untrusted code. Pre-warmed container pool, per-user resource limits, permission-gated access.

**Architecture:** New `SandboxPort` interface with Docker and Incus backends. Sandbox tools registered alongside existing internal tools. Permission matrix gates sandbox access per-user. Dashboard view for monitoring active instances.

**Tech Stack:** Go, Docker SDK (`github.com/docker/docker`), Incus client SDK (`github.com/lxc/incus`), GORM, gqlgen, SolidJS

---

## Task 1: Sandbox Port + Domain Models

**Files:**
- Create: `apps/backend/internal/domain/ports/sandbox.go`
- Create: `apps/backend/internal/domain/models/sandbox.go`
- Modify: `apps/backend/internal/infrastructure/persistence/migrate.go`

Port interface:
```go
type SandboxBackend interface {
    Create(ctx context.Context, cfg SandboxConfig) (*SandboxInstance, error)
    Execute(ctx context.Context, id string, cmd Command) (*SandboxResult, error)
    Destroy(ctx context.Context, id string) error
    List(ctx context.Context) ([]SandboxInstance, error)
    Get(ctx context.Context, id string) (*SandboxInstance, error)
}

type SandboxConfig struct {
    Image       string
    Packages    []string
    MemLimit    int64   // bytes
    CPULimit    float64 // cores
    Timeout     time.Duration
    NetPolicy   string  // "none", "restricted", "full"
    Persistent  bool
    UserID      string
}

type Command struct {
    Cmd     string
    Stdin   string
    Env     map[string]string
    WorkDir string
}

type SandboxInstance struct { ID, Image, Status, UserID string; CreatedAt time.Time; MemLimit int64; CPULimit float64 }
type SandboxResult struct { ExitCode int; Stdout, Stderr string; Duration time.Duration }
```

DB model for tracking sandbox instances (SandboxInstanceModel) — add to AutoMigrate.

Verify build. Commit.

---

## Task 2: Sandbox Manager Service

**Files:**
- Create: `apps/backend/internal/domain/services/sandbox/manager.go`

The manager wraps the backend and adds pooling + lifecycle:
```go
type Manager struct {
    backend     ports.SandboxBackend
    repo        SandboxRepositoryPort  // optional, for persistence
    poolSize    int
    timeout     time.Duration
    memDefault  int64
    cpuDefault  float64
    netDefault  string
}
```

Methods:
- `CreateSandbox(ctx, userID, cfg)` — create via backend, track in DB
- `Execute(ctx, sandboxID, cmd)` — execute in existing sandbox
- `RunOnce(ctx, userID, image, cmd)` — create, execute, destroy (ephemeral)
- `DestroySandbox(ctx, id)` — destroy via backend, remove from DB
- `ListSandboxes(ctx)` — list all active
- `ListUserSandboxes(ctx, userID)` — list per-user
- `WarmPool(ctx, image, count)` — pre-create N containers for an image
- `ClaimFromPool(ctx, userID, image)` — take a pre-warmed container

Verify build. Commit.

---

## Task 3: Docker Backend

**Files:**
- Create: `apps/backend/internal/infrastructure/adapters/sandbox/docker/backend.go`
- Modify: `apps/backend/go.mod` (add Docker SDK)

Implements `SandboxBackend` using Docker API:
- `Create`: `docker create` with image, mem/cpu limits, network mode
- `Execute`: `docker exec` with command, capture stdout/stderr
- `Destroy`: `docker rm -f`
- `List`: `docker ps` with label filter
- `Get`: `docker inspect`

Use `github.com/docker/docker/client` SDK. Label containers with `mypal.sandbox=true`, `mypal.user=<userID>`.

Network policies: "none" = `--network none`, "restricted" = custom bridge, "full" = default.

Verify build. Commit.

---

## Task 4: Incus Backend

**Files:**
- Create: `apps/backend/internal/infrastructure/adapters/sandbox/incus/backend.go`

Implements `SandboxBackend` using Incus API:
- `Create`: Create instance via Incus client, apply resource limits
- `Execute`: `incus exec` equivalent
- `Destroy`: Delete instance
- `List`/`Get`: Query Incus API

Use `github.com/lxc/incus/v6/client` SDK. If the Incus SDK is heavy or has build issues, implement via CLI exec (`incus` command) as a fallback.

Verify build. Commit.

---

## Task 5: Sandbox Configuration

**Files:**
- Modify: `apps/backend/internal/infrastructure/config/config.go`

```go
type SandboxConfig struct {
    Enabled     bool    `mapstructure:"enabled"`
    Backend     string  `mapstructure:"backend"`      // "docker" or "incus"
    PoolSize    int     `mapstructure:"pool_size"`
    Timeout     int     `mapstructure:"timeout"`       // seconds
    MemDefault  int64   `mapstructure:"mem_default"`   // bytes, default 256MB
    CPUDefault  float64 `mapstructure:"cpu_default"`   // cores, default 1.0
    NetDefault  string  `mapstructure:"net_default"`   // "none"
    DockerHost  string  `mapstructure:"docker_host"`   // optional, default unix socket
    IncusSocket string  `mapstructure:"incus_socket"`  // optional
}
```

Add to Config struct. Set defaults. Validate (when enabled, backend must be "docker" or "incus").

Verify build. Commit.

---

## Task 6: Sandbox Tools (LLM-callable)

**Files:**
- Create: `apps/backend/internal/domain/services/mcp/sandbox_tools.go`
- Modify: `apps/backend/internal/domain/services/mcp/internal_tools.go` (register tools)

Tools:
- `sandbox_execute`: Run code in an ephemeral sandbox (image, command, timeout)
- `sandbox_create`: Create a persistent sandbox (image, packages)
- `sandbox_run`: Execute in an existing persistent sandbox (id, command)
- `sandbox_list`: List user's sandboxes
- `sandbox_destroy`: Destroy a sandbox

Add "sandbox" capability gate in `CapabilityForTool()`.

Register in `RegisterAllInternalTools()` when sandbox manager is provided.

Verify build. Commit.

---

## Task 7: Sandbox Repository + Wiring

**Files:**
- Create: `apps/backend/internal/domain/repositories/sandbox/sandbox_repository.go`
- Modify: `apps/backend/internal/domain/ports/repositories.go`
- Modify: `apps/backend/cmd/mypal/serve/services.go`

Repository for tracking sandbox instances in DB. Wire sandbox manager + backend creation in services.go based on config (docker or incus). Pass sandbox manager to tool registration.

Verify build. Commit.

---

## Task 8: Sandbox GraphQL Schema + Resolvers

**Files:**
- Create: `schema/sandbox.graphql`
- Create resolver, wire deps

```graphql
type SandboxInstance {
  id: String!
  image: String!
  status: String!
  userId: String!
  memLimit: Int!
  cpuLimit: Float!
  createdAt: String!
}

type SandboxResult {
  exitCode: Int!
  stdout: String!
  stderr: String!
  durationMs: Int!
}

extend type Query {
  sandboxInstances: [SandboxInstance!]!
  sandboxInstance(id: String!): SandboxInstance
}

extend type Mutation {
  createSandbox(image: String!, persistent: Boolean): SandboxInstance!
  executeSandbox(id: String!, command: String!): SandboxResult!
  destroySandbox(id: String!): Boolean!
}
```

Verify build. Commit.

---

## Task 9: Frontend — Sandbox Dashboard View

**Files:**
- Create: `apps/frontend/src/views/SandboxView/SandboxView.tsx` + `.css`
- Modify: App.tsx routing, Header nav

Dashboard showing:
- Active sandbox instances (table: id, image, status, user, resource usage, created)
- Create sandbox button (image selector, resource limits)
- Execute command in sandbox (input + output display)
- Destroy button per instance
- Resource usage summary

Verify frontend builds. Commit.

---

## Summary

| Task | Description | Dependencies |
|------|-------------|-------------|
| 1 | Sandbox port + domain models | None |
| 2 | Sandbox manager service | Task 1 |
| 3 | Docker backend | Task 1 |
| 4 | Incus backend | Task 1 |
| 5 | Sandbox configuration | None |
| 6 | Sandbox tools (LLM-callable) | Task 2 |
| 7 | Repository + wiring | Tasks 2, 3, 4, 5 |
| 8 | GraphQL schema + resolvers | Task 7 |
| 9 | Frontend sandbox view | Task 8 |
