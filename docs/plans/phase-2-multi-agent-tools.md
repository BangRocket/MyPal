# Phase 2: Multi-Agent + Tools

**Timeframe:** Weeks 7-10
**Depends on:** Phase 1 complete (FastAPI app, core models, auth, basic agent runtime, Rook v2 core, LLM providers)
**Architecture reference:** `docs/MyPal_Master_Architecture.md` (sections 3, 5.6-5.9, 7, 8.3)

---

## Prerequisites (Phase 1 Complete)

Before starting Phase 2, the following must be verified:

- FastAPI app boots and serves `/api/v1/chat` with a single hardcoded agent
- All core DB models exist: `tenants`, `users`, `agents`, `sessions`, `messages`, `memories`
- Clerk JWT verification middleware works for web requests
- Basic `AgentRuntime` processes text messages end-to-end (context build, LLM call, response)
- Rook v2 core: memories table with pgvector, embedding, basic store/retrieve round-trip passing
- LLM providers (Anthropic + OpenAI) respond to completions with tool support
- `tool_permissions` table exists (schema from Phase 1, resolver logic in Phase 2)
- Alembic migrations running cleanly

---

## Stage 2A: Agent Registry

### WI-2.1: Agent Definition Management Service

- **Depends on:** Phase 1 (agents table, AgentDefinition model)
- **Port from:** New (MyPalClara had no agent registry -- agents were hardcoded)
- **Deliverables:**
  - `mypal/agents/service.py` -- AgentService with CRUD operations
  - `mypal/agents/schemas.py` -- Pydantic request/response schemas for API layer
- **Acceptance criteria:**
  - `AgentService.create()` persists an agent definition with all fields from `AgentDefinition` (id, tenant_id, name, persona, llm_config, memory_config, tools, capabilities, sub_agents, max_concurrent, metadata)
  - `AgentService.get()` returns an agent scoped to the requesting tenant
  - `AgentService.update()` patches mutable fields (persona, llm_config, tools, capabilities, sub_agents, metadata) without replacing the entire record
  - `AgentService.delete()` soft-deletes (sets `is_active=False`), does not remove rows
  - `AgentService.list()` returns paginated agents for a tenant, excludes soft-deleted
  - All operations enforce `tenant_id` scoping -- no cross-tenant access
  - Unit tests: create, get, update, soft-delete, list with pagination, cross-tenant rejection
- **Estimated effort:** M

### WI-2.2: Built-in Persona Configurations

