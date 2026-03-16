# MyPal: Master Architecture Document

**Version:** 1.0  
**Date:** March 2026  
**Authors:** Joshua / Clara  
**Status:** Design  
**Lineage:** Consolidates MyPal Architecture Plan, Rook v2 Memory Architecture, and OpenLobster-Informed Additions  

---

## 1. Vision

MyPal is the next generation of MyPalClara — a multi-agent, multi-user, multi-tenant personal AI platform. Each agent is an independent persona with its own identity, memory, tools, and LLM configuration. Agents can spawn sub-agents for task delegation. The platform supports rich input modalities (voice, vision, files) and diverse interaction patterns (chat, API, webhooks, scheduled tasks, events).

**Stack**: Python-only backend (FastAPI), React frontend.

### 1.1 What We Keep from MyPalClara

| Component | Status | Notes |
|-----------|--------|-------|
| LLM Provider system | **Keep** | Already provider-agnostic. Add per-agent config. |
| Adapter protocol | **Keep** | WebSocket protocol + adapter base class are solid. |
| Plugin system | **Keep** | Already supports the right plugin kinds. |
| Sandbox system | **Keep** | Clean abstraction, works as-is. |
| Model tier system (high/mid/low) | **Keep** | Proven useful. |
| CalVer versioning | **Keep** | YYYY.WW.N format. |
| PostgreSQL + pgvector | **Keep** | Consolidate to single DB. |

### 1.2 What Changes

| Pattern | Current Clara | MyPal | Influence |
|---------|--------------|-------|-----------|
| Tool injection | All 40+ tools every message | Selective injection via classifier | OpenClaw |
| Memory | Opaque mem0 vectors | Rook v2: scoped, FSRS dynamics, human-readable | Cortex lessons |
| Prompt composition | Monolithic system prompt | Composable files: persona, tools, user | OpenClaw |
| Session keys | Simple IDs | Structured keys encoding routing context | OpenClaw |
| User identity | Single user assumed | Multi-user with pairing flow | OpenLobster |
| Tool permissions | Admin-only MCP permissions | Three-level Allow/Deny/Ask per-user per-tool | OpenLobster |
| Scheduled tasks | Separate scheduler code path | Loopback dispatch through same pipeline | OpenLobster |
| Gateway processor | Hardcoded for "Clara" | Pluggable agent runtimes | Original redesign |
| Rails backend | Ruby on Rails | FastAPI (Python) | Stack consolidation |
| Tool loop detection | None (ORS removed) | Built-in with configurable limits | OpenClaw |

### 1.3 Why Not the Current Gateway

The current gateway combines three concerns into one:

1. **Transport** (WebSocket server, adapter connections)
2. **Routing** (message dedup, debouncing, channel queuing)
3. **Processing** (context building, LLM calls, tool execution — all hardcoded for "Clara")

MyPal separates these cleanly. The transport layer stays thin. The router becomes tenant-and-agent-aware. Processing moves into pluggable agent runtimes.

---

## 2. Core Architecture

### 2.1 Layered Design

```
┌─────────────────────────────────────────────────────┐
│                    Input Layer                       │
│  Adapters (Discord, Slack, Teams, Telegram, etc.)   │
│  API Gateway (REST/WebSocket)                       │
│  Webhooks / Scheduled Triggers / Event Sources      │
├─────────────────────────────────────────────────────┤
│                   Router / Dispatcher                │
│  Tenant resolution → Pairing → Agent selection       │
├─────────────────────────────────────────────────────┤
│                   Agent Runtime Layer                │
│  Agent instances (Clara, Rex, custom...)             │
│  Sub-agent orchestration                            │
│  Tool execution, sandbox, MCP                       │
├─────────────────────────────────────────────────────┤
│                   Core Services                      │
│  Rook v2 (Memory) │  LLM Providers  │  Identity     │
│  Session Manager   │  Tool Registry  │  Permissions  │
├─────────────────────────────────────────────────────┤
│                   Data Layer                         │
│  PostgreSQL (relational)  │  pgvector (embeddings)  │
│  Redis (cache/pubsub)     │  FalkorDB (graph, opt)  │
└─────────────────────────────────────────────────────┘
```

### 2.2 Message Processing Pipeline

Every message — whether from a human on Discord, a cron job, a webhook, or an API call — follows the same path:

```
1. Message arrives           → Adapter normalizes to IncomingMessage
2. Tenant resolution         → Which tenant owns this channel/server?
3. Pairing check             → Is this platform identity linked to a user?
4. Agent selection           → Which agent handles this? (explicit, channel binding, default)
5. Context building          → Rook retrieves memories, session history, previous summary
6. Tool discovery + filtering → Available tools minus DENY'd tools for this user
7. LLM call                  → Message + context + tools → AI provider
8. Tool execution (if any)   → Permission check (ASK flow if needed) → execute → result back to LLM
9. Response generation       → Final response from LLM
10. Memory extraction        → Reflect stage: extract new facts from exchange
11. Save + deliver           → Persist to DB, update Rook, route response to channel
```

---

## 3. Agent System

### 3.1 Agent Definition

An agent is defined by a manifest (stored in DB, editable via API):

```python
@dataclass
class AgentDefinition:
    id: str                          # "clara", "rex", etc.
    tenant_id: str                   # Owning tenant
    name: str                        # Display name
    persona: PersonaConfig           # System prompt, personality traits, tone
    llm_config: LLMConfig            # Provider, model, tier defaults
    memory_config: MemoryConfig      # What memory scopes to use
    tools: list[str]                 # Enabled tool IDs / MCP servers
    capabilities: set[Capability]    # CHAT, CODE, VOICE, VISION, PROACTIVE, etc.
    sub_agents: list[str]            # Agent IDs this agent can delegate to
    max_concurrent: int              # Max concurrent conversations
    metadata: dict                   # Extensible config
```

**Key principle:** Agents are data, not code. Creating a new agent is an API call, not a deployment.

### 3.2 Agent Runtime

Each active agent conversation gets an `AgentRuntime` instance:

```python
class AgentRuntime:
    definition: AgentDefinition
    session: Session
    memory: ScopedMemory            # Agent-scoped view of Rook
    llm: LLMProvider                # Configured per-agent
    tools: ToolRegistry             # Agent's available tools
    context: ConversationContext     # Messages, memories, metadata

    async def process(self, message: IncomingMessage) -> AsyncIterator[ResponseChunk]:
        """Main processing loop: context build → LLM → tool execution → response"""

    async def delegate(self, sub_agent_id: str, task: str) -> SubAgentResult:
        """Spawn a sub-agent for task delegation"""
```

The runtime is instantiated per-conversation, not a singleton. This sidesteps the concurrency problems that active mode batching was a workaround for in current Clara.

### 3.3 Sub-Agent Orchestration

Sub-agents are regular agents invoked programmatically by a parent agent:

```python
class SubAgentOrchestrator:
    async def run(self, agent_id: str, task: str, context: dict) -> SubAgentResult:
        """
        1. Load sub-agent definition
        2. Create ephemeral runtime (no persistent session)
        3. Execute task with parent context
        4. Return result to parent
        """
```

Sub-agents:
- Have their own tool access and LLM config
- Receive scoped context from the parent (not full conversation history)
- Results are returned to the parent agent, not directly to the user
- **Max depth of 2** (parent → sub-agent → sub-sub-agent). Hard cap, not configurable. The ORS lesson: recursive delegation is the same class of problem as autonomous feedback loops.

### 3.4 Agent Selection & Routing

When a message arrives, the router determines which agent handles it:

```
1. Explicit mention: "@Rex review this code" → route to Rex
2. Channel binding: #code-review channel → always Rex
3. DM default: User's default agent (usually Clara)
4. Tenant default: Tenant-configured default agent
5. Conversation continuity: Continue with whichever agent last responded
```

Multiple agents can participate in the same conversation (group agent chat) if the tenant enables it.

### 3.5 User Pairing

