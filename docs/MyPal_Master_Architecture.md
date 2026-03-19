# MyPal — Design Document

**Project**: MyPal (hard-fork of MyPal v0.2.0, rebrand)
**Origin**: MyPal (Neirth/MyPal) + MyPalClara (BangRocket/mypalclara)
**Language**: Go (primary), with targeted rewrites from Python where needed
**License**: GPLv3 (inherited from MyPal; OpenClaw's MIT is compatible)

---

## 1. Project thesis

MyPal provides the infrastructure chassis — multi-user, multi-channel, security-first Go backend with GraphQL API, web dashboard, MCP integration with OAuth 2.1, task scheduler, and encrypted secrets management.

MyPalClara provides the soul — personality system with per-user relationships, organic response behavior, proactive engagement, model tier routing, code execution sandbox, email channel, and a mem0-based memory system with both vector search and graph storage.

MyPal is the union: a personal AI assistant platform that is secure, multi-channel, and multi-user out of the box, but with the behavioral depth and tool capabilities that make it feel like a genuine companion rather than a chatbot.

---

## 2. Architecture overview

```
┌─────────────────────────────────────────────────────────────┐
│                      MyPal Core (Go)                        │
│                                                             │
│  ┌──────────┐  ┌──────────┐  ┌───────────┐  ┌───────────┐  │
│  │Personality│  │  Organic  │  │ Proactive │  │   Model   │  │
│  │  Engine   │  │ Response  │  │  Engine   │  │   Router  │  │
│  └──────────┘  └──────────┘  └───────────┘  └───────────┘  │
│                                                             │
│  ┌──────────┐  ┌──────────┐  ┌───────────┐  ┌───────────┐  │
│  │  Memory   │  │ Sandbox  │  │   Task    │  │    MCP    │  │
│  │  System   │  │  Manager │  │ Scheduler │  │  Manager  │  │
│  └──────────┘  └──────────┘  └───────────┘  └───────────┘  │
│                                                             │
│  ┌─────────────────────────────────────────────────────┐    │
│  │              Channel Abstraction Layer               │    │
│  │  Discord │ Telegram │ WhatsApp │ Slack │ SMS │ Email │    │
│  └─────────────────────────────────────────────────────┘    │
│                                                             │
│  ┌─────────────────────────────────────────────────────┐    │
│  │                  GraphQL API (gqlgen)                │    │
│  │          Web Dashboard  │  External Clients          │    │
│  └─────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────┘
```

### What comes from MyPal (keep as-is or extend)

- Go binary / build system
- GraphQL API via gqlgen
- Web dashboard (frontend)
- Channel abstraction (Discord, Telegram, WhatsApp, Slack, SMS)
- User pairing flow and per-user isolation
- MCP integration with OAuth 2.1, per-user permission matrix, marketplace
- Task scheduler (cron + ISO 8601)
- Secrets backend (encrypted file / OpenBao)
- Auth (bearer token, encrypted config on disk)
- Database migrations on startup

### What comes from MyPalClara (port to Go)

- Personality engine
- Organic response system
- Proactive engine
- Model tier routing
- Memory system (vector search + graph)
- Code execution sandbox
- Email channel
- Heartbeat system

---

## 3. Phase 1 — Personality & intelligence layer

### 3.1 Personality engine

**Source**: `mypalclara/personalities/`

The personality system defines how the assistant behaves, speaks, and relates to each user. This is not a system prompt template — it is a structured configuration that shapes every interaction.

**Data model** (new Go struct):

```go
type Personality struct {
    ID          string            `json:"id"`
    Name        string            `json:"name"`
    BasePrompt  string            `json:"base_prompt"`
    Traits      []string          `json:"traits"`
    Tone        string            `json:"tone"`
    Boundaries  []string          `json:"boundaries"`
    Quirks      []string          `json:"quirks"`
    Adaptations map[string]string `json:"adaptations"` // channel-specific tweaks
}

type UserPersonaRelationship struct {
    UserID        string            `json:"user_id"`
    PersonalityID string            `json:"personality_id"`
    Familiarity   float64           `json:"familiarity"`   // 0.0-1.0, grows over time
    Preferences   map[string]string `json:"preferences"`   // learned user-specific prefs
    History       RelationshipMeta  `json:"history"`
}
```

**Key behaviors**:

- Each user has their own relationship with the personality. User A might have a casual, familiar relationship while User B is still in early "getting to know you" mode.
- Familiarity score increases organically based on interaction frequency, depth, and duration. This affects formality, humor, initiative, and how much the assistant references shared history.
- Channel-specific adaptations: the same personality is more terse on SMS, more expressive on Discord, more professional on email.
- Personality configs are stored in the database (not YAML files) and editable via the dashboard under Settings → Personality.
- Multiple personalities can be defined; the default is assigned to new users, but operators can assign different ones.

**Dashboard integration**: New section under Settings → Personality with:

- Personality CRUD (create, edit, delete personality configs)
- Per-user relationship viewer (see familiarity scores, learned preferences)
- Personality preview (test a prompt against a personality to see the output style)

### 3.2 Model tier routing

**Source**: MyPalClara's `!high`/`!mid`/`!low` prefix system

MyPal supports multiple providers but only one active at a time. MyPal needs to route individual messages to different model tiers based on complexity, user preference, or explicit prefix.

**Data model**:

```go
type ProviderConfig struct {
    Provider string       `json:"provider"` // anthropic, openai, ollama, openrouter, etc.
    Tiers    []ModelTier  `json:"tiers"`
}

type ModelTier struct {
    Name     string  `json:"name"`     // "high", "mid", "low"
    Model    string  `json:"model"`    // "claude-opus-4", "claude-sonnet-4", "claude-haiku-4"
    Prefix   string  `json:"prefix"`   // "!high", "!mid", "!low"
    CostCap  float64 `json:"cost_cap"` // optional daily/monthly cost limit
    Default  bool    `json:"default"`  // which tier to use when no prefix given
}
```

**Routing logic**:

1. Check for explicit prefix (`!high`, `!mid`, `!low`) — strip it from the message, route to that tier
2. If no prefix, use the user's default tier (configurable per-user in the dashboard)
3. Future: automatic complexity-based routing (message length, tool calls needed, conversation depth)

**Provider-specific tier mappings** (configured in dashboard):

| Provider  | High          | Mid             | Low            |
| --------- | ------------- | --------------- | -------------- |
| Anthropic | claude-opus-4 | claude-sonnet-4 | claude-haiku-4 |
| OpenAI    | gpt-5.2       | gpt-5.1         | gpt-5          |
| Ollama    | llama-3.3-70b | llama-3.3-8b    | llama-3.2-3b   |

**Multiple active providers**: Unlike MyPal's one-at-a-time constraint, MyPal can have multiple providers configured simultaneously. Each tier can optionally point to a different provider (e.g., high = Anthropic Opus, mid = local Ollama, low = OpenRouter cheap model). This enables cost optimization and offline fallback.

### 3.3 Organic response system

**Source**: `mypalclara/organic_response_system.py`

The organic response system governs when and how the assistant speaks in multi-user contexts (group chats, shared channels) without being directly addressed.

**Ported behaviors**:

- **Contextual relevance scoring**: Evaluate each message in a group for whether the assistant has something meaningful to contribute. Score based on: topic relevance to assistant's knowledge, direct/indirect references to the assistant, emotional cues that suggest someone needs support, questions that go unanswered for N seconds.
- **Cooldown management**: Prevent the assistant from dominating group conversations. Configurable cooldown between organic responses (default: 5 minutes). Cooldown resets on direct mention.
- **Tone matching**: Organic responses match the energy of the conversation — playful in a casual thread, reserved in a serious discussion. This ties into the personality engine's tone settings.
- **Opt-out signals**: Recognize when users don't want the assistant participating (e.g., "we're talking to each other", conversation shifting to private topics). Back off gracefully.

**Additional guidelines to implement** (extending beyond MyPalClara's current system):

- **Thread awareness**: In platforms that support threads (Discord, Slack), only organically respond in threads the assistant has already been part of, or in the main channel. Don't jump into every thread.
- **Reaction-based engagement**: On platforms that support reactions (Discord, Slack), the assistant can react to messages as a lightweight form of acknowledgment without sending a full response. Use sparingly.
- **Conversation phase detection**: Detect whether a group conversation is in an active discussion phase, winding down, or idle. Only organically respond during active phases.
- **Knowledge confidence threshold**: Only organically respond when the assistant is confident it has accurate, helpful information. Avoid guessing or speculating in organic responses — save that for direct conversations where the user can push back.
- **Cultural context**: In channels with established communication norms (e.g., a channel where people only post memes, or a channel that's strictly business), adapt organic response style and frequency accordingly. Learn these norms from observation over time.
- **Multi-assistant awareness**: If other bots or assistants are active in a channel, avoid piling on. Detect other bot responses and yield.

**Configuration** (per-channel in dashboard):

```go
type OrganicResponseConfig struct {
    Enabled              bool          `json:"enabled"`
    CooldownDuration     time.Duration `json:"cooldown_duration"`
    RelevanceThreshold   float64       `json:"relevance_threshold"` // 0.0-1.0
    MaxDailyOrganic      int           `json:"max_daily_organic"`   // per channel
    AllowReactions       bool          `json:"allow_reactions"`
    ThreadPolicy         string        `json:"thread_policy"` // "joined_only", "all", "none"
    QuietHoursStart      string        `json:"quiet_hours_start"` // "22:00"
    QuietHoursEnd        string        `json:"quiet_hours_end"`   // "08:00"
}
```

---

## 4. Phase 2 — Proactive capabilities

### 4.1 Proactive engine with heartbeat

**Source**: `mypalclara/proactive_engine.py` + MyPal's task scheduler + OpenClaw's heartbeat concept (improved)

The proactive engine allows MyPal to initiate conversations and take autonomous action based on schedules, triggers, and evolving context. It combines MyPal's cron scheduler with a heartbeat file that the bot maintains and modifies.

**Heartbeat system**:

The heartbeat is a structured document (stored in the database, editable in the dashboard, and exportable as `HEARTBEAT.md` for transparency) that the bot reads, acts on, and updates. Unlike OpenClaw's static HEARTBEAT.md that was read every 30 minutes, MyPal's heartbeat is:

- **Bot-modifiable**: The assistant can add, complete, reschedule, and deprioritize its own tasks based on context. If a user mentions "remind me about X on Friday," the assistant adds it to the heartbeat.
- **Cron-integrated**: Each heartbeat item can have a cron expression or ISO 8601 datetime. The task scheduler fires the evaluation; the heartbeat provides the context.
- **Priority-aware**: Items have priority levels and the bot triages during each heartbeat cycle.
- **Auditable**: Every modification the bot makes to the heartbeat is logged with a reason.

**Data model**:

```go
type HeartbeatItem struct {
    ID           string        `json:"id"`
    Title        string        `json:"title"`
    Description  string        `json:"description"`
    Schedule     string        `json:"schedule"`      // cron expression or ISO 8601
    Priority     int           `json:"priority"`      // 1 (critical) to 5 (low)
    Status       string        `json:"status"`        // "active", "completed", "snoozed", "cancelled"
    CreatedBy    string        `json:"created_by"`    // "user:<id>" or "system" or "bot"
    TargetUser   string        `json:"target_user"`   // who to contact (optional)
    TargetChannel string       `json:"target_channel"` // where to send (optional)
    Context      string        `json:"context"`       // additional context for the bot
    LastRun      time.Time     `json:"last_run"`
    NextRun      time.Time     `json:"next_run"`
    History      []HeartbeatLog `json:"history"`
}

type HeartbeatLog struct {
    Timestamp time.Time `json:"timestamp"`
    Action    string    `json:"action"`    // "executed", "snoozed", "modified", "created"
    Reason    string    `json:"reason"`
    Result    string    `json:"result"`    // outcome of execution
}
```

**Heartbeat cycle flow**:

1. Scheduler fires the heartbeat evaluation (configurable interval, default 15 minutes)
2. Bot loads all active heartbeat items with `next_run <= now`
3. For each item, bot evaluates:
   - Is the target user active/available? (check last message timestamp, quiet hours)
   - Is the context still relevant? (check memory for updates)
   - What's the best channel to reach them?
4. Bot executes or defers, logs the decision
5. Bot scans recent conversations for implicit tasks to add to the heartbeat
6. Heartbeat state is persisted; dashboard reflects current state

**Dashboard integration**: New section under Tasks → Heartbeat with:

- Visual heartbeat timeline
- Manual add/edit/remove items
- Execution log with bot's reasoning
- Export as HEARTBEAT.md for external use

### 4.2 Email channel

**Source**: `mypalclara/email_service/` and `mypalclara/email_monitor.py`

Email becomes a first-class channel alongside Discord, Telegram, WhatsApp, Slack, and SMS. It should feel as natural as any other channel — not bolted on.

**Implementation approach**:

The email channel implements the same channel interface as Discord/Telegram/etc. in MyPal's abstraction layer.

```go
type EmailChannel struct {
    IMAPConfig   IMAPConfig
    SMTPConfig   SMTPConfig
    PollInterval time.Duration
    Filters      []EmailFilter
}

type EmailFilter struct {
    Field    string `json:"field"`    // "from", "subject", "to", "label"
    Pattern  string `json:"pattern"`  // regex or glob
    Action   string `json:"action"`   // "process", "ignore", "forward"
}
```

**Key requirements**:

- **IMAP polling** with configurable interval (default 60 seconds). IDLE support where the server allows it.
- **Thread awareness**: Group email replies into conversations using `In-Reply-To` and `References` headers. Map email threads to MyPal conversation threads.
- **Natural tone**: Email responses should feel like a human wrote them — proper greeting, appropriate formality (tied to personality engine), signature. Not like a chatbot reply in an email body.
- **Attachment handling**: Process common attachments (PDF, images, documents) as context. Store in the file system with S3 sync.
- **Filtering**: Configurable rules for which emails the bot processes vs. ignores (e.g., only from known contacts, only with specific subjects, exclude newsletters).
- **User pairing**: Map email addresses to MyPal user accounts using the same pairing flow as other channels.
- **Send capability**: The bot can compose and send emails as a tool, not just respond to incoming ones. Useful for proactive engine tasks ("email Josh the weekly summary").

**Dashboard integration**: Email appears as a channel under Settings → Channels, with:

- IMAP/SMTP configuration
- Filter rules
- Email-specific personality overrides (more formal tone, signature block)
- Test send capability

---

## 5. Phase 3 — Code execution & sandbox

### 5.1 Built-in sandbox manager

**Source**: `mypalclara/sandbox/` and `mypalclara/sandbox_service/`

The sandbox provides isolated code execution environments. Unlike MyPalClara's external sandbox service, MyPal's sandbox is built directly into the core binary with support for both Incus/LXC and Docker as backends.

**Architecture**:

```go
type SandboxManager struct {
    Backend    SandboxBackend // "incus" or "docker"
    PoolSize   int            // pre-warmed containers
    Timeout    time.Duration  // max execution time
    MemLimit   int64          // bytes
    CPULimit   float64        // cores
    NetPolicy  string         // "none", "restricted", "full"
}

type SandboxBackend interface {
    Create(config SandboxConfig) (SandboxInstance, error)
    Execute(id string, command Command) (Result, error)
    Destroy(id string) error
    List() ([]SandboxInstance, error)
}

type SandboxConfig struct {
    Image       string            `json:"image"`       // "python:3.12", "node:22", "ubuntu:24.04"
    Packages    []string          `json:"packages"`    // pre-install on creation
    Mounts      []Mount           `json:"mounts"`      // read-only file mounts
    Environment map[string]string `json:"environment"`
    Persistent  bool              `json:"persistent"`  // keep between executions
}
```

**Backends**:

- **Incus/LXC** (preferred for homelab): System containers with better resource isolation. Ideal for long-running or persistent sandboxes. Leverages your existing Proxmox/Incus setup.
- **Docker** (fallback / portability): Standard Docker containers. Easier for users who don't have Incus. Uses the Docker socket.

**Features**:

- **Pre-warmed pool**: Keep N containers ready to go for instant execution. Configurable per-image.
- **Persistent sandboxes**: Per-user persistent containers that retain state between executions. Useful for ongoing development tasks.
- **Network policies**: No network (default for untrusted code), restricted (allow specific domains), full (for trusted users).
- **File I/O**: Mount user files read-only into the sandbox. Copy output files back.
- **Streaming output**: Stream stdout/stderr back to the user in real-time via the channel.
- **Resource limits**: Memory, CPU, and time limits. Kill on exceeded.

**Permission integration**: Sandbox access is gated by the per-user permission matrix. Operators can grant/revoke sandbox access per user, set per-user resource limits, and restrict which images are available.

**Dashboard integration**: New section under Tools → Sandbox with:

- Active sandbox instances
- Resource usage per user
- Execution history and logs
- Image management (pull, tag, remove)

### 5.2 Claude Code delegation (future)

Deferred for later implementation. The concept: a built-in MCP tool that delegates complex, multi-step coding tasks to a Claude Code agent running in a sandbox. The agent gets its own persistent workspace, can install packages, run tests, and iterate — then reports results back through the conversation.

When implemented, this would be exposed as a tool in the MCP permission matrix, gated per-user.

---

## 6. Phase 4 — Memory system (Go port)

### 6.1 Overview

**Source**: `mypalclara/vendor/mem0/`, `mypalclara/clara_core/` (memory components)

This is the largest porting effort. MyPalClara's memory system has two layers:

1. **Vector memory** (semantic search): Embeddings stored in pgvector or Qdrant, used for "find me the conversation where we talked about X" type retrieval.
2. **Graph memory** (relationship tracking): Entity-relationship graph stored in Neo4j or Kuzu, used for "what do I know about Josh's project" type queries.

Both layers need to be ported to Go while maintaining compatibility with the existing memory data format so that existing MyPalClara deployments can migrate.

### 6.2 Vector memory (Go port)

**Current Python implementation**: mem0 wraps embedding generation (OpenAI embeddings API) + vector storage (pgvector or Qdrant) + chunking/retrieval logic.

**Go port approach**:

```go
type VectorMemory struct {
    EmbeddingProvider EmbeddingProvider // OpenAI, Ollama, local
    Store             VectorStore       // pgvector, Qdrant
    ChunkSize         int
    ChunkOverlap      int
    TopK              int               // default retrieval count
}

type EmbeddingProvider interface {
    Embed(text string) ([]float64, error)
    EmbedBatch(texts []string) ([][]float64, error)
    Dimensions() int
}

type VectorStore interface {
    Upsert(id string, vector []float64, metadata map[string]any) error
    Search(vector []float64, topK int, filters map[string]any) ([]MemoryResult, error)
    Delete(id string) error
}

type MemoryResult struct {
    ID        string         `json:"id"`
    Content   string         `json:"content"`
    Score     float64        `json:"score"`
    Metadata  map[string]any `json:"metadata"`
    UserID    string         `json:"user_id"`
    Timestamp time.Time      `json:"timestamp"`
}
```

**Supported vector backends**:

- **pgvector**: PostgreSQL extension. Aligns with MyPal's existing PostgreSQL usage. Recommended default.
- **Qdrant**: Standalone vector DB. Higher performance at scale, but another service to run.

**Supported embedding providers**:

- **OpenAI** (default): Best quality embeddings. Requires API key and internet access.
- **Ollama**: Local-first alternative. No external dependency. Operator selects via configuration.

The embedding provider is pluggable via the `EmbeddingProvider` interface — add new providers by implementing `Embed`, `EmbedBatch`, and `Dimensions`.

**Migration path**: Write a one-time migration tool that reads mem0's existing pgvector/Qdrant data and maps it to MyPal's schema. The vector data itself doesn't change — it's the metadata schema and indexing that needs alignment.

### 6.3 Graph memory (Go port)

**Current state**: MyPalClara uses FalkorDB or Kuzu for graph storage.

**Go port approach**: Port MyPalClara's entity-relationship model with two backend options.

```go
type GraphMemory struct {
    Backend   GraphBackend // falkordb, kuzu
}

type GraphBackend interface {
    AddEntity(entity Entity) error
    AddRelation(relation Relation) error
    Query(query GraphQuery) ([]Entity, error)
    GetNeighbors(entityID string, depth int) ([]Entity, []Relation, error)
    Search(text string, limit int) ([]Entity, error)
}

type Entity struct {
    ID          string            `json:"id"`
    Type        string            `json:"type"`     // "person", "project", "concept", "event"
    Name        string            `json:"name"`
    Properties  map[string]any    `json:"properties"`
    UserID      string            `json:"user_id"`  // per-user isolation
    CreatedAt   time.Time         `json:"created_at"`
    UpdatedAt   time.Time         `json:"updated_at"`
}

type Relation struct {
    ID       string         `json:"id"`
    FromID   string         `json:"from_id"`
    ToID     string         `json:"to_id"`
    Type     string         `json:"type"`     // "works_on", "knows", "related_to"
    Weight   float64        `json:"weight"`
    Metadata map[string]any `json:"metadata"`
}
```

**Per-user isolation**: Every entity and relation is scoped to a user ID. The graph backend enforces this at the query level — User A cannot see User B's memory graph. This extends MyPal's existing per-user isolation model.

### 6.4 Unified memory interface

Both vector and graph memory are accessed through a single interface that the rest of the system uses:

```go
type MemorySystem struct {
    Vector *VectorMemory
    Graph  *GraphMemory
}

// Store a memory (creates vector embedding + graph entities)
func (m *MemorySystem) Remember(userID string, content string, metadata MemoryMetadata) error

// Retrieve relevant memories (combines vector search + graph context)
func (m *MemorySystem) Recall(userID string, query string, opts RecallOptions) ([]Memory, error)

// Bootstrap from profile data
func (m *MemorySystem) Bootstrap(userID string, profile UserProfile) error

// Clear all memories for a user
func (m *MemorySystem) Forget(userID string) error
```

### 6.5 Memory bootstrap

Port MyPalClara's bootstrap tool — the ability to seed a user's memory from a structured profile (interests, preferences, relationships, project history). This runs on first pairing or on demand.

```go
type UserProfile struct {
    Name         string            `json:"name"`
    Preferences  map[string]string `json:"preferences"`
    Interests    []string          `json:"interests"`
    Projects     []ProjectInfo     `json:"projects"`
    Relationships []RelationInfo   `json:"relationships"`
}
```

---

## 7. Dashboard extensions

The MyPal web dashboard gets new sections:

| Section                     | Source   | Description                                           |
| --------------------------- | -------- | ----------------------------------------------------- |
| Settings → Personality      | New      | Personality CRUD, per-user relationship viewer        |
| Settings → Model Tiers      | New      | Provider configs, tier mappings, cost caps            |
| Settings → Channels → Email | New      | IMAP/SMTP config, filters, tone overrides             |
| Tools → Sandbox             | New      | Active instances, resource usage, execution history   |
| Tasks → Heartbeat           | New      | Visual timeline, bot-managed task list, execution log |
| Memory (enhanced)           | Extended | Vector search explorer + graph browser (existing)     |
| Chat (enhanced)             | Extended | Organic response config per channel                   |

---

## 8. Migration plan

### From MyPalClara to MyPal

1. **Memory migration**: Run the migration tool to port mem0 vector data + graph data into MyPal's schema
2. **Personality migration**: Convert personality YAML configs to MyPal's database format
3. **Heartbeat migration**: Convert any existing proactive engine schedules to heartbeat items
4. **Channel migration**: Discord bot token and config transfer directly; email config maps to new email channel settings

### From MyPal to MyPal

The hard fork means you take the full codebase. Key changes:

1. Rename all `MYPAL_*` env vars to `MYPAL_*`
2. Rebrand dashboard UI (logo, name, color scheme)
3. Update Docker image names, binary name
4. Preserve MyPal's database migration system — add new migrations for MyPal-specific tables

---

## 9. Implementation order

```
Phase 1a: Fork & rebrand
  ├── Hard-fork MyPal v0.2.0
  ├── Rename to MyPal (env vars, binary, dashboard branding)
  ├── Verify build, tests, existing functionality
  └── Set up CI/CD (GitHub Actions → VPS deploy)

Phase 1b: Personality & model tiers
  ├── Personality engine (Go structs, DB schema, CRUD API)
  ├── Per-user persona relationships
  ├── Model tier routing middleware
  ├── Dashboard: Settings → Personality, Settings → Model Tiers
  └── Organic response system (channel message handler extension)

Phase 2: Proactive & email
  ├── Heartbeat data model + DB schema
  ├── Heartbeat evaluation loop (hooks into existing scheduler)
  ├── Bot self-modification of heartbeat items
  ├── Email channel (IMAP/SMTP, implements channel interface)
  ├── Dashboard: Tasks → Heartbeat, Settings → Channels → Email
  └── Integration testing across channels

Phase 3: Sandbox
  ├── SandboxManager with Incus backend
  ├── Docker backend (fallback)
  ├── Pre-warmed container pool
  ├── Permission matrix integration
  ├── Dashboard: Tools → Sandbox
  └── Streaming output to channels

Phase 4: Memory port
  ├── VectorMemory Go implementation + pgvector backend
  ├── Embedding provider interface (OpenAI default, Ollama local)
  ├── GraphMemory Go implementation (FalkorDB + Kuzu backends)
  ├── Unified MemorySystem interface
  ├── Memory bootstrap tool
  ├── Migration tool (mem0 → MyPal format)
  ├── Dashboard: enhanced Memory section
  └── Qdrant backend (optional, for scale)
```

---

## 10. Infrastructure / deployment

**Target environment**: Self-hosted on Proxmox/Unraid homelab

**Minimal deployment**:

- Single Go binary (MyPal core)
- PostgreSQL (conversations, config, pgvector for memory)
- FalkorDB (graph memory, production) or Kuzu (graph memory, small/test deployments)
- Optional: Incus (for sandbox, if code execution is enabled)

**Docker Compose deployment**:

```yaml
services:
  mypal:
    image: mypal:latest
    environment:
      - MYPAL_GRAPHQL_AUTH_TOKEN=${AUTH_TOKEN}
      - MYPAL_DB_URL=postgresql://...
      - MYPAL_MEMORY_BACKEND=pgvector  # or "file" for local-only
      - MYPAL_SANDBOX_BACKEND=docker   # or "incus"
    ports:
      - "3000:3000"  # Dashboard + API
    volumes:
      - ./data:/data

  postgres:
    image: pgvector/pgvector:pg16
    volumes:
      - pgdata:/var/lib/postgresql/data

  falkordb:
    image: falkordb/falkordb:latest
    ports:
      - "6379:6379"
    volumes:
      - falkordata:/data
```

---

## 11. Resolved design decisions

1. **License**: GPLv3. OpenClaw is MIT (GPL-compatible), MyPal is GPLv3. MyPal inherits GPLv3.
2. **Upstream tracking**: None. MyPal is a full hard fork — a new project, not a downstream consumer. No effort spent on merge compatibility with MyPal.
3. **Dashboard framework**: Keep MyPal's existing SolidJS + Vite + vanilla CSS frontend. Extend it with new sections (Personality, Model Tiers, Heartbeat, Sandbox, etc.) rather than replacing it.
4. **Graph memory backend**: FalkorDB as the primary graph backend for production deployments. Kuzu as the lightweight embedded option for small/test deployments. GML file backend and Neo4j are dropped.
5. **Embedding model**: Pluggable embedding provider interface. OpenAI embeddings as the default (best quality, requires API key). Ollama embeddings as a local-first alternative. Operator selects via configuration.