- **Depends on:** WI-2.1
- **Port from:** New (persona concept is new; Clara's personality was in system prompt strings)
- **Deliverables:**
  - `mypal/agents/personas/clara.py` -- Clara default persona config
  - `mypal/agents/personas/__init__.py` -- `get_builtin_personas()` registry
  - `mypal/agents/seed.py` -- seed function that ensures built-in agents exist on startup
- **Acceptance criteria:**
  - Clara persona includes: system prompt, personality traits, tone descriptors, default LLM config (model, tier, temperature), default tool list, capabilities set (CHAT)
  - `seed_builtin_agents(tenant_id)` creates Clara agent for a tenant if not already present (idempotent)
  - Seeding runs automatically during tenant creation
  - Built-in agents are marked `is_builtin=True` and cannot be deleted (soft-delete rejected)
  - Test: seed twice for same tenant, verify only one Clara agent exists
- **Estimated effort:** S

### WI-2.3: Agent API Endpoints

- **Depends on:** WI-2.1, WI-2.2
- **Port from:** New
- **Deliverables:**
  - `mypal/api/v1/agents.py` -- FastAPI router
- **Acceptance criteria:**
  - `POST /api/v1/agents` -- create agent, requires ADMIN+ role, returns 201 with agent definition
  - `GET /api/v1/agents` -- list agents for tenant, paginated, any authenticated role
  - `GET /api/v1/agents/{agent_id}` -- get single agent, 404 if not found or wrong tenant
  - `PATCH /api/v1/agents/{agent_id}` -- partial update, requires ADMIN+ role
  - `DELETE /api/v1/agents/{agent_id}` -- soft-delete, requires ADMIN+ role, rejects built-in agents with 403
  - All endpoints enforce tenant isolation via auth middleware
  - Integration tests: full CRUD cycle, role-based rejection (MEMBER cannot create), cross-tenant 404
- **Estimated effort:** M

---

## Stage 2B: Agent Router/Dispatcher

### WI-2.4: Agent Router

- **Depends on:** WI-2.1
- **Port from:** New (MyPalClara gateway hardcoded "Clara" -- `../mypalclara/mypalclara/gateway/tool_executor.py` line 79 shows the single-agent assumption)
- **Deliverables:**
  - `mypal/agents/router.py` -- AgentRouter class
- **Acceptance criteria:**
  - Router implements the five-step resolution chain from architecture doc section 3.4:
    1. Explicit mention: message contains `@agent_name` or `agent_id` is set on IncomingMessage
    2. Channel binding: channel has a configured default agent
    3. DM default: user's `preferences.default_agent_id`
    4. Tenant default: tenant's configured default agent
    5. Conversation continuity: last agent that responded in this conversation
  - Resolution short-circuits on first match (does not evaluate remaining steps)
  - Returns `AgentDefinition` or raises `NoAgentFoundError` if all steps fail
  - Explicit mention parsing handles: `@Clara`, `@clara`, `@Rex` (case-insensitive, matches against agent names in tenant)
  - Test: each resolution step in isolation; priority ordering (explicit > channel > DM > tenant > continuity); no-match error
- **Estimated effort:** M

### WI-2.5: Channel-Agent Binding Configuration

- **Depends on:** WI-2.4
- **Port from:** New
- **Deliverables:**
  - `mypal/agents/bindings.py` -- ChannelBindingService
  - DB migration: `channel_agent_bindings` table (tenant_id, channel_id, agent_id, created_at)
- **Acceptance criteria:**
  - `ChannelBindingService.bind(tenant_id, channel_id, agent_id)` creates or updates a binding
  - `ChannelBindingService.unbind(tenant_id, channel_id)` removes a binding
  - `ChannelBindingService.get_agent(tenant_id, channel_id)` returns bound agent_id or None
  - AgentRouter from WI-2.4 queries this service for step 2
  - API endpoints: `PUT /api/v1/channels/{channel_id}/agent` and `DELETE /api/v1/channels/{channel_id}/agent` (ADMIN+ role)
  - Test: bind, resolve, unbind, re-resolve falls through to next step
- **Estimated effort:** S

### WI-2.6: Wire Router into Chat Endpoint

- **Depends on:** WI-2.4, WI-2.5
- **Port from:** New (replaces hardcoded agent in Phase 1 chat endpoint)
- **Deliverables:**
  - Modified `mypal/api/v1/chat.py` -- replace hardcoded agent lookup with AgentRouter
  - Modified `mypal/agents/runtime.py` -- accept router-resolved AgentDefinition
- **Acceptance criteria:**
  - `POST /api/v1/chat` resolves agent via router before creating AgentRuntime
  - Message with `agent_id` field bypasses router (explicit selection)
  - Message without `agent_id` goes through full resolution chain
  - Response includes `agent_id` and `agent_name` in metadata so client knows who responded
  - Integration test: send message to channel with binding, verify correct agent responds; send message with explicit agent_id, verify that agent responds; send DM, verify user's default agent responds
- **Estimated effort:** M

---

## Stage 2C: Sub-Agent Orchestration

### WI-2.7: SubAgentOrchestrator

- **Depends on:** WI-2.1, WI-2.6 (agent registry + routing working)
- **Port from:** `../mypalclara/mypalclara/gateway/tool_executor.py` (lines 46-82, subagent runner/registry pattern), `../mypalclara/mypalclara/core/subagent/` (if exists)
- **Deliverables:**
  - `mypal/agents/orchestrator.py` -- SubAgentOrchestrator class
- **Acceptance criteria:**
  - `orchestrator.run(agent_id, task, parent_context)` performs the sequence from architecture doc section 3.3:
    1. Load sub-agent definition from registry
    2. Validate sub-agent is in parent agent's `sub_agents` list
    3. Create ephemeral AgentRuntime (no persistent session)
    4. Execute task with scoped context
    5. Return `SubAgentResult(content, metadata, tokens_used)`
  - Ephemeral runtime does not write to session store (no persistent session_id)
  - Sub-agent gets its own LLM config and tool set (from its AgentDefinition)
  - Orchestrator returns result to parent, does not send directly to user
  - Timeout: sub-agent execution capped at 60 seconds, returns timeout error
  - Test: parent calls sub-agent, gets result; sub-agent not in allowed list raises PermissionError; timeout returns error
- **Estimated effort:** L

### WI-2.8: Max Depth Enforcement

- **Depends on:** WI-2.7
- **Port from:** New (architecture doc section 3.3 specifies max depth 2, hard cap)
- **Deliverables:**
  - Modified `mypal/agents/orchestrator.py` -- depth tracking and enforcement
- **Acceptance criteria:**
  - Each `SubAgentOrchestrator.run()` call increments a `depth` counter passed through context
  - Depth 0 = user message to primary agent; depth 1 = sub-agent; depth 2 = sub-sub-agent
  - Attempting depth 3 raises `MaxDepthExceededError` immediately (does not make LLM call)
  - Depth is tracked in context metadata, not global state (supports concurrent conversations)
  - Hard cap: depth limit is not configurable (const `MAX_SUB_AGENT_DEPTH = 2`)
  - Test: chain parent -> sub -> sub-sub succeeds; chain parent -> sub -> sub-sub -> sub-sub-sub raises error
- **Estimated effort:** S

### WI-2.9: Sub-Agent Context Scoping

- **Depends on:** WI-2.7
- **Port from:** New
- **Deliverables:**
  - `mypal/agents/context.py` -- SubAgentContextBuilder
- **Acceptance criteria:**
  - Sub-agents receive a scoped context containing:
    - The task description from the parent
    - Parent agent's relevant context (trimmed to token budget)
    - Sub-agent's own persona/system prompt
  - Sub-agents do NOT receive:
    - Full conversation history (only task-relevant excerpt)
    - USER_AGENT scope memories from other agents (no memory leakage)
    - Parent agent's tool results from current turn
  - Memory writes by sub-agents are scoped to the sub-agent's own agent_id
  - Test: sub-agent context does not contain parent's USER_AGENT memories; sub-agent memory write has sub-agent's agent_id, not parent's
- **Estimated effort:** M

---

## Stage 2D: Tool System

### WI-2.10: Port ToolDef, ToolContext, ToolRegistry

- **Depends on:** Phase 1
- **Port from:** `../mypalclara/mypalclara/tools/_base.py` (139 lines), `../mypalclara/mypalclara/tools/_registry.py` (296 lines)
- **Deliverables:**
  - `mypal/tools/base.py` -- ToolDef, ToolContext dataclasses (ported from `_base.py`)
  - `mypal/tools/registry.py` -- ToolRegistry (simplified, no singleton, no PluginRegistry delegation)
  - `mypal/tools/validation.py` -- `validate_tool_args()` (ported from `_registry.py` lines 22-99)
  - `mypal/tools/__init__.py`
- **Acceptance criteria:**
  - `ToolDef` retains all fields: name, description, parameters (JSON Schema), handler, platforms, requires, risk_level, intent
  - `ToolContext` gains `tenant_id` and `agent_id` fields (multi-tenant extension beyond MyPalClara's single-user context)
  - `ToolDef.to_openai_format()`, `to_claude_format()`, `to_mcp_format()` all produce valid schemas
  - `ToolRegistry` is instantiated per-AgentRuntime (not a singleton) -- each agent gets its own registry scoped to its configured tools
  - `validate_tool_args()` handles string-to-array, string-to-int, string-to-bool coercion (preserving logic from MyPalClara)
  - Test: register tool, retrieve by name, format conversion, argument validation with coercion
- **Estimated effort:** M

### WI-2.11: Port ToolExecutor

- **Depends on:** WI-2.10
- **Port from:** `../mypalclara/mypalclara/gateway/tool_executor.py` (797 lines)
- **Deliverables:**
  - `mypal/tools/executor.py` -- ToolExecutor class
- **Acceptance criteria:**
  - Executor routes tool calls through: registry lookup -> argument validation -> permission check (WI-2.14) -> handler execution -> result return
  - Routes to three sources:
    1. Built-in tools: handler in ToolDef
    2. MCP tools: delegated to MCP client (WI-2.19)
    3. Registry tools: modular tool modules
  - Circuit breaker pattern ported from MyPalClara (`tool_executor.py` lines 33-44, 513-517, 536-537): tracks failures per tool, temporarily disables after threshold
  - Execution timing: logs duration per tool call
  - Error handling: catches exceptions, returns error string (does not crash runtime)
  - Test: execute built-in tool, execute unknown tool returns error, circuit breaker trips after 3 failures
- **Estimated effort:** M

### WI-2.12: Per-Agent Tool Configuration

- **Depends on:** WI-2.10, WI-2.1
- **Port from:** New (MyPalClara injected all tools for every message -- architecture doc section 1.2 changes this)
- **Deliverables:**
  - Modified `mypal/agents/runtime.py` -- build per-agent ToolRegistry from AgentDefinition.tools
  - `mypal/tools/loader.py` -- loads and resolves tool configurations for an agent
- **Acceptance criteria:**
  - `AgentDefinition.tools` is a list of tool IDs/patterns (e.g., `["web_search", "execute_python", "mcp:github"]`)
  - `ToolLoader.load_for_agent(agent_def)` returns a ToolRegistry containing only the declared tools
  - Wildcard patterns supported: `"mcp:*"` includes all MCP tools, `"builtin:*"` includes all built-ins
  - Agent without tool X cannot see or execute tool X (not passed to LLM, rejected if attempted)
  - Test: agent with `["web_search"]` gets only web_search; agent with `["mcp:*"]` gets all MCP tools; attempt to execute non-configured tool returns error
- **Estimated effort:** M

### WI-2.13: Tool Loop Detection

- **Depends on:** WI-2.11
- **Port from:** New (architecture doc section 1.2: "Tool loop detection: Built-in with configurable limits")
- **Deliverables:**
  - `mypal/tools/loop_guard.py` -- ToolLoopGuard class
- **Acceptance criteria:**
  - Tracks tool calls within a single turn (message processing cycle)
  - Default limit: 10 tool calls per turn (configurable per-agent via `AgentDefinition.metadata.max_tool_calls`)
  - Detects repeated identical calls: same tool + same arguments 3 times in a turn triggers abort
  - When limit hit: returns control to LLM with a system message "Tool call limit reached, please respond to the user"
  - Does not persist across turns (resets each message)
  - Test: 10 different tool calls succeed; 11th triggers limit; 3 identical calls trigger repeat detection
- **Estimated effort:** S

---

## Stage 2E: Tool Permissions

### WI-2.14: Permission Resolver

- **Depends on:** WI-2.10 (tool system exists), Phase 1 (tool_permissions table exists)
- **Port from:** `../mypalclara/mypalclara/core/plugins/policies.py` (745 lines -- PolicyEngine, PolicyAction, PolicyContext)
- **Deliverables:**
  - `mypal/tools/permissions.py` -- PermissionResolver class
- **Acceptance criteria:**
  - Implements the five-level resolution chain from architecture doc section 8.3:
    1. User-specific override (most specific)
    2. Agent-specific default
    3. Tenant-wide default
    4. Role-based default (ROLE_TOOL_DEFAULTS dict)
    5. System default = ALLOW (least specific)
  - `resolver.resolve(tool_id, user_id, agent_id, tenant_id, role) -> ToolPolicy`
  - First explicit match wins -- stops evaluating remaining levels
  - ToolPolicy enum: ALLOW, DENY, ASK
  - Role defaults match architecture doc section 8.3: GUEST denies terminal/filesystem_write/sandbox, ASKs browser; MEMBER ASKs terminal/filesystem_write
  - Queries `tool_permissions` table with appropriate filters at each level
  - Caches resolved permissions per (user_id, agent_id, tool_id) for duration of a request (not cross-request)
  - Test: user override wins over agent default; agent default wins over tenant default; role default applies when no explicit rules; system default is ALLOW
- **Estimated effort:** M

### WI-2.15: Pre-LLM Tool Filtering (Point A)

- **Depends on:** WI-2.14, WI-2.12
- **Port from:** `../mypalclara/mypalclara/core/plugins/registry.py` (lines 411-461, get_tools with policy filtering)
- **Deliverables:**
  - Modified `mypal/tools/registry.py` -- `get_tools_for_llm()` method with permission filtering
  - Modified `mypal/agents/runtime.py` -- call filtering before LLM invocation
- **Acceptance criteria:**
  - Before building the LLM tool list, each tool is checked against the permission resolver
  - DENY'd tools: removed entirely from the tool list (LLM never sees them)
  - ASK tools: included in tool list but with description annotation: "(Requires user permission before execution)"
  - ALLOW tools: included normally
  - Filtering happens once per turn (cached resolver from WI-2.14 prevents repeated DB queries)
  - Test: DENY'd tool absent from LLM tool list; ASK tool present with annotation; ALLOW tool present normally
- **Estimated effort:** S

### WI-2.16: Pre-Execution Permission Check (Point B) + ASK Flow

- **Depends on:** WI-2.14, WI-2.11
- **Port from:** `../mypalclara/mypalclara/core/plugins/registry.py` (lines 605-689, execute with policy check)
- **Deliverables:**
  - Modified `mypal/tools/executor.py` -- pre-execution permission check
  - `mypal/tools/ask_flow.py` -- ASK flow implementation
  - Modified `mypal/agents/runtime.py` -- mid-turn interruption support
- **Acceptance criteria:**
  - Before executing any tool, executor checks permission (defense in depth, even though Point A filtered)
  - DENY at Point B: return "Access denied" error to LLM
  - ALLOW at Point B: execute normally
  - ASK at Point B:
    1. Runtime yields a permission request to the user: "Agent wants to use [tool_name] with [summary of args]. Allow? (yes/no, 60s timeout)"
    2. User response "yes"/"go ahead"/"allow" -> execute tool
    3. User response "no"/"deny"/"skip" -> return "User denied permission" to LLM
    4. 60-second timeout with no response -> deny (fail closed)
    5. Runtime resumes processing after user response
  - ASK flow uses WebSocket for real-time prompt/response (for API clients, returns a `permission_required` status that client must respond to)
  - Test: ALLOW executes; DENY rejects; ASK with "yes" executes; ASK with "no" rejects; ASK timeout rejects
- **Estimated effort:** L

### WI-2.17: Tool Permission API Endpoints

- **Depends on:** WI-2.14
- **Port from:** New
- **Deliverables:**
  - `mypal/api/v1/permissions.py` -- FastAPI router for tool permissions
- **Acceptance criteria:**
  - `GET /api/v1/permissions/tools` -- list all tool permissions for tenant (matrix view data), ADMIN+ role
  - `GET /api/v1/permissions/tools/{tool_id}` -- get permissions for a specific tool across all users/agents
  - `PUT /api/v1/permissions/tools/{tool_id}` -- set permission rule (body: user_id, agent_id, policy, reason), ADMIN+ role
  - `DELETE /api/v1/permissions/tools/{tool_id}/{permission_id}` -- remove a specific override, ADMIN+ role
  - Response includes effective policy (resolved) alongside explicit rules so UI can show inherited vs. explicit
  - Validation: `policy` must be one of "allow", "deny", "ask"
  - Test: create permission override, verify resolution changes; delete override, verify fallback to next level
- **Estimated effort:** M

---

## Stage 2F: MCP System

### WI-2.18: MCPServerConfig Model

- **Depends on:** Phase 1 (database)
- **Port from:** `../mypalclara/clara_core/mcp/client_manager.py` (lines 36-65, ServerConfig/ServerConnection dataclasses), `../mypalclara/clara_core/mcp/server_configs.py` (363 lines)
- **Deliverables:**
  - DB migration: `mcp_servers` table
  - `mypal/tools/mcp/models.py` -- MCPServerConfig SQLAlchemy model + Pydantic schemas
- **Acceptance criteria:**
  - Model matches architecture doc section 7.3: id, tenant_id, name, display_name, transport (streamable_http/sse/stdio), endpoint_url, command, args, env (encrypted at rest), oauth_config, enabled, status, tool_count, health_check_interval
  - `transport` is an enum: `streamable_http`, `sse`, `stdio`
  - `env` field stores encrypted JSON (secrets like API keys must not be plaintext in DB)
  - `status` enum: `connected`, `disconnected`, `error`, `starting`
  - CRUD API endpoints at `/api/v1/mcp-servers` (ADMIN+ role)
  - Tenant-scoped: each tenant manages its own MCP servers
  - Test: create server config, retrieve, update, verify env encryption round-trip
- **Estimated effort:** M

### WI-2.19: MCP Client Manager

- **Depends on:** WI-2.18
- **Port from:** `../mypalclara/clara_core/mcp/client_manager.py` (323 lines -- MCPClientManager class)
- **Deliverables:**
  - `mypal/tools/mcp/client.py` -- MCPClientManager class
  - `mypal/tools/mcp/transport.py` -- transport strategy implementation
- **Acceptance criteria:**
  - Transport strategy from architecture doc section 7.2:
    - StreamableHTTP (preferred for production): connects to URL endpoint
    - SSE (legacy): Server-Sent Events transport
    - STDIO (local dev): subprocess management, can be disabled via config `allow_stdio=False`
  - Ported from MyPalClara client_manager.py with these changes:
    - Remove singleton pattern (instantiated per-tenant)
    - Add StreamableHTTP transport (MyPalClara only had stdio + SSE)
    - Tool names prefixed with server name to avoid collisions (preserved from original: `{server_name}_{tool_name}`)
  - `connect_server(config)` -> discovers tools, stores connection
  - `call_tool(tool_name, arguments)` -> routes to correct server, returns result
  - `disconnect_server(name)` -> clean shutdown of connection
  - `get_all_tools()` -> aggregated tools from all connected servers
  - Connection cleanup: `managed_connection()` async context manager for automatic cleanup (preserved from original)
  - Test: connect stdio server, discover tools, call tool, disconnect; connect HTTP server; call tool on disconnected server returns error
- **Estimated effort:** L

### WI-2.20: MCP Health Checks and Reconnection

- **Depends on:** WI-2.19
- **Port from:** New (MyPalClara had no health checks -- architecture doc section 7.4 adds them)
- **Deliverables:**
  - `mypal/tools/mcp/health.py` -- MCPHealthMonitor class
- **Acceptance criteria:**
  - HTTP-based servers are health-checked at configurable intervals (from `MCPServerConfig.health_check_interval`, default 60s, 0 = disabled)
  - Health check: attempt `list_tools()` call -- if it returns, server is healthy
  - Failed health check triggers reconnection with exponential backoff: 1s, 2s, 4s (3 attempts)
  - After 3 failed reconnection attempts: mark server status as `error`, stop retrying until manual intervention or next health check cycle
  - STDIO servers: monitor subprocess, restart if process exits unexpectedly
  - Server status changes emit events (for future dashboard WebSocket updates)
  - `MCPServerConfig.status` updated in DB on state transitions
  - Test: healthy server stays connected; simulate failure, verify backoff timing (1s, 2s, 4s); verify error status after 3 failures
- **Estimated effort:** M

### WI-2.21: MCP OAuth 2.1 Flow

- **Depends on:** WI-2.19
- **Port from:** `../mypalclara/clara_core/mcp/google_token_provider.py` (OAuth token bridge concept)
- **Deliverables:**
  - `mypal/tools/mcp/oauth.py` -- MCP OAuth 2.1 client
  - Modified `mypal/tools/mcp/transport.py` -- inject OAuth tokens into HTTP transport
- **Acceptance criteria:**
  - For HTTP MCP servers that require OAuth 2.1:
    - `MCPServerConfig.oauth_config` stores: client_id, client_secret (encrypted), token_endpoint, scope, grant_type
    - On connection: acquire access token via client_credentials or authorization_code flow
    - Token refresh: auto-refresh before expiry (refresh 60s before `expires_at`)
    - Inject `Authorization: Bearer {token}` header on all requests to the MCP server
  - Per-tenant OAuth configs (different tenants can have different credentials for the same MCP server type)
  - Token storage: in-memory with Redis cache fallback (tokens are short-lived, no need for DB persistence)
  - Test: OAuth flow acquires token, injects into request; token refresh works before expiry; expired token triggers re-auth
- **Estimated effort:** M

---

## Stage 2G: Rook v2 Ingestion Pipeline

### WI-2.22: Normalize Step

- **Depends on:** Phase 1 (Rook v2 core: memories table, embedding, basic store)
- **Port from:** `../mypalclara/mypalclara/core/memory/core/memory.py` (fact extraction logic), `../mypalclara/mypalclara/core/memory/core/prompts.py` (FACT_RETRIEVAL_PROMPT)
- **Deliverables:**
  - `mypal/memory/ingestion.py` -- IngestionPipeline class with normalize step
- **Acceptance criteria:**
  - Normalize step receives raw memory content and produces a cleaned `MemoryCandidate`:
    - Strip leading/trailing whitespace
    - Categorize into MemoryCategory: FACT, PREFERENCE, OBSERVATION, RELATIONSHIP, SKILL, CONTEXT, INSTRUCTION, EVENT
    - Assign default importance (0.5) and confidence (1.0 for user-stated, 0.7 for inferred)
    - Determine scope from context: session-scoped for ephemeral, user_agent for relationship facts, user for cross-agent facts
    - Set source: CONVERSATION, REFLECTION, API, USER_EDIT
  - Category assignment uses keyword heuristics first (cheap), LLM classification as fallback (expensive, only for ambiguous cases)
  - Test: "User's favorite color is blue" categorizes as PREFERENCE; "Meeting with John at 3pm" categorizes as EVENT; whitespace is stripped
- **Estimated effort:** M

### WI-2.23: Dedup Check

- **Depends on:** WI-2.22
- **Port from:** `../mypalclara/mypalclara/core/memory/core/memory.py` (hash-based dedup exists, vector similarity dedup is new per architecture doc section 5.6)
- **Deliverables:**
  - Modified `mypal/memory/ingestion.py` -- dedup step in pipeline
- **Acceptance criteria:**
  - Before storing a new memory, compute its embedding and search existing memories in the same scope
  - Cosine similarity threshold: 0.92 (from architecture doc section 5.6)
  - If any existing memory has similarity >= 0.92: skip the new memory (it's a duplicate)
  - Dedup is scoped: only compares within the same (tenant_id, scope, agent_id, user_id) partition
  - Search limit: compare against top 10 nearest neighbors (not entire memory store)
  - Return dedup decision with the matching memory ID (for logging/debugging)
  - Test: store "User likes blue", attempt to store "User likes blue" again, verify skip; store "User likes blue", attempt to store "User prefers the color blue", verify skip (paraphrase); store "User likes blue", attempt to store "User likes red", verify accept (different fact)
- **Estimated effort:** M

### WI-2.24: Supersede Check

- **Depends on:** WI-2.23
- **Port from:** `../mypalclara/mypalclara/core/memory/dynamics/contradiction.py` (ContradictionResult, negation/antonym/temporal detection patterns)
- **Deliverables:**
  - Modified `mypal/memory/ingestion.py` -- supersede step in pipeline
  - `mypal/memory/contradiction.py` -- contradiction detection (ported from MyPalClara dynamics/contradiction.py)
- **Acceptance criteria:**
  - After dedup passes (no exact duplicate), check for semantic contradiction against existing memories
  - Two-phase check:
    1. Fast heuristic: negation patterns, antonym pairs, temporal conflicts (ported from MyPalClara contradiction.py)
    2. Cheap LLM classification (only if heuristic finds candidates at similarity 0.7-0.92):
       - Prompt: "Does memory A contradict memory B? Answer: supersedes/compatible/unrelated"
       - Uses cheapest model tier (from agent's LLM config)
  - If supersede detected:
    - New memory is stored with `supersedes` pointing to old memory ID
    - Old memory gets `superseded_by` pointing to new memory ID
    - Old memory `is_active` set to False
  - Cost guards from architecture doc section 5.6:
    - Use cheapest model tier
    - Rate limit: max 5 supersede checks per minute per tenant
    - Skip entirely for SESSION-scoped memories
    - On LLM failure: fall back to "just store it" (do not block ingestion)
  - Test: "lives in Portland" supersedes "lives in Seattle"; "likes dogs" does not supersede "likes cats" (compatible); SESSION-scoped memory skips supersede check; LLM failure results in normal store
- **Estimated effort:** L

### WI-2.25: Ingestion Pipeline Integration

- **Depends on:** WI-2.22, WI-2.23, WI-2.24
- **Port from:** New (pipeline composition is new; MyPalClara had ad-hoc ingestion in memory.py)
- **Deliverables:**
  - Modified `mypal/memory/ingestion.py` -- complete pipeline: normalize -> dedup -> supersede -> embed -> store
  - Modified `mypal/memory/manager.py` -- `RookManager.ingest()` calls pipeline
- **Acceptance criteria:**
  - `IngestionPipeline.ingest(content, scope, context) -> MemoryIngestionResult`
  - Result includes: action taken (stored/skipped_dedup/stored_superseded/error), memory_id, matched_memory_id (if dedup/supersede), timing_ms
  - Pipeline is sequential: normalize -> dedup -> supersede -> embed -> store
  - Each step can short-circuit (dedup match skips remaining steps)
  - Embedding happens after dedup/supersede checks (avoid wasting embedding calls on duplicates... though dedup itself needs an embedding, so the flow is: embed -> dedup -> supersede -> store)
  - Correction: Embed first (needed for dedup similarity search), then dedup, then supersede, then store
  - Round-trip integration test (the Cortex lesson from architecture doc section 5.11):
    ```
    ingest("User's favorite color is blue", scope=USER) -> memory_id
    retrieve("what color does the user like", scope=USER) -> results
    assert "blue" in results[0].content
    ```
  - Test: full pipeline end-to-end; dedup short-circuit; supersede with old memory deactivated; error in supersede LLM call does not block store
- **Estimated effort:** M

---

## Stage 2H: Rook v2 Retrieval

### WI-2.26: Two-Tier Retrieval

- **Depends on:** Phase 1 (Rook v2 core), WI-2.25 (ingestion pipeline working)
- **Port from:** New (concept from architecture doc section 5.7, inspired by Cortex's two-tier design)
- **Deliverables:**
  - `mypal/memory/retriever.py` -- RookRetriever class with quick_context and full_context modes
- **Acceptance criteria:**
  - **Quick context** (for evaluate/triage stages):
    - No vector search, no LLM calls
    - Returns only Redis-cached identity facts and current session messages
    - Target latency: < 50ms
    - Falls back to Postgres query if Redis is unavailable
  - **Full context** (for respond stage):
    - Full vector search with FSRS scoring (similarity * 0.5 + retrievability * 0.3 + importance * 0.2, per architecture doc section 5.5)
    - Budget-aware truncation (see WI-2.27)
    - Multi-scope query (see WI-2.28)
  - Both modes return `RetrievalResult(memories, token_count, source, latency_ms)`
  - Test: quick_context returns cached data without vector search; full_context performs vector search and returns scored results; quick_context falls back to Postgres when Redis is down
- **Estimated effort:** M

### WI-2.27: Token Budget Allocation

- **Depends on:** WI-2.26
- **Port from:** New (budget allocation specified in architecture doc section 5.7)
- **Deliverables:**
  - `mypal/memory/budget.py` -- TokenBudgetAllocator class
- **Acceptance criteria:**
  - Fixed allocation from architecture doc section 5.7:
    - Identity facts: 500 tokens
    - Session messages (recent history): 1500 tokens
    - Previous session summary: 300 tokens
    - Semantic memories: remaining budget (total context window - above - system prompt - tool definitions)
  - Total budget derived from agent's LLM config context window minus reserved space for system prompt and tool definitions
  - Each category filled in priority order: identity first, then session, then summary, then semantic
  - If a category exceeds its budget, truncate (oldest messages first for session, lowest-scored first for semantic)
  - Token counting uses tiktoken (or provider-specific tokenizer) for accuracy
  - Returns `BudgetResult(identity_memories, session_messages, summary, semantic_memories, tokens_used, tokens_remaining)`
  - Test: budget allocation respects limits; overflow in session messages truncates oldest; semantic memories ranked by retrieval score with lowest dropped first
- **Estimated effort:** M

### WI-2.28: Scoped Multi-Scope Query

- **Depends on:** WI-2.26
- **Port from:** New (scope priority from architecture doc section 5.3)
- **Deliverables:**
  - Modified `mypal/memory/retriever.py` -- multi-scope query with priority
- **Acceptance criteria:**
  - Retrieval queries multiple scopes in priority order (narrowest first, highest priority, per architecture doc section 5.3):
    1. SESSION -- current conversation context
    2. USER_AGENT -- this agent's memories about this user
    3. USER -- facts about this user any agent can see
    4. AGENT -- this agent's general knowledge
    5. TENANT -- shared team/org knowledge
    6. SYSTEM -- platform-wide knowledge (if any)
  - Each scope gets a portion of the semantic memory budget (configurable weights, default: SESSION 30%, USER_AGENT 25%, USER 20%, AGENT 15%, TENANT 8%, SYSTEM 2%)
  - Results are merged and re-ranked by retrieval_score across all scopes
  - Scope filters enforced at the SQL level: `WHERE tenant_id = ? AND scope = ? AND agent_id = ? AND user_id = ?`
  - Test: query returns memories from multiple scopes; narrower scope memories rank higher; cross-tenant memories never returned
- **Estimated effort:** M

### WI-2.29: Redis Caching Layer

- **Depends on:** WI-2.26
- **Port from:** `../mypalclara/mypalclara/core/memory/cache/redis_cache.py` (RedisCache class with graceful degradation)
- **Deliverables:**
  - `mypal/memory/cache.py` -- MemoryCache class (ported from MyPalClara RedisCache)
- **Acceptance criteria:**
  - Caches three categories (TTLs from MyPalClara redis_cache.py):
    - Identity facts: 10 min TTL (key: `rook:{tenant_id}:{user_id}:identity`)
    - Session context: 5 min TTL (key: `rook:{tenant_id}:{session_id}:context`)
    - Hot memories (frequently accessed): 5 min TTL (key: `rook:{tenant_id}:{user_id}:{agent_id}:hot`)
  - Cache invalidation: on memory ingest that affects a cached scope, invalidate relevant keys
  - Graceful degradation (preserved from MyPalClara): if Redis is unavailable, all operations are no-ops (return None), system falls back to Postgres-only queries
  - Cache warming: on session start, pre-fetch identity facts and recent session context into cache
  - Test: cached identity facts returned without DB query; cache miss falls through to DB; Redis down returns None and falls back to Postgres; ingest invalidates relevant cache key
- **Estimated effort:** M

---

## Stage 2I: Memory Extraction + Prompts

### WI-2.30: Memory Extraction from Conversations (Reflect Stage)

- **Depends on:** WI-2.25 (ingestion pipeline)
- **Port from:** `../mypalclara/mypalclara/core/memory/core/prompts.py` (FACT_RETRIEVAL_PROMPT), `../mypalclara/mypalclara/core/memory/core/memory.py` (fact extraction logic with LLM)
- **Deliverables:**
  - `mypal/memory/extraction.py` -- MemoryExtractor class
- **Acceptance criteria:**
  - After each agent response (Reflect stage per architecture doc section 5.9), extract new facts from the exchange
  - Extraction uses cheap model tier (from agent's LLM config `low_tier` setting)
  - Extraction prompt includes existing context: "Here are things you already know about this user: [existing memories]. Extract only NEW facts from this conversation."
  - Extraction output: list of `MemoryCandidate` objects with content, category, importance, is_key flag
  - Each extracted candidate is fed through the ingestion pipeline (WI-2.25) for dedup/supersede
  - Extraction is async: does not block the response delivery to the user
  - Test: conversation "My name is Josh and I just moved to Portland" extracts name fact and location fact; re-extraction with existing "Name is Josh" memory does not re-extract name; extraction failure does not block response
- **Estimated effort:** M

### WI-2.31: Extraction Cost Controls

- **Depends on:** WI-2.30
- **Port from:** New (architecture doc section 5.9: "lightweight, cheap model")
- **Deliverables:**
  - Modified `mypal/memory/extraction.py` -- cost control decorators/guards
- **Acceptance criteria:**
  - Model tier: always uses cheapest tier for extraction (never high-tier)
  - Skip trivial: if conversation turn has < 20 tokens of user input, skip extraction entirely (greetings, "ok", "thanks")
  - Rate limit: max 10 extraction calls per user per hour (prevents runaway costs in high-volume conversations)
  - Session-scoped memories: extraction produces session-scoped memories by default for ephemeral context; only promotes to user/user_agent scope if `is_key=true` or importance >= 0.7
  - Cost tracking: log model, tokens_used, estimated_cost per extraction call (for billing/monitoring)
  - Test: trivial message "ok" skips extraction; rate limit triggers after 10 calls; session-scoped memory stays session-scoped unless key
- **Estimated effort:** S

### WI-2.32: Composable Prompt System

- **Depends on:** WI-2.2 (persona configs), WI-2.12 (per-agent tools), WI-2.26 (retrieval)
- **Port from:** New (architecture doc section 1.2: "Composable files: persona, tools, user")
- **Deliverables:**
  - `mypal/agents/prompts.py` -- PromptComposer class
- **Acceptance criteria:**
  - Builds the system prompt from composable sections (replacing MyPalClara's monolithic system prompt):
    1. **Persona section**: agent's system prompt, personality traits, tone (from AgentDefinition.persona)
    2. **Tools section**: descriptions and usage instructions for the agent's configured tools
    3. **User section**: identity facts, preferences, relationship context (from Rook retrieval)
    4. **Session section**: conversation summary, recent context
  - Each section is a separate template (Jinja2 or f-string based) that can be customized per-agent
  - Token-aware: total system prompt must fit within the agent's context budget (truncates user section first, then session, preserving persona and tools)
  - `PromptComposer.build(agent_def, user_context, session_context, tools) -> str`
  - Test: composed prompt contains all four sections; token overflow truncates user section first; agent with no tools omits tools section; different agents produce different personas
- **Estimated effort:** M

---

## Dependency Graph

```
Phase 1 (complete)
│
├── WI-2.1 (Agent Service)
│   ├── WI-2.2 (Personas) ──── WI-2.3 (Agent API)
│   ├── WI-2.4 (Router) ─────── WI-2.5 (Bindings)
│   │   └── WI-2.6 (Wire Router)
│   └── WI-2.7 (Sub-Agent Orchestrator)
│       ├── WI-2.8 (Max Depth)
│       └── WI-2.9 (Context Scoping)
│
├── WI-2.10 (Tool Base)
│   ├── WI-2.11 (Executor)
│   │   ├── WI-2.13 (Loop Guard)
│   │   └── WI-2.16 (Point B + ASK Flow) ── WI-2.14 (Permission Resolver)
│   ├── WI-2.12 (Per-Agent Tools)
│   │   └── WI-2.15 (Point A Filtering) ── WI-2.14
│   └── WI-2.14 (Permission Resolver)
│       └── WI-2.17 (Permission API)
│
├── WI-2.18 (MCP Config Model)
│   └── WI-2.19 (MCP Client Manager)
│       ├── WI-2.20 (Health Checks)
│       └── WI-2.21 (OAuth 2.1)
│
├── WI-2.22 (Normalize) ── WI-2.23 (Dedup) ── WI-2.24 (Supersede) ── WI-2.25 (Pipeline Integration)
│
├── WI-2.26 (Two-Tier Retrieval)
│   ├── WI-2.27 (Token Budget)
│   ├── WI-2.28 (Multi-Scope Query)
│   └── WI-2.29 (Redis Cache)
│
└── WI-2.30 (Memory Extraction)
    ├── WI-2.31 (Cost Controls)
    └── WI-2.32 (Composable Prompts)
```

---

## Suggested Execution Order

Work items grouped by week, accounting for dependencies:

**Week 7:**
- WI-2.1 (Agent Service) + WI-2.10 (Tool Base) -- independent, start in parallel
- WI-2.2 (Personas) + WI-2.18 (MCP Config Model) -- once WI-2.1 is done
- WI-2.22 (Normalize) -- independent of agent work

**Week 8:**
- WI-2.3 (Agent API) + WI-2.4 (Router) + WI-2.5 (Bindings)
- WI-2.11 (Executor) + WI-2.12 (Per-Agent Tools) + WI-2.13 (Loop Guard)
- WI-2.23 (Dedup) + WI-2.24 (Supersede) + WI-2.25 (Pipeline Integration)

**Week 9:**
- WI-2.6 (Wire Router) + WI-2.7 (Orchestrator) + WI-2.8 (Max Depth) + WI-2.9 (Context Scoping)
- WI-2.14 (Permission Resolver) + WI-2.15 (Point A) + WI-2.16 (Point B + ASK)
- WI-2.19 (MCP Client) + WI-2.20 (Health Checks)
- WI-2.26 (Two-Tier Retrieval) + WI-2.27 (Token Budget) + WI-2.28 (Multi-Scope)

**Week 10:**
- WI-2.17 (Permission API) + WI-2.21 (OAuth 2.1)
- WI-2.29 (Redis Cache)
- WI-2.30 (Extraction) + WI-2.31 (Cost Controls) + WI-2.32 (Composable Prompts)
- Integration testing: full message flow through router -> runtime -> tools -> memory

---

## Exit Criteria (Phase 2 Complete)

All of the following must be demonstrated before proceeding to Phase 3:

1. **Multi-agent:** Create two agents (Clara, Rex) via API. Send messages to each, verify different personas respond.
2. **Routing:** Bind Rex to a channel, send message to that channel, verify Rex responds. Send DM, verify user's default (Clara) responds. Send `@Rex` in an unbound channel, verify Rex responds.
3. **Sub-agents:** Clara delegates a task to Rex, receives result, includes it in her response to user. Depth-3 delegation is rejected.
4. **Tools:** Agent with `["web_search"]` can execute web_search. Agent without it cannot see or execute it. Tool loop at 11 calls triggers guard.
5. **Permissions:** DENY'd tool is invisible to LLM. ASK tool prompts user, "yes" executes, "no" rejects, timeout rejects.
6. **MCP:** Connect to at least one MCP server (stdio for dev), discover tools, execute a tool call, verify result.
7. **Ingestion:** Store "lives in Seattle", then store "lives in Portland" -- verify supersede chain. Store duplicate -- verify skip.
8. **Retrieval:** Two-tier retrieval returns cached quick context in < 50ms. Full context returns scored memories from multiple scopes. Redis down falls back to Postgres.
9. **Extraction:** Conversation produces new memories via Reflect stage. Trivial messages skip extraction. Rate limit enforced.
10. **Prompts:** System prompt is composed from persona + tools + user + session sections. Different agents produce different prompts.

---

## File Index

All new files created in Phase 2, organized by the project structure from architecture doc section 11:

```
mypal/
├── agents/
│   ├── bindings.py          (WI-2.5)
│   ├── context.py           (WI-2.9)
│   ├── orchestrator.py      (WI-2.7, WI-2.8)
│   ├── prompts.py           (WI-2.32)
│   ├── router.py            (WI-2.4)
│   ├── schemas.py           (WI-2.1)
│   ├── seed.py              (WI-2.2)
│   ├── service.py           (WI-2.1)
│   └── personas/
│       ├── __init__.py       (WI-2.2)
│       └── clara.py          (WI-2.2)
├── api/v1/
│   ├── agents.py            (WI-2.3)
│   └── permissions.py       (WI-2.17)
├── memory/
│   ├── budget.py            (WI-2.27)
│   ├── cache.py             (WI-2.29)
│   ├── contradiction.py     (WI-2.24)
│   ├── extraction.py        (WI-2.30, WI-2.31)
│   ├── ingestion.py         (WI-2.22, WI-2.23, WI-2.24, WI-2.25)
│   └── retriever.py         (WI-2.26, WI-2.27, WI-2.28)
├── tools/
│   ├── __init__.py           (WI-2.10)
│   ├── ask_flow.py           (WI-2.16)
│   ├── base.py               (WI-2.10)
│   ├── executor.py           (WI-2.11)
│   ├── loader.py             (WI-2.12)
│   ├── loop_guard.py         (WI-2.13)
│   ├── permissions.py        (WI-2.14, WI-2.15)
│   ├── registry.py           (WI-2.10)
│   ├── validation.py         (WI-2.10)
│   └── mcp/
│       ├── __init__.py        (WI-2.18)
│       ├── client.py          (WI-2.19)
│       ├── health.py          (WI-2.20)
│       ├── models.py          (WI-2.18)
│       ├── oauth.py           (WI-2.21)
│       └── transport.py       (WI-2.19)
```

Modified files (created in Phase 1, updated in Phase 2):

```
mypal/
├── agents/
│   └── runtime.py           (WI-2.6, WI-2.12, WI-2.15, WI-2.16)
├── api/v1/
│   └── chat.py              (WI-2.6)
└── memory/
    └── manager.py           (WI-2.25)
```