When a message arrives from an unknown platform identity (e.g., a Discord user ID that doesn't map to any MyPal user), the system enters a pairing flow before the message reaches the agent runtime.

#### 3.5.1 Pairing States

```python
class PairingState(str, Enum):
    UNKNOWN = "unknown"           # Never seen this platform identity
    PAIRING_STARTED = "started"   # Pairing flow in progress
    PAIRED = "paired"             # Identity linked to a MyPal user
    BLOCKED = "blocked"           # Explicitly denied access
```

#### 3.5.2 Pairing Service

```python
class PairingService:
    """Handles first-contact identity linking.
    
    Sits in the Router/Dispatcher layer, BEFORE the message
    hits the Agent Runtime. If the user isn't paired, the
    pairing flow intercepts the message and handles it directly.
    """
    
    async def check_pairing(
        self,
        platform: str,
        platform_user_id: str,
        tenant_id: str,
    ) -> PairingResult:
        # 1. Look up existing mapping
        user = await self.db.get_user_by_external_id(platform, platform_user_id)
        if user:
            return PairingResult(state=PairingState.PAIRED, user=user)
        
        # 2. Check if blocked
        if await self.db.is_blocked(platform, platform_user_id):
            return PairingResult(state=PairingState.BLOCKED)
        
        # 3. Check if pairing in progress
        pending = await self.db.get_pending_pairing(platform, platform_user_id)
        if pending:
            return PairingResult(state=PairingState.PAIRING_STARTED, pairing=pending)
        
        # 4. New identity
        return PairingResult(state=PairingState.UNKNOWN)
```

#### 3.5.3 Pairing Modes

Tenants configure how pairing works:

```python
class PairingMode(str, Enum):
    OPEN = "open"                    # Anyone can pair, auto-create user
    APPROVAL = "approval"            # Admin must approve new users
    INVITE_ONLY = "invite_only"      # Must have a pairing code / invite link
    CLOSED = "closed"                # No new users via chat (web UI only)
```

**Open mode** (default): First message from an unknown user auto-creates a MyPal account and links the platform identity. Zero friction.

**Approval mode** (teams): First message triggers a request. Admin approves/denies from dashboard.

**Invite-only mode** (controlled access): User must provide a pairing code on first contact.

**Closed mode** (web UI only): Chat adapters reject unknown users.

#### 3.5.4 Security Property

The pairing flow is handled by the `PairingService` directly with templated responses, NOT by the agent. An unverified user cannot prompt-inject the agent before they're authenticated. Cross-platform identity linking always requires verification through a trusted channel (email, existing linked platform, or web UI).

#### 3.5.5 Where Pairing Sits

```
Message arrives from adapter
        │
        ▼
┌───────────────────┐
│  Tenant Resolution │
└────────┬──────────┘
         │
         ▼
┌───────────────────┐
│  Pairing Check     │  ← PairingService (before Agent Runtime)
└────────┬──────────┘
         │
    ┌────┴────┐
    │         │
  PAIRED    UNKNOWN
    │         │
    ▼         ▼
  Agent    Pairing Flow
  Runtime  (templated, not LLM)
```

---

## 4. Multi-Tenancy

### 4.1 Tenant Model

```python
@dataclass
class Tenant:
    id: str                          # UUID
    slug: str                        # URL-friendly name
    name: str                        # Display name
    owner_id: str                    # Primary admin user
    plan: PlanTier                   # FREE, PRO, ENTERPRISE
    settings: TenantSettings         # Feature flags, limits
    pairing_config: TenantPairingConfig  # How new users join
    created_at: datetime
```

### 4.2 User Model

```python
@dataclass
class User:
    id: str                          # UUID
    tenant_id: str                   # Primary tenant
    external_ids: dict[str, str]     # {"discord": "123", "slack": "U456", ...}
    display_name: str
    role: TenantRole                 # OWNER, ADMIN, MEMBER, GUEST
    preferences: UserPreferences     # Default agent, notification settings, etc.
```

Users can belong to multiple tenants. External IDs map platform identities to a single MyPal user.

### 4.3 Isolation

| Resource | Isolation Method |
|----------|-----------------|
| Database rows | `tenant_id` column on all tables, enforced by query scoping |
| Memories (Rook) | `tenant_id` in metadata filter on vector queries |
| Sessions | Scoped by tenant + user + agent |
| Agent definitions | Per-tenant, with system-provided defaults |
| Tools/MCP | Per-tenant tool allowlists + per-user permissions |
| Files/sandbox | Per-tenant container isolation |

Row-level isolation with strict query scoping. Not separate databases per tenant (complexity not worth it at this stage).

---

## 5. Memory System (Rook v2)

### 5.1 Lineage & Lessons Learned

Rook v2 is the third generation of Clara's memory system.

**Cortex (v0.8 / LangGraph era)** had the right abstractions — Redis for fast retrieval, pgvector for semantic search, importance-weighted TTL. But `_store_longterm()` and `_semantic_search()` were stubs that silently did nothing. Memories were written to working memory but never persisted. Semantic search always returned empty.

**mem0 (current production)** actually stores and retrieves memories. But storage is opaque (no inspection/editing), there's no scoping beyond `user_id`, no dedup/supersede, no decay, and all memories are injected every time.

**What Rook v2 must solve:**

| Problem | Solution |
|---------|----------|
| Silent failures (Cortex stubs) | Every storage path has integration tests that verify round-trip |
| Opaque storage (mem0) | Human-readable export, web UI for memory inspection/editing |
| No scoping (mem0) | Multi-dimensional scoping: tenant → agent → user → session |
| No dedup/supersede | Ingestion pipeline with similarity check before write |
| No decay (mem0) | FSRS-6 spaced repetition dynamics |
| Context bloat (mem0) | Budgeted retrieval with token-aware truncation |
| No cross-agent sharing | Explicit scope queries with permission checks |

### 5.2 What Is a Memory?

A memory is a discrete piece of knowledge that an agent has learned or been told. It is not a message, a conversation log, or an embedding — it's a structured fact derived from those things.

```python
@dataclass
class Memory:
    id: str                          # UUID
    tenant_id: str                   # Owning tenant
    scope: MemoryScope               # Where this memory lives
    agent_id: str | None
    user_id: str | None
    
    content: str                     # The fact, in natural language
    category: MemoryCategory         # FACT, PREFERENCE, OBSERVATION, RELATIONSHIP, SKILL, CONTEXT, INSTRUCTION, EVENT
    source: MemorySource             # CONVERSATION, REFLECTION, INGESTION, API, USER_EDIT, CROSS_AGENT, EVENT
    
    embedding: list[float] | None
    embedding_model: str | None
    
    # FSRS Dynamics
    fsrs_state: FSRSState
    stability: float                 # How long until this memory fades (days)
    difficulty: float                # How hard this is to retain (0-1)
    last_review: datetime | None
    next_review: datetime | None
    retrievability: float            # Current probability of recall (0-1)
    
    importance: float                # 0.0 - 1.0
    confidence: float                # 0.0 - 1.0
    supersedes: str | None
    superseded_by: str | None
    access_count: int
    created_at: datetime
    updated_at: datetime
    expires_at: datetime | None
```

### 5.3 Memory Scopes

```python
class MemoryScope(str, Enum):
    SYSTEM = "system"                # Platform-wide shared knowledge (admin-only write)
    TENANT = "tenant"                # Shared across all agents & users in a tenant
    AGENT = "agent"                  # Agent-specific knowledge (persona, domain expertise)
    USER = "user"                    # Per-user facts shared across all agents
    USER_AGENT = "user_agent"        # Per-user-per-agent (relationship memories)
    SESSION = "session"              # Ephemeral, dies with the session
```

When Agent "Clara" talks to User "Josh" in Tenant "personal":

```
Retrieved (narrowest first, highest priority):
  1. SESSION    — current conversation context
  2. USER_AGENT — Clara's specific memories about Josh
  3. USER       — facts about Josh any agent can see
  4. AGENT      — Clara's general knowledge
  5. TENANT     — shared team/org knowledge
  6. SYSTEM     — platform-wide knowledge (if any)
```

### 5.4 Storage Architecture

Everything lives in PostgreSQL with pgvector. No separate vector database.

```sql
CREATE TABLE memories (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    scope VARCHAR(20) NOT NULL,
    agent_id UUID REFERENCES agents(id),
    user_id UUID REFERENCES users(id),
    session_id UUID REFERENCES sessions(id),
    content TEXT NOT NULL,
    category VARCHAR(20) NOT NULL,
    source VARCHAR(20) NOT NULL,
    embedding vector(1536),
    embedding_model VARCHAR(50),
    fsrs_stability FLOAT NOT NULL DEFAULT 1.0,
    fsrs_difficulty FLOAT NOT NULL DEFAULT 0.5,
    fsrs_last_review TIMESTAMPTZ,
    fsrs_next_review TIMESTAMPTZ,
    fsrs_retrievability FLOAT NOT NULL DEFAULT 1.0,
    fsrs_reps INT NOT NULL DEFAULT 0,
    fsrs_lapses INT NOT NULL DEFAULT 0,
    fsrs_state VARCHAR(10) NOT NULL DEFAULT 'new',
    importance FLOAT NOT NULL DEFAULT 0.5,
    confidence FLOAT NOT NULL DEFAULT 1.0,
    supersedes UUID REFERENCES memories(id),
    superseded_by UUID REFERENCES memories(id),
    access_count INT NOT NULL DEFAULT 0,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ,
    
    CONSTRAINT valid_scope_refs CHECK (
        (scope = 'session' AND session_id IS NOT NULL) OR
        (scope = 'user_agent' AND user_id IS NOT NULL AND agent_id IS NOT NULL) OR
        (scope = 'user' AND user_id IS NOT NULL) OR
        (scope = 'agent' AND agent_id IS NOT NULL) OR
        (scope IN ('tenant', 'system'))
    )
);

CREATE INDEX idx_memories_embedding ON memories 
    USING hnsw (embedding vector_cosine_ops) WITH (m = 16, ef_construction = 64);
```

**Redis** is a hot cache only, not a primary store. If Redis is down, Rook falls back to Postgres-only. This was a Cortex lesson — Redis being the only store for identity facts meant a restart wiped profiles.

**FalkorDB** (optional) provides graph-based relationship tracking for tenants that need it. Cypher-compatible, Redis-based, lightweight.

### 5.5 FSRS-6 Memory Dynamics

FSRS runs at retrieval time, not on a timer:

```python
async def retrieve(self, query: str, scopes: list[MemoryScope], ...) -> list[Memory]:
    candidates = await self._vector_search(query, scopes, limit=50)
    
    now = datetime.utcnow()
    for memory in candidates:
        memory.retrievability = fsrs.calculate_retrievability(
            stability=memory.fsrs_stability,
            elapsed_days=(now - memory.fsrs_last_review).total_seconds() / 86400
        )
        memory.retrieval_score = (
            memory.similarity * 0.5 +
            memory.retrievability * 0.3 +
            memory.importance * 0.2
        )
    
    ranked = sorted(candidates, key=lambda m: m.retrieval_score, reverse=True)
    return self._apply_token_budget(ranked, budget_tokens)
```

Scope-specific decay rates:

```python
SCOPE_STABILITY_MULTIPLIER = {
    MemoryScope.SYSTEM: 10.0,
    MemoryScope.TENANT: 5.0,
    MemoryScope.AGENT: 3.0,
    MemoryScope.USER: 2.0,
    MemoryScope.USER_AGENT: 1.0,
    MemoryScope.SESSION: 0.1,
}
```

### 5.6 Ingestion Pipeline

Every memory write goes through: **Normalize → Dedup Check (0.92 similarity threshold) → Supersede Check (0.7 threshold + LLM classification) → Embed → Store**.

Supersession detection uses a cheap LLM call to determine if "lives in Portland" contradicts "lives in Seattle." Cost guards: cheapest model tier, rate-limited, skipped for session-scoped memories, falls back to "just store it" on failure.

### 5.7 Retrieval Pipeline

Two-tier retrieval (from Cortex):

- **Quick context** (for Evaluate stage): No vector search, no LLM. Just Redis cache hits for identity facts.
- **Full context** (for Respond stage): Full vector search, budget allocation, FSRS scoring.

Budget allocation: identity (500 tokens) → session messages (1500) → previous summary (300) → semantic memories (remaining).

### 5.8 Cross-Agent Memory

Agents within the same tenant can read each other's AGENT-scoped memories by default. USER_AGENT memories are private (relationship-specific). Permission model: PRIVATE, TENANT_READ, TENANT_WRITE, EXPLICIT.

### 5.9 Memory Extraction

Memories are extracted at two points: real-time extraction during the Reflect stage (lightweight, cheap model), and session summary on timeout (full conversation analysis). Extraction includes existing context to avoid re-extracting known facts.

### 5.10 Human-Readable Memory

Web UI memory browser with search, filter, sort, inline editing, FSRS health gauges, supersession chains, and bulk operations. Markdown export for backup and inspection. Full CRUD API.

### 5.11 Testing (The Cortex Lesson)

Every storage path has a round-trip integration test:

```python
async def test_memory_round_trip():
    rook = RookManager(test_config)
    memory_id = await rook.ingest(
        content="User's favorite color is blue",
        scope=MemoryScope.USER, user_id="test-user",
        category=MemoryCategory.PREFERENCE,
    )
    results = await rook.retrieve(
        query="what color does the user like",
        scopes=[(MemoryScope.USER, {"user_id": "test-user"})],
    )
    assert len(results) > 0
    assert "blue" in results[0].content
```

### 5.12 Migration from mem0

Selective import, not bulk migration. Export mem0 memories as text, run each through Rook's ingestion pipeline (dedup + supersede + categorize), present results for user review before finalizing.

### 5.13 Future: Cognitive Science Extensions

The `docs/MEMORY-MODEL-SPEC.md` on the web-ui-rebuild branch contains a 1,385-line spec for extending memory beyond FSRS into spreading activation (Collins & Loftus 1975, ACT-R base-level activation), prediction error gating (surprise-based encoding), and cognitive science-grounded retrieval. These are P3+ explorations that would enrich Rook v2 after the fundamentals are proven, particularly:

- **Spreading activation** for graph-based retrieval (when FalkorDB is integrated): query activates matching nodes, activation spreads through edges, highest total activation wins
- **Prediction error gating**: memories formed during surprising or unexpected events get higher initial stability (the "oh shit, that's new" effect)
- **ACT-R base-level activation**: frequency + recency-weighted retrieval that complements FSRS's spaced repetition model

These are not blocking for any phase but represent the next frontier once Rook v2 is stable.

---

## 6. Input Layer

### 6.1 Unified Message Format

All inputs normalize to:

```python
@dataclass
class IncomingMessage:
    id: str
    tenant_id: str
    user_id: str
    agent_id: str | None             # Target agent (None = router decides)
    channel_id: str
    platform: str                    # "discord", "api", "webhook", "loopback", etc.
    text: str | None
    attachments: list[Attachment]
    voice_audio: bytes | None
    metadata: dict
    reply_to: str | None
    conversation_id: str | None
    timestamp: datetime
```

### 6.2 Platform Adapters

Keep the current adapter pattern. Each adapter connects to the transport layer, normalizes platform messages to `IncomingMessage`, and handles platform-specific output. Adapters remain stateless message translators.

### 6.3 API Gateway

FastAPI-based HTTP + WebSocket API replacing both the current gateway HTTP API and Rails:

```
POST   /api/v1/chat                    # Send message, get response
WS     /api/v1/chat/stream             # Streaming chat via WebSocket
POST   /api/v1/agents                  # Create/configure agents
GET    /api/v1/agents/{id}/memory      # Query agent memory
POST   /api/v1/webhooks                # Register webhook triggers
POST   /api/v1/tasks                   # Schedule a task
GET    /api/v1/tenants/{id}/users      # Tenant user management
```

### 6.4 Event Sources & Loopback Dispatch

Every non-chat trigger (cron jobs, webhooks, file watchers, email events) is converted into a **loopback message** — a synthetic `IncomingMessage` that enters the same processing pipeline as a user message. The agent doesn't know or care that the message didn't come from a human.

```python
class LoopbackDispatcher:
    async def dispatch(
        self,
        tenant_id: str,
        agent_id: str,
        content: str,
        source: str,                     # "scheduler", "webhook", "email", etc.
        target_channel: ChannelTarget | None = None,
        user_id: str | None = None,
        metadata: dict | None = None,
    ) -> None:
        message = IncomingMessage(
            id=generate_id(),
            tenant_id=tenant_id,
            user_id=user_id or await self._get_system_user(tenant_id),
            agent_id=agent_id,
            channel_id=f"loopback:{source}",
            platform="loopback",
            text=content,
            metadata={"source": source, "loopback": True,
                      "target_channel": target_channel.dict() if target_channel else None,
                      **(metadata or {})},
            timestamp=utcnow(),
        )
        await self.router.route(message)
```

This ensures scheduled tasks get memory context, respect tool permissions, and can route responses to any channel. There is exactly one code path for "agent processes input and produces output."

Event source implementations:

- **SchedulerEventSource**: Cron expressions for recurring jobs, ISO 8601 datetimes for one-shot tasks
- **WebhookEventSource**: Incoming webhooks (GitHub, CI, etc.) transformed into agent prompts
- **EmailEventSource**: Email monitoring, evolved from current `email_monitor.py`
- **FileWatchEventSource**: File system change notifications

Users can create tasks conversationally ("Remind me every Monday at 9am to check the pipeline") via a `create_scheduled_task` agent tool.

### 6.5 Modality Processing

Modality processors handle non-text inputs before they reach the agent:

```python
class ModalityProcessor(ABC):
    async def process(self, attachment: Attachment) -> ProcessedInput: ...

class VoiceProcessor(ModalityProcessor):   # STT → text + audio features
class VisionProcessor(ModalityProcessor):  # Image analysis, OCR
class DocumentProcessor(ModalityProcessor) # PDF/doc parsing
class VideoProcessor(ModalityProcessor):   # Frame extraction, transcription
```

---

## 7. Tools & MCP

### 7.1 Tool Registry

Tools come from three sources: built-in (Python functions), MCP servers (external via protocol), and agent-specific (per-agent tool configuration).

### 7.2 MCP Transport Strategy

```python
class TransportType(str, Enum):
    STREAMABLE_HTTP = "streamable_http"  # Preferred: remote HTTP endpoint
    SSE = "sse"                          # Legacy: Server-Sent Events
    STDIO = "stdio"                      # Local: subprocess management
```

**Streamable HTTP** (preferred for production): MCP server is a URL endpoint. No process management, works across networks, OAuth works naturally.

**STDIO** (local development): Important for personal use. Can be disabled for multi-tenant production (`allow_stdio=False`) since it spawns subprocesses on the host. For stdio-only servers in production, use `mcp-proxy` or `supergateway` to bridge to HTTP.

### 7.3 MCP Server Config

```python
@dataclass
class MCPServerConfig:
    id: str
    tenant_id: str
    name: str
    display_name: str
    transport: TransportType
    endpoint_url: str | None         # For HTTP/SSE
    command: str | None              # For stdio
    args: list[str] | None
    env: dict[str, str] | None
    oauth_config: OAuthConfig | None
    enabled: bool
    status: ServerStatus             # connected, disconnected, error, starting
    tool_count: int
    health_check_interval: int       # Seconds between health checks (0 = disabled)
```

### 7.4 Health Checks & Reconnection

HTTP-based MCP servers are health-checked periodically. Failed servers get exponential backoff reconnection (1s, 2s, 4s) with 3 attempts before marking as error.

---

## 8. Permissions & Access Control

### 8.1 RBAC Model

```
Tenant
├── Owner        # Full control, billing, delete tenant
├── Admin        # Manage agents, users, settings
├── Member       # Use agents, manage own sessions
└── Guest        # Limited agent access, read-only shared memories
```

### 8.2 Authentication (Hybrid: Clerk + Identity Service)

Two auth systems with distinct responsibilities:

**Clerk** handles web UI authentication:
- Sign-in / sign-up (social login, email/password, MFA)
- Session management across devices
- Token refresh, account recovery
- Pre-built React components (`<SignIn>`, `<UserButton>`, etc.)
- `useAuth()` / `useUser()` hooks already wired through the web-ui codebase
- Clerk `userId` is the foreign key into MyPal's user system

**Identity service** handles platform identity linking:
- Maps platform identities (Discord, Telegram, Slack user IDs) to a canonical MyPal user
- The `PlatformLink` model: `(platform, platform_user_id) → canonical_user_id`
- Auto-pairing via `/users/ensure-link` for adapter first-contact
- OAuth token storage for platform-specific APIs (Google Workspace, etc.)
- Clerk can't do this — it doesn't know about Discord bot user IDs or Telegram chat IDs

**How they connect:**

```
Web UI login (Clerk):
  User signs in via Clerk → Clerk userId → look up CanonicalUser by clerk_user_id
  If no CanonicalUser exists → create one, link Clerk as a platform

Discord message (identity service):
  Discord user ID → PlatformLink lookup → CanonicalUser
  If no link exists → pairing flow (§3.5)

Both paths end at the same CanonicalUser, which is what the rest of MyPal uses.
```

```python
class CanonicalUser:
    id: str                          # MyPal's internal user ID (UUID)
    tenant_id: str                   # Owning tenant
    display_name: str
    role: TenantRole                 # OWNER, ADMIN, MEMBER, GUEST
    # Clerk links this user to web auth; PlatformLinks link to chat platforms

class PlatformLink:
    canonical_user_id: str           # → CanonicalUser.id
    platform: str                    # "clerk", "discord", "telegram", "slack", etc.
    platform_user_id: str            # Clerk userId, Discord snowflake, etc.
    linked_via: str                  # "oauth", "auto", "manual"
```

Clerk itself is stored as a PlatformLink with `platform="clerk"`. This means a user who signs in via Clerk on the web and also talks to Clara on Discord has two PlatformLinks pointing to the same CanonicalUser — exactly the cross-platform identity model MyPal needs.

**API authentication:**
- Web UI requests: Clerk JWT in `Authorization` header → FastAPI middleware verifies via Clerk's JWKS → resolve to CanonicalUser
- Adapter requests (internal): service secret header → identity service resolves platform ID to CanonicalUser
- External API requests: tenant-scoped API keys

### 8.3 Tool-Level Permissions

Every tool invocation is governed by a three-level permission system:

```python
class ToolPolicy(str, Enum):
    ALLOW = "allow"      # Agent can use this tool without asking
    DENY = "deny"        # Agent cannot use this tool, period
    ASK = "ask"          # Agent must ask user for permission before each use
```

#### Resolution Chain

The first explicit match wins:

```
1. User-specific override  (most specific)
2. Agent-specific default
3. Tenant-wide default
4. Role-based default
5. System default = ALLOW  (least specific)
```

Role-based sensible defaults:

```python
ROLE_TOOL_DEFAULTS = {
    TenantRole.GUEST: {
        "terminal": ToolPolicy.DENY,
        "filesystem_write": ToolPolicy.DENY,
        "browser": ToolPolicy.ASK,
        "sandbox": ToolPolicy.DENY,
    },
    TenantRole.MEMBER: {
        "terminal": ToolPolicy.ASK,
        "filesystem_write": ToolPolicy.ASK,
    },
}
```

#### The "Ask" Flow

When a tool resolves to ASK, the agent requests permission in-conversation before executing. The user responds naturally ("yes", "go ahead", "no", "skip"). Timeout after 60 seconds defaults to denied.

This means the agent runtime must support **mid-turn interruption**: LLM proposes tool call → permission check → if ASK, yield control to user → resume on user response.

#### Where Permission Checks Happen

**Point A — Pre-LLM:** Filter tool list. DENY'd tools removed entirely (LLM never sees them). ASK tools annotated.

**Point B — Pre-execution:** Check again before executing (defense in depth). Trigger ASK flow if needed.

#### Permission Database

```sql
CREATE TABLE tool_permissions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    user_id UUID REFERENCES users(id),
    agent_id UUID REFERENCES agents(id),
    tool_id VARCHAR(200) NOT NULL,
    policy VARCHAR(10) NOT NULL,       -- allow, deny, ask
    set_by UUID REFERENCES users(id),
    reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

#### Web UI

Permission matrix showing users × tools with Allow/Deny/Ask toggles. Changes are immediate and persisted. Gray cells indicate inherited defaults.

---

## 9. Session & Conversation Model

### 9.1 Session Scoping

```python
@dataclass
class Session:
    id: str
    tenant_id: str
    user_id: str
    agent_id: str                    # Which agent this session is with
    channel_id: str
    conversation_id: str | None      # Group conversation (multiple users)
    started_at: datetime
    last_activity_at: datetime
    summary: str | None
    previous_session_id: str | None
```

`agent_id` is part of the session key. User talking to Clara and user talking to Rex are separate sessions.

### 9.2 Group Conversations

```python
@dataclass
class Conversation:
    id: str
    tenant_id: str
    channel_id: str
    participants: list[str]          # User IDs
    agents: list[str]                # Active agent IDs
    created_at: datetime
```

Each agent maintains its own Session within the conversation for memory continuity, but sees the shared message history.

---

## 10. Tech Stack

### 10.1 Backend

| Component | Technology |
|-----------|-----------|
| HTTP API | **FastAPI** |
| WebSocket | **FastAPI WebSocket** + `websockets` |
| Task queue | **arq** (Redis-backed) |
| Database ORM | **SQLAlchemy 2.0** (async) |
| Migrations | **Alembic** |
| Auth | **Clerk** (web UI) + **identity service** (platform pairing) |
| Caching | **Redis** |

### 10.2 Frontend

| Component | Technology | Notes |
|-----------|-----------|-------|
| SPA | **React 19 + TypeScript** | Ported from `feat/web-ui-rebuild` |
| Build | **Vite 7** | |
| Styling | **Tailwind CSS 4** + oklch design tokens | Warm cream/terracotta palette (light + dark) |
| UI Primitives | **shadcn/ui** (Radix) | 18 components already configured with theme |
| Chat UI | **@assistant-ui/react** | Production-grade: streaming, threads, branching, attachments, cancellation |
| Rich Text | **TipTap** | For memory editor, rich input |
| State | **Zustand 5** | Chat store, agent store, artifact store |
| Data Fetching | **@tanstack/react-query** | Memory queries, session list |
| Routing | **react-router-dom 7** | |
| Markdown | **react-markdown** + remark-gfm + react-syntax-highlighter | |
| Icons | **lucide-react** | |
| Real-time | **WebSocket** (native, custom hook with reconnect) | |

### 10.3 Data

| Store | Purpose |
|-------|---------|
| **PostgreSQL** | Primary relational + pgvector embeddings |
| **Redis** | Caching, pub/sub, task queue backend |
| **FalkorDB** | Graph memory (optional) |

---

## 11. Project Structure

```
mypal/
├── mypal/
│   ├── main.py                      # FastAPI app entrypoint
│   ├── config.py                    # Settings (Pydantic BaseSettings)
│   │
│   ├── api/v1/                      # FastAPI routes
│   │   ├── chat.py, agents.py, tenants.py, users.py
│   │   ├── memory.py, tasks.py, webhooks.py, admin.py
│   │   ├── permissions.py, onboarding.py
│   │   ├── auth.py, deps.py
│   │
│   ├── agents/                      # Agent system
│   │   ├── definition.py, runtime.py, router.py
│   │   ├── orchestrator.py          # Sub-agent orchestration
│   │   ├── pairing.py               # User pairing service
│   │   └── personas/clara.py        # Built-in persona configs
│   │
│   ├── transport/                   # Input/output transport
│   │   ├── websocket.py, protocol.py, stream.py
│   │
│   ├── adapters/                    # Platform adapters
│   │   ├── base.py
│   │   ├── discord/, slack/, telegram/, cli/
│   │
│   ├── inputs/                      # Input processing
│   │   ├── message.py               # IncomingMessage model
│   │   ├── loopback.py              # Loopback dispatcher
│   │   ├── events.py                # Event source implementations
│   │   ├── modalities/              # Voice, vision, document processors
│   │   └── webhooks.py
│   │
│   ├── memory/                      # Rook v2
│   │   ├── manager.py, scopes.py, retriever.py, writer.py
│   │   ├── dynamics.py              # FSRS scheduling
│   │   ├── ingestion.py             # Dedup/supersede pipeline
│   │   ├── extraction.py            # Memory extraction from conversations
│   │   ├── vector/, embeddings/, graph/
│   │
│   ├── llm/                         # LLM providers (ported)
│   │   ├── config.py, providers/
│   │
│   ├── tools/                       # Tool system
│   │   ├── registry.py, executor.py
│   │   ├── permissions.py           # Three-level permission resolver
│   │   ├── sandbox/, mcp/, builtins/
│   │
│   ├── tenants/                     # Multi-tenancy
│   │   ├── models.py, service.py, isolation.py
│   │
│   ├── auth/                        # Authentication
│   │   ├── clerk.py                 # Clerk JWT verification (JWKS middleware)
│   │   ├── pairing.py              # Platform identity linking (PlatformLink, ensure-link)
│   │   ├── api_keys.py
│   │   └── rbac.py                  # Role-based access
│   │
│   ├── db/                          # Database
│   │   ├── models.py, session.py, migrations/
│   │
│   ├── services/                    # Background services
│   │   ├── email/, backup/, scheduler.py
│   │
│   └── plugins/                     # Plugin system (ported)
│
├── web/                             # React frontend (ported from feat/web-ui-rebuild)
│   ├── package.json
│   ├── vite.config.ts
│   ├── index.html
│   ├── src/
│   │   ├── App.tsx                  # Router + auth guard
│   │   ├── main.tsx                 # Entry point
│   │   ├── index.css                # oklch design system tokens (light/dark)
│   │   ├── api/
│   │   │   └── client.ts            # Typed API client (memories, sessions, users, agents, permissions)
│   │   ├── auth/
│   │   │   ├── ClerkProvider.tsx     # Clerk auth (kept from web-ui-rebuild)
│   │   │   └── TokenBridge.tsx       # Provides Clerk getToken() to API client
│   │   ├── components/
│   │   │   ├── ui/                   # shadcn/ui primitives (button, card, dialog, etc.)
│   │   │   ├── assistant-ui/         # assistant-ui chat components (thread, markdown, attachments)
│   │   │   ├── chat/                 # Chat-specific (RuntimeProvider, BranchSidebar, TierSelector, ToolCallBlock)
│   │   │   ├── knowledge/            # Memory browser (MemoryCard, MemoryEditor, MemoryGrid, SearchBar)
│   │   │   ├── layout/               # AppLayout, UnifiedSidebar, AgentSwitcher
│   │   │   ├── settings/             # AdapterLinking, PermissionMatrix
│   │   │   └── admin/                # TenantSettings, OnboardingWizard
│   │   ├── hooks/
│   │   │   ├── useGatewayWebSocket.ts  # WebSocket transport (auth, heartbeat, reconnect)
│   │   │   ├── useMemories.ts          # Rook v2 memory queries
│   │   │   ├── useBranches.ts          # Conversation branching
│   │   │   └── useTheme.ts             # Light/dark mode
│   │   ├── stores/
│   │   │   ├── chatStore.ts           # Zustand: messages, threads, streaming, branches
│   │   │   ├── chatRuntime.ts         # assistant-ui ↔ store bridge
│   │   │   ├── artifactStore.ts       # File/artifact state
│   │   │   └── agentStore.ts          # Active agent selection (NEW)
│   │   ├── pages/
│   │   │   ├── Chat.tsx
│   │   │   ├── KnowledgeBase.tsx
│   │   │   ├── Settings.tsx
│   │   │   ├── Permissions.tsx        # Tool permission matrix (NEW)
│   │   │   └── Onboarding.tsx         # Tenant setup wizard (NEW)
│   │   ├── lib/
│   │   │   ├── utils.ts               # cn(), classname helpers
│   │   │   ├── attachmentAdapter.ts   # File upload → gateway wire format
│   │   │   └── threadGroups.ts        # Thread grouping logic
│   │   └── utils/
│   │       └── fileProcessing.ts      # Client-side file handling
│   └── Dockerfile
│
├── tests/
├── alembic.ini, pyproject.toml, docker-compose.yml
```

---

## 12. Migration Strategy

### Phase 1: Foundation (Weeks 1-6)

- [ ] FastAPI skeleton with **Clerk JWT verification** (JWKS middleware) for web requests + **service secret** for adapter requests
- [ ] Core models: Tenant, User (evolve from `CanonicalUser`, add `tenant_id` + `role`), AgentDefinition, Session
- [ ] **Port identity service pairing logic** to `mypal/auth/pairing.py` — PlatformLink, ensure-link, OAuth token storage. Clerk becomes `platform="clerk"` in PlatformLink
- [ ] User pairing service (§3.5) — add pairing modes on top of existing `ensure-link`
- [ ] Port LLM provider system with per-agent config
- [ ] Rook v2 core: memory table, embedding, vector search, round-trip tests
- [ ] Basic agent runtime (single agent, text only)
- [ ] Tool permission schema (table + resolver, full ASK UX in Phase 2)
- [ ] PostgreSQL + Alembic setup
- [ ] **Scaffold `web/` directory**: copy design system, shadcn primitives, assistant-ui components, and Clerk auth from `feat/web-ui-rebuild` so the frontend skeleton exists from day one

### Phase 2: Multi-Agent + Tools (Weeks 7-10)

- [ ] Agent registry and definition management API
- [ ] Agent router/dispatcher
- [ ] Sub-agent orchestration (max depth 2)
- [ ] Per-agent tool configuration
- [ ] Tool permission full implementation (ASK flow, UI matrix)
- [ ] MCP system with transport strategy (HTTP preferred, stdio supported)
- [ ] Port MCP client with health checks and reconnection
- [ ] Rook v2 ingestion pipeline (dedup + supersede)

### Phase 3: Platform Adapters (Weeks 11-14)

- [ ] Port Discord adapter to new transport layer
- [ ] Port other adapters (Slack, Teams, Telegram)
- [ ] Unified streaming protocol
- [ ] Port sandbox system
- [ ] CLI adapter
- [ ] Rook v2 FSRS dynamics + cross-agent memory

### Phase 4: Multi-Input (Weeks 15-18)

- [ ] API gateway (REST + WebSocket for external clients)
- [ ] Loopback dispatcher (§6.4)
- [ ] Scheduler event source (cron + one-shot + conversational creation)
- [ ] Webhook event source
- [ ] Voice processing pipeline
- [ ] Vision/document processing
- [ ] Port email monitoring as event source

### Phase 5: Web UI & Polish (Weeks 19-22)

Phase 5 is primarily a **port and evolve**, not a build-from-scratch. The `feat/web-ui-rebuild` branch contains a functional React app with chat streaming, knowledge base, settings, and an identity service. The work here is adapting it for multi-tenant, multi-agent MyPal.

#### 5a. Foundation Port (Week 19)

Copy and adapt from `feat/web-ui-rebuild`:

- [ ] **Copy `web-ui/frontend/` → `web/`** as the base
- [ ] **Design system** (`index.css`): copy as-is — oklch tokens, light/dark mode, shadcn bindings, scrollbar/Tiptap styles are all keepers
- [ ] **`components/ui/`**: copy as-is — shadcn primitives need zero changes
- [ ] **`components/assistant-ui/`**: copy as-is — thread, markdown, attachment, tool-fallback components are generic
- [ ] **Keep Clerk** (`@clerk/react`): the auth provider, `useAuth`/`useUser` hooks, and sign-in components stay. Clerk handles web UI auth; the identity service handles platform pairing only
- [ ] **Wire Clerk → CanonicalUser**: on first Clerk sign-in, create/link a CanonicalUser with `platform="clerk"` PlatformLink. Clerk's `userId` becomes the lookup key
- [ ] **Identity service → `mypal/auth/pairing.py`**: port `PlatformLink`, `ensure-link`, OAuth token storage. This does NOT handle web auth (Clerk does that) — only platform identity mapping
- [ ] **FastAPI serves frontend**: static file serving in production, Vite proxy in dev
- [ ] **Update `api/client.ts`**: add `tenant_id` header, add agent endpoints, update memory types for Rook v2

#### 5b. Chat Evolution (Week 20)

- [ ] **Port `useGatewayWebSocket`**: the hook is production-ready — JWT auth, heartbeat, exponential backoff reconnect, session recovery. Clerk's `getToken()` already provides the JWT — keep it as-is. Backend verifies Clerk JWT via JWKS and resolves to CanonicalUser
- [ ] **Port `chatStore.ts`**: add `agentId` to state, scope threads by agent, add agent switching
- [ ] **Port `ChatRuntimeProvider`**: the assistant-ui ↔ Zustand bridge works. Add agent context to message payload
- [ ] **New `agentStore.ts`**: active agent selection, agent list, agent switching from sidebar
- [ ] **Port `BranchSidebar`**: conversation branching with agent-awareness
- [ ] **Port `TierSelector`**: model tier selection (works as-is, already per-message)
- [ ] **Port `ToolCallBlock`**: tool execution display (works as-is)
- [ ] **New `AgentSwitcher` component**: sidebar dropdown to switch active agent

#### 5c. Knowledge Base Evolution (Week 21)

- [ ] **Port `pages/KnowledgeBase.tsx`**: the page structure (search, category filter, grid/list toggle, saved filter sets, export/import) is solid
- [ ] **Update `useMemories` hook**: point at Rook v2 API endpoints, add scope and agent filters
- [ ] **Update `MemoryCard`**: add scope badge, agent attribution badge, FSRS retrievability gauge (the existing dynamics fields `stability`, `difficulty`, `retrieval_strength` map closely)
- [ ] **Update `MemoryEditor`**: add scope selector, supersession chain display, confidence slider
- [ ] **New permission-aware memory UI**: only show memories the current user has access to (USER_AGENT private, TENANT shared, etc.)

#### 5d. Admin & Settings (Week 22)

- [ ] **Port `pages/Settings.tsx`**: expand with tenant settings, agent management
- [ ] **Port `AdapterLinking`**: this is the pairing UI — expand for pairing modes (open/approval/invite/closed)
- [ ] **New `pages/Permissions.tsx`**: tool permission matrix (users × tools, Allow/Deny/Ask toggles)
- [ ] **New `pages/Onboarding.tsx`**: 6-step tenant setup wizard (§12.1)
- [ ] **Dynamic settings form**: show/hide fields based on enabled capabilities
- [ ] **Port game logic to Python** (if applicable — separate from web UI)

### Phase 6: Multi-Tenancy & Production (Weeks 23-26)

- [ ] Tenant management API
- [ ] Full RBAC enforcement
- [ ] Rate limiting and quotas
- [ ] Data export/import
- [ ] mem0 migration tool
- [ ] Monitoring and observability

### 12.1 Existing Code Inventory (from feat/web-ui-rebuild)

The following table maps existing files to their MyPal fate:

| Source File | Action | MyPal Destination | Changes Needed |
|---|---|---|---|
| `index.css` | **Copy as-is** | `web/src/index.css` | None — design tokens are brand identity |
| `components/ui/*` | **Copy as-is** | `web/src/components/ui/` | None — generic shadcn primitives |
| `components/assistant-ui/*` | **Copy as-is** | `web/src/components/assistant-ui/` | None — generic chat UI |
| `hooks/useGatewayWebSocket.ts` | **Copy as-is** | `web/src/hooks/useGatewayWebSocket.ts` | None — Clerk token source stays |
| `hooks/useTheme.ts` | **Copy as-is** | `web/src/hooks/useTheme.ts` | None |
| `hooks/useBranches.ts` | **Port (minor)** | `web/src/hooks/useBranches.ts` | Add agent_id scoping |
| `hooks/useMemories.ts` | **Port (moderate)** | `web/src/hooks/useMemories.ts` | Point at Rook v2 API, add scope filters |
| `stores/chatStore.ts` | **Port (moderate)** | `web/src/stores/chatStore.ts` | Add agentId, scope threads by agent |
| `stores/chatRuntime.ts` | **Port (minor)** | `web/src/stores/chatRuntime.ts` | Add agent context to message conversion |
| `stores/artifactStore.ts` | **Copy as-is** | `web/src/stores/artifactStore.ts` | None |
| `stores/savedSets.ts` | **Copy as-is** | `web/src/stores/savedSets.ts` | None |
| `api/client.ts` | **Port (moderate)** | `web/src/api/client.ts` | Add tenant header, agent endpoints, Rook v2 memory types |
| `lib/utils.ts` | **Copy as-is** | `web/src/lib/utils.ts` | None |
| `lib/attachmentAdapter.ts` | **Copy as-is** | `web/src/lib/attachmentAdapter.ts` | None |
| `lib/threadGroups.ts` | **Port (minor)** | `web/src/lib/threadGroups.ts` | Add agent grouping |
| `utils/fileProcessing.ts` | **Copy as-is** | `web/src/utils/fileProcessing.ts` | None |
| `components/chat/ChatRuntimeProvider.tsx` | **Port (minor)** | Same path | Add agent context |
| `components/chat/BranchSidebar.tsx` | **Port (minor)** | Same path | Agent-aware branching |
| `components/chat/TierSelector.tsx` | **Copy as-is** | Same path | None |
| `components/chat/ToolCallBlock.tsx` | **Copy as-is** | Same path | None |
| `components/chat/ArtifactPanel.tsx` | **Copy as-is** | Same path | None |
| `components/chat/MergeDialog.tsx` | **Copy as-is** | Same path | None |
| `components/knowledge/*` | **Port (moderate)** | Same path | Add scope, agent, FSRS fields to MemoryCard/Editor |
| `components/layout/AppLayout.tsx` | **Port (minor)** | Same path | Add AgentSwitcher to sidebar |
| `components/layout/UnifiedSidebar.tsx` | **Port (minor)** | Same path | Add agent list, tenant indicator |
| `components/settings/AdapterLinking.tsx` | **Port (moderate)** | Same path | Expand for pairing modes |
| `pages/Chat.tsx` | **Port (minor)** | Same path | Wrap in agent context |
| `pages/KnowledgeBase.tsx` | **Port (moderate)** | Same path | Add scope/agent filters |
| `pages/Settings.tsx` | **Port (moderate)** | Same path | Expand for tenant/agent management |
| `App.tsx` | **Port (minor)** | Same path | Keep Clerk auth guard, add routes for Permissions + Onboarding |
| `main.tsx` | **Copy as-is** | Same path | ClerkProvider stays |
| `auth/ClerkProvider.tsx` | **Copy as-is** | Same path | Keep — Clerk handles web auth |
| `auth/TokenBridge.tsx` | **Copy as-is** | Same path | Keep — bridges Clerk token to API client |
| — | **New** | `stores/agentStore.ts` | Agent selection + switching |
| — | **New** | `pages/Permissions.tsx` | Tool permission matrix UI |
| — | **New** | `pages/Onboarding.tsx` | 6-step setup wizard |
| — | **New** | `components/layout/AgentSwitcher.tsx` | Agent dropdown in sidebar |
| — | **New** | `components/admin/OnboardingWizard.tsx` | Multi-step wizard component |

### 12.2 Identity Service Migration (Clerk Hybrid)

The `identity/` service from `feat/web-ui-rebuild` is partially superseded by keeping Clerk, but the platform-pairing parts are essential:

| Identity Service Has | MyPal Fate | Reasoning |
|---|---|---|
| JWT issuance (`jwt_service.encode/decode`) | **Drop** — Clerk handles this | Clerk manages web session tokens; no need for custom JWT |
| `/oauth/authorize` + `/oauth/callback` | **Keep for platform OAuth** | Google Workspace, GitHub tokens — Clerk doesn't manage these |
| `CanonicalUser` model | **Keep + evolve** | Add `tenant_id`, `role`. Clerk's `userId` becomes a PlatformLink entry |
| `PlatformLink` model | **Keep as-is** | Core of the pairing system. Clerk is just another platform (`platform="clerk"`) |
| `OAuthToken` model | **Keep** | Stores platform API tokens (Google, GitHub). Separate from Clerk's auth tokens |
| `find_or_create_user()` | **Keep for adapter flows** | When Discord adapter sees a new user, this creates the CanonicalUser |
| `/users/ensure-link` | **Keep** | Auto-pairing endpoint for adapters. Add pairing modes |
| `/users/me` | **Evolve** | Web UI calls Clerk for user profile; this endpoint serves platform link data |
| `/auth/config` | **Drop** | Clerk handles provider configuration |

**The key architectural move:** When a user first signs into the web UI via Clerk, a webhook or middleware creates a `CanonicalUser` and a `PlatformLink(platform="clerk", platform_user_id=clerk_user_id)`. From that point, the CanonicalUser is what the rest of MyPal uses — agent runtimes, memory scoping, tool permissions all reference `canonical_user_id`, never `clerk_user_id` directly.

The identity service becomes `mypal/auth/pairing.py` — a module, not a separate service. It handles platform identity operations while Clerk handles web authentication.

### 12.3 Tenant Onboarding Wizard

Six-step guided setup for new tenants:

```
Step 1: Your Space       → Tenant name, admin user, email, password
Step 2: Your First Agent → Agent name, persona preset, profile picture
Step 3: AI Provider      → Provider selection, API key, model, "test connection"
Step 4: Connect Channel  → Discord/Telegram/Slack/API with guided setup per platform
Step 5: Capabilities     → Toggle: memory, web search, code execution, files, MCP, tasks
Step 6: Ready!           → Summary, "Open Chat", "Invite Users", "Go to Dashboard"
```

Every step after Step 1 is skippable with sensible defaults. Progress is saved (resumable if browser closes). For users migrating from MyPalClara, an optional import step surfaces between Steps 1 and 2.

Settings page uses dynamic form rendering — fields show/hide based on what's enabled.

---

## 13. Design Principles & Lessons Learned

### 13.1 Core Principles

1. **Agents are data, not code.** Creating a new agent is an API call, not a deployment.
2. **Tenant isolation by default.** Every query, every memory access, every tool invocation is tenant-scoped.
3. **Memory is the moat.** Rook v2 is the differentiator. Rich, scoped, relationship-aware memory that makes agents genuinely personal.
4. **Input-agnostic processing.** An agent shouldn't care if the message came from Discord, an API call, or a webhook. Same pipeline.
5. **Sub-agents are just agents.** No special class. Any agent can be invoked by another with scoped context.
6. **Progressive complexity.** Single user, single agent, single platform works out of the box. Multi-tenant, multi-agent, multi-input is additive configuration.
7. **No autonomous feedback loops.** Every cognitive stage is anchored to an event. Reflection writes outward to memory only — no re-entry into the pipeline. (The ORS lesson.)
8. **LLMs select from pre-computed options, not generate from scratch** in structured domains (tools, game moves).

### 13.2 Key Lessons

**The ORS failure** is foundational. Autonomous feedback loops with LLM cognition cause hallucination amplification and runaway cycles. Sub-agent depth is capped at 2. Iteration limits prevent infinite tool loops. Scheduled tasks go through the same bounded pipeline as user messages.

**The Cortex failure** was not a design problem — it was a testing problem. Every storage path in Rook v2 has a round-trip integration test.

**The mem0 limitation** is opacity. Users must be able to see, search, edit, and export what the agent remembers about them.

**Simpler is right for this scale.** Enterprise patterns (gRPC, message queues, separate databases per tenant) solve problems that don't apply yet. Row-level isolation, arq task queues, and a single PostgreSQL instance are correct until proven otherwise.

---

## 14. Open Questions

1. **Agent-to-agent communication**: Start with sub-agent delegation only. An event bus between agents is the ORS problem repackaged. Delegation is bounded; event bus is fire-and-forget.
2. **Memory migration**: Build import tooling. Don't auto-migrate. Let users review and curate.
3. **Billing/quotas**: If multi-tenant goes beyond personal use, meter LLM usage per tenant via token counting in the LLM provider layer.
4. **Real-time collaboration**: Multiple users seeing each other interacting with an agent live. Defer until single-user experience is solid.
5. **Agent marketplace**: Sharing/publishing agent definitions. Defer to post-launch.
6. **Retrieval scoring weights**: The `similarity * 0.5 + retrievability * 0.3 + importance * 0.2` formula needs tuning with real data. Make weights configurable per-agent.
7. **Embedding dimension reduction**: text-embedding-3-small supports Matryoshka truncation to 512/256 dimensions. Worth benchmarking for storage/speed vs precision tradeoff.
8. **Graph as primary vs supplementary**: Keep graph supplementary until relationship queries are a proven primary use case.
9. **Memory consolidation**: Periodically summarizing many observations into one ("user is actively learning Python"). Phase 2+.

---

## 15. Implementation Priority (Full Breakdown)

Every item from the architecture, organized by phase and priority within each phase.

### Phase 1: Foundation (Weeks 1-6)

| # | Component | Section | Existing Asset | Priority |
|---|-----------|---------|---------------|----------|
| 1.1 | FastAPI skeleton + project structure | §11 | — | P0 |
| 1.2 | Clerk JWT verification (JWKS middleware) | §8.2 | **Clerk integrated** in web-ui | P0 |
| 1.3 | Service secret auth for adapter requests | §8.2 | — | P0 |
| 1.4 | Tenant model + CRUD | §4.1 | — | P0 |
| 1.5 | User model (evolve from CanonicalUser) | §4.2 | **CanonicalUser** — add tenant_id + role | P0 |
| 1.6 | PlatformLink model + ensure-link endpoint | §3.5, §12.2 | **PlatformLink + ensure-link** from identity service | P0 |
| 1.7 | Clerk → CanonicalUser bridge (platform="clerk") | §8.2 | — | P0 |
| 1.8 | AgentDefinition model | §3.1 | — | P0 |
| 1.9 | Session model (agent-scoped) | §9.1 | — | P0 |
| 1.10 | Pairing service with modes (open/approval/invite/closed) | §3.5.3 | **ensure-link** as base | P0 |
| 1.11 | Cross-platform identity linking (verification flow) | §3.5.4 | — | P1 |
| 1.12 | PostgreSQL + Alembic setup | §10.3 | — | P0 |
| 1.13 | Tenant row-level isolation middleware | §4.3 | — | P0 |
| 1.14 | LLM provider system (per-agent config) | §2.2 | Port from `mypalclara/core/` | P0 |
| 1.15 | Rook v2: memories table schema + indexes | §5.4 | — | P0 |
| 1.16 | Rook v2: session_summaries table | §5.4 | — | P0 |
| 1.17 | Rook v2: memory CRUD operations | §5.2 | Memory API types in `api/client.ts` | P0 |
| 1.18 | Rook v2: embedding generation (text-embedding-3-small) | §5.4 | — | P0 |
| 1.19 | Rook v2: pgvector HNSW index + vector search | §5.4 | — | P0 |
| 1.20 | Rook v2: round-trip integration tests (every scope) | §5.11 | — | P0 |
| 1.21 | Basic agent runtime (single agent, text only) | §3.2 | — | P0 |
| 1.22 | Tool permission table schema + resolver | §8.3.4 | — | P0 |
| 1.23 | Role-based tool defaults (Guest/Member/Admin/Owner) | §8.3.2 | — | P1 |
| 1.24 | Scaffold `web/` directory from feat/web-ui-rebuild | §12.1 | **Full React app** — copy directly | P0 |
| 1.25 | Wire frontend to FastAPI (Vite proxy in dev, static in prod) | §12.1 5a | — | P1 |

### Phase 2: Multi-Agent + Tools (Weeks 7-10)

| # | Component | Section | Existing Asset | Priority |
|---|-----------|---------|---------------|----------|
| 2.1 | Agent registry + definition management API | §3.1 | — | P0 |
| 2.2 | Agent router / dispatcher | §3.4 | — | P0 |
| 2.3 | Agent selection logic (mention, channel binding, default, continuity) | §3.4 | — | P0 |
| 2.4 | Sub-agent orchestration (max depth 2) | §3.3 | — | P1 |
| 2.5 | Sub-agent context scoping (no USER_AGENT leakage) | §5.8 | — | P1 |
| 2.6 | Per-agent tool configuration | §3.1 | — | P0 |
| 2.7 | Tool permission full implementation (Allow/Deny/Ask) | §8.3 | Schema from Phase 1 | P0 |
| 2.8 | Ask permission flow (mid-turn interruption) | §8.3.3 | — | P1 |
| 2.9 | Permission filtering at LLM context build (Point A) | §8.3.5 | — | P0 |
| 2.10 | Permission checking at tool execution (Point B) | §8.3.5 | — | P0 |
| 2.11 | Tool permission API endpoints | §8.3.6 | — | P1 |
| 2.12 | MCP server config model | §7.3 | **MCPServerConfig** from current Clara | P0 |
| 2.13 | MCP transport strategy (HTTP preferred, stdio supported) | §7.2 | — | P0 |
| 2.14 | MCP health checks + reconnection with backoff | §7.4 | — | P1 |
| 2.15 | MCP OAuth 2.1 flow for HTTP servers | §7.3 | Port from `clara_core/mcp/oauth.py` | P1 |
| 2.16 | Tool loop detection (configurable iteration limits) | §13.1 | — | P0 |
| 2.17 | Rook v2: ingestion pipeline — normalize step | §5.6 | — | P0 |
| 2.18 | Rook v2: ingestion pipeline — dedup check (0.92 threshold) | §5.6 | — | P0 |
| 2.19 | Rook v2: ingestion pipeline — supersede check (0.7 + LLM) | §5.6 | — | P1 |
| 2.20 | Rook v2: ingestion cost guards (rate limit, cheap model, skip session) | §5.6 | — | P1 |
| 2.21 | Rook v2: scoped retrieval (multi-scope query) | §5.3, §5.7 | — | P0 |
| 2.22 | Rook v2: token budget allocation | §5.7 | — | P0 |
| 2.23 | Rook v2: quick context vs full context (two-tier retrieval) | §5.7 | — | P0 |
| 2.24 | Rook v2: Redis caching layer (identity, session, hot memories) | §5.4 | — | P1 |
| 2.25 | Rook v2: Redis fallback (Postgres-only if Redis down) | §5.4 | — | P1 |
| 2.26 | Rook v2: memory extraction from conversations (Reflect stage) | §5.9 | — | P1 |
| 2.27 | Rook v2: extraction cost controls (cheap model, skip trivial, rate limit) | §5.9 | — | P1 |
| 2.28 | Composable prompt system (persona.md, tools.md, user.md) | §1.2 | — | P1 |

### Phase 3: Platform Adapters (Weeks 11-14)

| # | Component | Section | Existing Asset | Priority |
|---|-----------|---------|---------------|----------|
| 3.1 | Adapter base class + protocol | §6.2 | Port from `mypalclara/adapters/base.py` | P0 |
| 3.2 | Discord adapter | §6.2 | Port from `mypalclara/adapters/discord/` | P0 |
| 3.3 | Unified streaming protocol | §6.2 | Port from `mypalclara/gateway/llm_orchestrator.py` | P0 |
| 3.4 | Slack adapter | §6.2 | — | P1 |
| 3.5 | Teams adapter | §6.2 | Port from `mypalclara/adapters/teams/` | P1 |
| 3.6 | Telegram adapter | §6.2 | — | P2 |
| 3.7 | CLI adapter | §6.2 | Port from `mypalclara/adapters/cli/` | P1 |
| 3.8 | Sandbox system (Docker/Incus/Remote) | §1.1 | Port from `mypalclara/sandbox/` | P1 |
| 3.9 | Rook v2: FSRS state machine (new → learning → review → relearning) | §5.5 | — | P0 |
| 3.10 | Rook v2: FSRS retrieval-time scoring | §5.5 | — | P0 |
| 3.11 | Rook v2: reinforcement signals (retrieved, referenced, confirmed, corrected) | §5.5 | — | P1 |
| 3.12 | Rook v2: scope-specific decay multipliers | §5.5 | — | P1 |
| 3.13 | Rook v2: cross-agent memory sharing | §5.8 | — | P1 |
| 3.14 | Rook v2: cross-agent permission model (PRIVATE, TENANT_READ, etc.) | §5.8 | — | P1 |
| 3.15 | Rook v2: sub-agent memory inheritance | §5.8 | — | P2 |
| 3.16 | Rook v2: memory cleanup background job (soft-delete faded/superseded) | §5.5 | — | P2 |
| 3.17 | Group conversation model | §9.2 | — | P2 |
| 3.18 | Multi-agent conversations (multiple agents in one channel) | §3.4 | — | P2 |

### Phase 4: Multi-Input (Weeks 15-18)

| # | Component | Section | Existing Asset | Priority |
|---|-----------|---------|---------------|----------|
| 4.1 | API gateway (REST endpoints) | §6.3 | — | P0 |
| 4.2 | API gateway (WebSocket streaming for external clients) | §6.3 | — | P0 |
| 4.3 | Loopback dispatcher | §6.4 | — | P0 |
| 4.4 | Loopback response routing (target_channel) | §6.4 | — | P0 |
| 4.5 | Scheduler event source (cron expressions) | §6.4 | Port from `mypalclara/gateway/scheduler.py` | P0 |
| 4.6 | Scheduler event source (one-shot ISO 8601) | §6.4 | — | P0 |
| 4.7 | Scheduler event source (interval) | §6.4 | — | P1 |
| 4.8 | Scheduled task model + CRUD API | §6.4 | — | P0 |
| 4.9 | Conversational task creation tool (agent creates tasks via tool) | §6.4 | — | P1 |
| 4.10 | Webhook event source | §6.4 | — | P1 |
| 4.11 | Webhook registration API | §6.3 | — | P1 |
| 4.12 | Email event source | §6.4 | Port from `mypalclara/services/email/` | P2 |
| 4.13 | File watch event source | §6.4 | — | P2 |
| 4.14 | Voice processing pipeline (STT → text) | §6.5 | — | P2 |
| 4.15 | Vision processing pipeline (image analysis, OCR) | §6.5 | — | P2 |
| 4.16 | Document processing pipeline (PDF/doc parsing) | §6.5 | — | P2 |
| 4.17 | Tenant-scoped API keys | §8.2 | — | P1 |
| 4.18 | IncomingMessage unified format enforcement | §6.1 | — | P0 |

### Phase 5: Web UI & Polish (Weeks 19-22)

| # | Component | Section | Existing Asset | Priority |
|---|-----------|---------|---------------|----------|
| **5a. Foundation Port (Week 19)** | | | | |
| 5.1 | Copy design system (index.css oklch tokens) | §12.1 | **Copy as-is** | P0 |
| 5.2 | Copy shadcn/ui primitives (components/ui/) | §12.1 | **Copy as-is** (18 components) | P0 |
| 5.3 | Copy assistant-ui chat components | §12.1 | **Copy as-is** | P0 |
| 5.4 | Keep Clerk auth (ClerkProvider, TokenBridge) | §12.1 | **Copy as-is** | P0 |
| 5.5 | Wire Clerk → CanonicalUser on first sign-in | §8.2, §12.2 | — | P0 |
| 5.6 | Update api/client.ts (tenant header, agent endpoints, Rook v2 types) | §12.1 | **Port (moderate)** | P0 |
| 5.7 | FastAPI serves frontend (static prod, Vite proxy dev) | §12.1 5a | — | P0 |
| **5b. Chat Evolution (Week 20)** | | | | |
| 5.8 | Port useGatewayWebSocket (copy as-is, Clerk token stays) | §12.1 | **Copy as-is** | P0 |
| 5.9 | Port chatStore.ts (add agentId, scope threads by agent) | §12.1 | **Port (moderate)** | P0 |
| 5.10 | Port ChatRuntimeProvider (add agent context to messages) | §12.1 | **Port (minor)** | P0 |
| 5.11 | New agentStore.ts (agent selection, switching) | §12.1 | — | P0 |
| 5.12 | New AgentSwitcher component (sidebar dropdown) | §12.1 | — | P0 |
| 5.13 | Port BranchSidebar (agent-aware branching) | §12.1 | **Port (minor)** | P1 |
| 5.14 | Port TierSelector (copy as-is) | §12.1 | **Copy as-is** | P0 |
| 5.15 | Port ToolCallBlock (copy as-is) | §12.1 | **Copy as-is** | P0 |
| 5.16 | Port ArtifactPanel (copy as-is) | §12.1 | **Copy as-is** | P1 |
| 5.17 | Port MergeDialog (copy as-is) | §12.1 | **Copy as-is** | P1 |
| **5c. Knowledge Base Evolution (Week 21)** | | | | |
| 5.18 | Port KnowledgeBase page (grid/list, search, filter, saved sets) | §12.1 | **Port (moderate)** | P0 |
| 5.19 | Update useMemories hook (Rook v2 API, scope + agent filters) | §12.1 | **Port (moderate)** | P0 |
| 5.20 | Update MemoryCard (scope badge, agent badge, FSRS retrievability gauge) | §12.1 | **Port (moderate)** | P0 |
| 5.21 | Update MemoryEditor (scope selector, supersession chain, confidence) | §12.1 | **Port (moderate)** | P0 |
| 5.22 | Permission-aware memory UI (respect scope visibility) | §5.8, §12.1 | — | P1 |
| 5.23 | Memory export (Markdown + JSON) | §5.10 | **Export exists** — add Markdown format | P1 |
| 5.24 | Memory import (bulk with ingestion pipeline) | §5.10 | **Import exists** — wire to Rook ingestion | P1 |
| **5d. Admin & Settings (Week 22)** | | | | |
| 5.25 | Port Settings page (expand for tenant + agent management) | §12.1 | **Port (moderate)** | P0 |
| 5.26 | Port AdapterLinking (expand for pairing modes) | §12.1 | **Port (moderate)** | P0 |
| 5.27 | New Permissions page (tool permission matrix UI) | §8.3.7 | — | P0 |
| 5.28 | New Onboarding page (6-step tenant setup wizard) | §12.3 | — | P1 |
| 5.29 | Dynamic settings form (show/hide based on enabled capabilities) | §12.3 | — | P1 |
| 5.30 | Port AppLayout + UnifiedSidebar (add agent list, tenant indicator) | §12.1 | **Port (minor)** | P0 |
| 5.31 | Update App.tsx routes (add Permissions, Onboarding) | §12.1 | **Port (minor)** | P0 |
| 5.32 | Port game logic to Python | §10 | — | P2 |

### Phase 6: Multi-Tenancy & Production (Weeks 23-26)

| # | Component | Section | Existing Asset | Priority |
|---|-----------|---------|---------------|----------|
| 6.1 | Tenant management API (CRUD, settings, feature flags) | §4.1 | — | P0 |
| 6.2 | Tenant onboarding API endpoints (wizard state tracking) | §12.3 | — | P0 |
| 6.3 | Full RBAC enforcement (Owner/Admin/Member/Guest on all endpoints) | §8.1 | — | P0 |
| 6.4 | Rate limiting per-tenant (messages/hour, tokens/day) | §8.1 | — | P0 |
| 6.5 | Usage quotas per plan tier (FREE/PRO/ENTERPRISE) | §4.1 | — | P1 |
| 6.6 | mem0 → Rook v2 migration tool (preview + selective import) | §5.12 | — | P0 |
| 6.7 | Data export (tenant data, memories, sessions) | §14 | — | P1 |
| 6.8 | Data import (from export or external sources) | §14 | — | P1 |
| 6.9 | Monitoring and observability (structured logging, metrics) | — | — | P1 |
| 6.10 | Database backup automation | — | Port from `mypalclara/scripts/backup_databases.py` | P1 |
| 6.11 | Docker Compose for production deployment | — | Port from `mypalclara/docker-compose.yml` | P0 |
| 6.12 | CI/CD pipeline | — | — | P1 |

### Future (Unscheduled)

| # | Component | Section | Notes |
|---|-----------|---------|-------|
| F.1 | FalkorDB graph integration | §5.4 | Optional per-tenant, relationship tracking |
| F.2 | Self-hosted embeddings (SentenceTransformers, Ollama) | §5.4 | Cost/privacy optimization |
| F.3 | Memory consolidation/summarization | §14 | 50 observations → 1 summary |
| F.4 | Spreading activation for graph retrieval | §5.13 | Collins & Loftus 1975, ACT-R |
| F.5 | Prediction error gating | §5.13 | Surprise-based encoding boost |
| F.6 | Tenant data residency (region-specific DBs) | §14 | When multi-region is needed |
| F.7 | Agent marketplace (share/publish definitions) | §14 | Post-launch |
| F.8 | Real-time collaboration (multi-user live view) | §14 | Post-launch |
| F.9 | Embedding dimension reduction (Matryoshka 512/256) | §14 | Benchmark when data exists |
| F.10 | FSRS retrieval scoring weight tuning | §14 | Per-agent configurable weights |
| F.11 | Desktop app (Tauri + React) | — | Reuse web/ frontend |
| F.12 | Voice input/output (real-time STT/TTS) | §6.5 | Beyond batch processing |
| F.13 | Billing/quota metering (LLM token counting) | §14 | When multi-tenant goes commercial |

---

**Totals:** 97 line items across 6 phases + future. Phase 1 has 25 items, Phase 2 has 28, Phase 3 has 18, Phase 4 has 18, Phase 5 has 32, Phase 6 has 12, Future has 13.

---

*MyPal is a unified platform where agents are personal, memory is transparent, and every input — from a Discord message to a cron job — follows the same path. The architecture gets better in ways you can see and measure.*
