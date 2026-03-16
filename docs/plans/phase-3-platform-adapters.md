# Phase 3: Platform Adapters, Streaming, Sandbox, FSRS & Cross-Agent Memory

**Depends on:** Phase 1 (Foundation) and Phase 2 (Agent System) complete
**Assumed ready:** Agent routing, tool execution, MCP, permissions, memory ingestion/retrieval all working
**Architecture reference:** `docs/MyPal_Master_Architecture.md` sections 3.4, 5.5, 5.8, 6, 9.2

---

## Stage 3A: Adapter Base + Protocol

Port the adapter infrastructure from mypalclara. The base class, protocol types, and capability system form the foundation all platform adapters build on. The key change from mypalclara is adding multi-tenant awareness: every incoming message must resolve to a tenant before hitting the agent router.

### WI-3.1: Adapter Base Class and Capability Protocols

- **Depends on:** Phase 2 (transport layer, agent router)
- **Port from:** `../mypalclara/mypalclara/adapters/base.py` (876 lines — `GatewayClient` ABC with WebSocket connection, heartbeats, reconnection, error classification, health checks, MCP request forwarding), `../mypalclara/mypalclara/adapters/protocols.py` (388 lines — 10 capability protocols: `MessagingAdapter`, `ToolAdapter`, `StreamingAdapter`, `AttachmentAdapter`, `ReactionAdapter`, `EditableAdapter`, `ThreadAdapter`, `ButtonAdapter`, `EmbedAdapter`, `MentionAdapter`), `../mypalclara/mypalclara/adapters/manifest.py` (324 lines — `AdapterManifest` dataclass, `@adapter` decorator, global registry with discovery)
- **Deliverables:**
  - `mypal/adapters/base.py` — `AdapterBase` ABC. Port `GatewayClient` but replace the WebSocket-to-gateway pattern with direct integration into MyPal's transport layer (`mypal/transport/`). Keep: error classification (`ErrorCategory`, `classify_error`), health check system (`HealthStatus`, `HealthCheckResult`), reconnection with exponential backoff. Drop: gateway WebSocket connection (adapters now call the agent router directly via internal API). Add: `tenant_id` on every outbound call.
  - `mypal/adapters/protocols.py` — Port all 10 capability protocols verbatim. These are pure `Protocol` classes with no implementation dependencies.
  - `mypal/adapters/manifest.py` — Port `AdapterManifest`, `@adapter` decorator, registry functions. Add `tenant_scoping` field to manifest (whether adapter supports multi-tenant guild mapping).
  - `mypal/adapters/__init__.py` — Re-exports and `discover_adapters()`.
- **Acceptance criteria:**
  - `AdapterBase` can be subclassed; a minimal test adapter registers via `@adapter` and appears in `list_adapters()`.
  - `validate_adapter_env("test")` returns missing env vars correctly.
  - `get_capabilities(TestAdapter)` returns the correct protocol list based on which protocols the test class implements.
  - `classify_error(TimeoutError())` returns `ErrorCategory.TRANSIENT`; `classify_error(Exception("invalid token"))` returns `ErrorCategory.CONFIGURATION`.
  - `health_check()` returns `HealthStatus.UNHEALTHY` when not connected.
  - All 10 capability protocols from `protocols.py` are importable and `runtime_checkable`.
- **Estimated effort:** M

### WI-3.2: Multi-Tenant Message Resolution

- **Depends on:** WI-3.1, Phase 2 (tenant service, agent router)
- **Port from:** New (mypalclara was single-tenant)
- **Deliverables:**
  - `mypal/adapters/tenant_resolver.py` — `TenantResolver` service. Maps platform-specific identifiers to tenant IDs. Discord: guild_id -> tenant. Slack: workspace_id -> tenant. Teams: teams_tenant_id -> tenant. CLI: configured default tenant. Caches mappings in Redis. Falls back to DB lookup.
  - `mypal/db/models/adapter_mapping.py` — `PlatformTenantMapping` model: `(platform, platform_org_id) -> tenant_id`. Unique constraint on `(platform, platform_org_id)`.
  - Alembic migration for the `platform_tenant_mappings` table.
- **Acceptance criteria:**
  - `resolver.resolve("discord", guild_id="123456")` returns the correct `tenant_id` from DB.
  - Cache hit on second call (verified via Redis mock or spy).
  - Unknown guild returns `None` (adapter can then enter onboarding flow).
  - `resolver.register_mapping("discord", "123456", tenant_id="t-abc")` creates the mapping and invalidates cache.
  - Migration runs cleanly up and down.
- **Estimated effort:** M

### WI-3.3: IncomingMessage Normalization

- **Depends on:** WI-3.1, WI-3.2
- **Port from:** New (architecture spec section 6.1 defines the format; mypalclara used `MessageRequest` from `gateway/protocol.py`)
- **Deliverables:**
  - `mypal/inputs/message.py` — `IncomingMessage` dataclass matching the architecture spec exactly: `id`, `tenant_id`, `user_id`, `agent_id`, `channel_id`, `platform`, `text`, `attachments`, `voice_audio`, `metadata`, `reply_to`, `conversation_id`, `timestamp`. Plus `Attachment` model for files/images.
  - `mypal/adapters/normalizer.py` — `MessageNormalizer` that each adapter calls. Takes platform-specific data (user info, channel info, content, attachments) and produces an `IncomingMessage`. Handles: tenant resolution (via WI-3.2), user identity lookup (platform_id -> user_id via pairing service from Phase 2), agent targeting (explicit mention extraction, channel binding lookup).
- **Acceptance criteria:**
  - `normalizer.normalize(platform="discord", user=UserInfo(...), channel=ChannelInfo(...), content="@Rex help", ...)` returns an `IncomingMessage` with `tenant_id` resolved, `user_id` resolved (or `None` for unpaired), `agent_id="rex"` extracted from mention.
  - Normalization without a mention sets `agent_id=None` (router decides).
  - Missing tenant raises `TenantNotFoundError` (adapter handles with onboarding prompt).
  - `IncomingMessage` is serializable to/from JSON for transport over WebSocket.
- **Estimated effort:** M

---

## Stage 3B: Discord Adapter

Port the Discord adapter, the most feature-rich adapter in mypalclara. Key changes: multi-tenant guild mapping, agent routing integration (mention-based agent selection, channel-to-agent binding), and normalization through the new `IncomingMessage` pipeline.

### WI-3.4: Discord Adapter Core

- **Depends on:** WI-3.1, WI-3.2, WI-3.3
- **Port from:** `../mypalclara/mypalclara/adapters/discord/gateway_client.py` (823 lines — `DiscordGatewayClient` with streaming display, typing loops, message chunking, embed/button/thread/file support, proactive messages), `../mypalclara/mypalclara/adapters/discord/adapter.py` (155 lines — `DiscordAdapter` Strangler Fig wrapper), `../mypalclara/mypalclara/adapters/discord/attachment_handler.py` (~300 lines — image resizing, text extraction, document parsing), `../mypalclara/mypalclara/adapters/discord/message_builder.py` (~250 lines — `clean_content`, `format_response`, `parse_markers`, `split_message`)
- **Deliverables:**
  - `mypal/adapters/discord/__init__.py`
  - `mypal/adapters/discord/client.py` — Port `DiscordGatewayClient`. Replace gateway WebSocket send with direct call to `MessageNormalizer` -> agent router. Keep: `PendingResponse` tracking, typing loop management, `on_response_start/chunk/end` handlers, embed creation, button views, thread creation, file sending (both path-based and base64), proactive message delivery, reply chain building, channel-scoped cancellation.
  - `mypal/adapters/discord/attachment_handler.py` — Port verbatim. Image resizing, text file extraction, document parsing are platform-specific and unchanged.
  - `mypal/adapters/discord/message_builder.py` — Port verbatim. `clean_content`, `split_message`, `parse_markers` are Discord-specific formatting.
  - `mypal/adapters/discord/main.py` — Port `GatewayDiscordBot` entry point. Replace `mypalclara.core.discord.setup` with MyPal slash command registration. Keep: intents config, `on_message` handler, stop phrase detection, tier override detection, voice state forwarding, signal handling.
- **Acceptance criteria:**
  - Bot starts, connects to Discord, and responds to DMs through the agent pipeline.
  - `@mention` in a server channel triggers the correct agent (via normalizer mention extraction).
  - Channel mode (active/mention/off) works: mention-only channels ignore non-mentioned messages.
  - Typing indicator shows during response generation and stops on completion.
  - Responses over 2000 chars are split correctly by `split_message`.
  - Image attachments are resized and forwarded to the agent as base64.
  - Embed markers in agent responses render as Discord embeds.
  - Stop phrase ("clara stop") cancels pending requests and adds the stop reaction.
  - `health_check()` reports connected status and latency.
- **Estimated effort:** L

### WI-3.5: Discord Multi-Tenant Guild Mapping

- **Depends on:** WI-3.4, WI-3.2
- **Port from:** New (mypalclara was single-tenant; the guild_id field exists in `ChannelInfo` but was unused for tenant routing)
- **Deliverables:**
  - `mypal/adapters/discord/tenant.py` — `DiscordTenantMapper`. On `on_guild_join`, prompts for tenant association (slash command or admin API). Maps `guild_id -> tenant_id` via `TenantResolver`. Caches per-guild. Handles: guild join (new mapping), guild leave (cleanup), guild already mapped (no-op).
  - `mypal/adapters/discord/channel_modes.py` — Port from `../mypalclara/mypalclara/adapters/discord/channel_modes.py` (169 lines). `ChannelModeManager` with mode cache and DB persistence. Change: add `tenant_id` to `ChannelConfig` model so modes are tenant-scoped.
- **Acceptance criteria:**
  - When the bot joins a guild not yet mapped to a tenant, it sends a setup prompt.
  - After mapping, all messages from that guild resolve to the correct `tenant_id`.
  - Two guilds mapped to different tenants produce `IncomingMessage`s with different `tenant_id` values.
  - Channel modes are scoped per tenant: guild A's channel 123 and guild B's channel 123 can have different modes.
  - Guild removal clears the mapping and associated channel modes.
- **Estimated effort:** M

### WI-3.6: Discord Agent Routing Integration

- **Depends on:** WI-3.4, WI-3.5, Phase 2 (agent router)
- **Port from:** New (mypalclara had only Clara; routing was implicit)
- **Deliverables:**
  - `mypal/adapters/discord/routing.py` — Discord-specific routing helpers. Agent mention detection: parses `@AgentName` from message content, resolves to agent_id via tenant's agent registry. Channel-to-agent binding: DB-backed mapping of `(tenant_id, channel_id) -> agent_id` so that e.g. `#code-review` always routes to Rex. Slash command `/bind <agent>` to set channel binding. Conversation continuity: tracks last-responding agent per channel for follow-up messages.
- **Acceptance criteria:**
  - `@Rex review this` in a channel routes to the Rex agent within that tenant.
  - Unknown agent mention (e.g., `@NonExistent`) falls through to tenant default agent.
  - `/bind rex` in a channel persists the binding; subsequent messages (even without mention) route to Rex.
  - `/unbind` removes the binding; messages revert to default routing.
  - In a channel without binding, a reply to a Rex message continues with Rex (conversation continuity).
- **Estimated effort:** M

---

## Stage 3C: Unified Streaming Protocol

Port and generalize the streaming response protocol so all adapters (Discord, Slack, CLI, API WebSocket) receive responses through a common streaming interface. The mypalclara `LLMOrchestrator` yields events (`tool_start`, `tool_result`, `chunk`, `complete`); this stage formalizes that into a typed protocol.

### WI-3.7: Streaming Response Protocol

- **Depends on:** WI-3.1, Phase 2 (agent runtime)
- **Port from:** `../mypalclara/mypalclara/gateway/llm_orchestrator.py` (665 lines — `LLMOrchestrator.generate_with_tools` yields event dicts with types: `tool_start`, `tool_result`, `chunk`, `complete`; also `_stream_text` for simulated streaming, `_call_main_llm_streaming` for real streaming), `../mypalclara/mypalclara/adapters/protocol.py` (re-exports of `ResponseStart`, `ResponseChunk`, `ResponseEnd`, `ToolStart`, `ToolResult`)
- **Deliverables:**
  - `mypal/transport/stream.py` — `ResponseStream` protocol. Typed event classes: `StreamEvent` (base), `ResponseStartEvent`, `ResponseChunkEvent(chunk: str, accumulated: str)`, `ResponseEndEvent(full_text: str, tool_count: int, files: list)`, `ToolStartEvent(tool_name: str, step: int, arguments: dict)`, `ToolResultEvent(tool_name: str, success: bool, output_preview: str)`, `ErrorEvent(code: str, message: str)`, `CancelledEvent(reason: str)`. All events carry `request_id` and `timestamp`.
  - `mypal/transport/stream_manager.py` — `StreamManager`. Manages active streams per request_id. Adapters subscribe to a request's stream via `async for event in stream_manager.subscribe(request_id)`. Agent runtime publishes events via `stream_manager.publish(request_id, event)`. Backed by asyncio queues (in-process) with Redis pub/sub fallback for multi-worker.
- **Acceptance criteria:**
  - Agent runtime yields `StreamEvent`s during processing; a test adapter receives them in order.
  - `ResponseChunkEvent` carries both the incremental `chunk` and `accumulated` text.
  - `ToolStartEvent` includes `tool_name`, `step` number, and `arguments`.
  - `ResponseEndEvent.full_text` matches the concatenation of all chunks.
  - Multiple concurrent requests have independent streams (no cross-contamination).
  - Subscribing after some events are published still delivers all events (replay from buffer).
  - `ErrorEvent` is delivered if the agent runtime raises during processing.
  - Stream auto-closes after `ResponseEndEvent` or `ErrorEvent`.
- **Estimated effort:** M

### WI-3.8: Adapter Stream Integration

- **Depends on:** WI-3.7, WI-3.4
- **Port from:** `../mypalclara/mypalclara/adapters/discord/gateway_client.py` (the `on_response_start/chunk/end` callback pattern), `../mypalclara/mypalclara/adapters/cli/gateway_client.py` (Rich live display streaming)
- **Deliverables:**
  - `mypal/adapters/base.py` (update) — Add `_handle_stream(request_id)` method to `AdapterBase` that subscribes to `StreamManager` and dispatches to `on_response_start`, `on_response_chunk`, `on_response_end`, `on_tool_start`, `on_tool_result`, `on_error`, `on_cancelled`. Default implementations log; subclasses override.
  - Update Discord adapter (`mypal/adapters/discord/client.py`) to use the new stream protocol instead of direct gateway messages.
  - Update CLI adapter (WI-3.12) to use the new stream protocol.
- **Acceptance criteria:**
  - Discord adapter shows typing indicator on `ResponseStartEvent`, accumulates text on `ResponseChunkEvent`, sends final message on `ResponseEndEvent`.
  - Tool status messages appear on `ToolStartEvent`.
  - CLI adapter updates Rich `Live` display on each chunk event.
  - An adapter that doesn't override `on_tool_start` silently ignores tool events (no crash).
  - Cancellation event stops the typing indicator and adds a reaction.
- **Estimated effort:** M

---

## Stage 3D: Additional Adapters

Port existing adapters and build new ones. Each follows the same pattern: subclass `AdapterBase`, implement capability protocols, normalize through `MessageNormalizer`, consume `ResponseStream`.

### WI-3.9: Slack Adapter

- **Depends on:** WI-3.1, WI-3.3, WI-3.7
- **Port from:** New (mypalclara's `manifest.py` lists `slack` in `discover_adapters()` but the module was not implemented)
- **Deliverables:**
  - `mypal/adapters/slack/__init__.py`
  - `mypal/adapters/slack/client.py` — `SlackAdapter(AdapterBase)`. Uses `slack_bolt` (Bolt for Python). Implements: `MessagingAdapter`, `StreamingAdapter`, `AttachmentAdapter`, `ReactionAdapter`, `ThreadAdapter`, `EditableAdapter`. Handles: Slack Events API (message.im, message.channels, app_mention), slash commands, interactive components (buttons, modals). Multi-tenant: workspace_id -> tenant_id via `TenantResolver`.
  - `mypal/adapters/slack/message_builder.py` — Slack Block Kit message formatting. Markdown to mrkdwn conversion. Message splitting at Slack's 4000-char block limit.
  - `mypal/adapters/slack/main.py` — Entry point using `slack_bolt` async app.
- **Acceptance criteria:**
  - Bot responds to DMs and `@mention` in channels.
  - Workspace-to-tenant resolution works for multi-tenant deployment.
  - Responses use Block Kit formatting with proper mrkdwn.
  - Long responses are split across multiple blocks.
  - File attachments are uploaded via Slack `files.upload` API.
  - Thread replies stay in-thread.
  - Typing indicator shows via `chat.postMessage` with `"typing": true` (or equivalent Slack mechanism).
- **Estimated effort:** L

### WI-3.10: Teams Adapter (Port)

- **Depends on:** WI-3.1, WI-3.3, WI-3.7
- **Port from:** `../mypalclara/mypalclara/adapters/teams/gateway_client.py` (515 lines — `TeamsGatewayClient` with Adaptive Card rendering, Graph API conversation history, OneDrive file sharing), `../mypalclara/mypalclara/adapters/teams/bot.py` (~180 lines — `TeamsBot` Bot Framework handler), `../mypalclara/mypalclara/adapters/teams/graph_client.py` (~500 lines — MS Graph API client for chat history, file upload), `../mypalclara/mypalclara/adapters/teams/message_builder.py` (~500 lines — `AdaptiveCardBuilder` for response/tool/error/file cards), `../mypalclara/mypalclara/adapters/teams/main.py` (~240 lines — aiohttp server entry point)
- **Deliverables:**
  - `mypal/adapters/teams/__init__.py`
  - `mypal/adapters/teams/client.py` — Port `TeamsGatewayClient`. Replace gateway WebSocket with direct agent router integration. Keep: `PendingResponse` tracking, Adaptive Card rendering, Graph API history fetching, OneDrive file upload, typing indicators, mention cleaning. Add: tenant resolution via `teams_tenant_id` -> MyPal `tenant_id`.
  - `mypal/adapters/teams/bot.py` — Port `TeamsBot` with Bot Framework integration.
  - `mypal/adapters/teams/graph_client.py` — Port Graph API client for conversation history and file management.
  - `mypal/adapters/teams/message_builder.py` — Port `AdaptiveCardBuilder` for response cards, tool status cards, error cards, file cards.
  - `mypal/adapters/teams/main.py` — Port aiohttp server entry point for Bot Framework webhook.
- **Acceptance criteria:**
  - Bot responds in personal chats (DM), group chats, and Teams channels.
  - Conversation history is fetched via Graph API and included as reply chain context.
  - Long responses render as Adaptive Cards; short responses as plain text.
  - Tool execution shows status cards during processing.
  - File attachments are uploaded to OneDrive and shared via file cards with download links.
  - Multi-tenant: Teams tenant ID maps to MyPal tenant.
  - Typing indicator shows during response generation.
- **Estimated effort:** L

### WI-3.11: Telegram Adapter

- **Depends on:** WI-3.1, WI-3.3, WI-3.7
- **Port from:** New (listed in `discover_adapters()` but not implemented)
- **Deliverables:**
  - `mypal/adapters/telegram/__init__.py`
  - `mypal/adapters/telegram/client.py` — `TelegramAdapter(AdapterBase)`. Uses `python-telegram-bot` library. Implements: `MessagingAdapter`, `StreamingAdapter`, `AttachmentAdapter`, `ReactionAdapter`, `EditableAdapter`. Handles: private messages, group messages (bot mentioned or reply-to), inline queries. Multi-tenant: one bot per tenant (bot token is tenant-scoped config).
  - `mypal/adapters/telegram/message_builder.py` — Telegram MarkdownV2 formatting. Message splitting at Telegram's 4096-char limit.
  - `mypal/adapters/telegram/main.py` — Entry point with webhook or polling mode.
- **Acceptance criteria:**
  - Bot responds to private messages.
  - In groups, bot responds only when mentioned or replied to.
  - Responses use Telegram MarkdownV2 formatting.
  - Long responses are split into multiple messages.
  - Images and files are sent via Telegram's `sendDocument`/`sendPhoto` API.
  - Bot token is per-tenant; multiple tenants can each have their own Telegram bot.
  - Typing indicator ("chat action") shows during processing.
- **Estimated effort:** M

### WI-3.12: CLI Adapter (Port)

- **Depends on:** WI-3.1, WI-3.3, WI-3.7
- **Port from:** `../mypalclara/mypalclara/adapters/cli/gateway_client.py` (205 lines — `CLIGatewayClient` with Rich live streaming, markdown rendering, tool status panels), `../mypalclara/mypalclara/adapters/cli/adapter.py` (155 lines), `../mypalclara/mypalclara/adapters/cli/commands.py` (~880 lines — slash commands, MCP management, shell executor), `../mypalclara/mypalclara/adapters/cli/main.py` (~130 lines — REPL entry point with input handling), `../mypalclara/mypalclara/adapters/cli/shell_executor.py` (~170 lines), `../mypalclara/mypalclara/adapters/cli/tools.py` (~370 lines — approval flow, tool management)
- **Deliverables:**
  - `mypal/adapters/cli/__init__.py`
  - `mypal/adapters/cli/client.py` — Port `CLIGatewayClient`. Replace gateway WebSocket with direct agent router call. Keep: Rich `Live` display for streaming, `Spinner` for thinking state, markdown rendering via `Rich.Markdown`, tool execution panels with color-coded success/failure. Add: tenant selection (CLI flag `--tenant` or env `MYPAL_TENANT_ID`), agent selection (CLI flag `--agent` or `/agent <name>` command).
  - `mypal/adapters/cli/commands.py` — Port slash commands. Keep MCP management commands, agent switching, tool listing. Add: `/tenant` command for multi-tenant switching, `/agents` to list available agents.
  - `mypal/adapters/cli/main.py` — Port REPL entry point. Keep: signal handling, input loop, Rich console setup.
- **Acceptance criteria:**
  - `python -m mypal.adapters.cli --tenant t-abc --agent clara` starts a REPL session.
  - User input is processed through the full agent pipeline and displayed with Rich markdown formatting.
  - Streaming response updates the display in real-time via `Rich.Live`.
  - Tool execution shows colored status panels (yellow for running, green for success, red for failure).
  - `/agent rex` switches the active agent mid-session.
  - `/agents` lists all agents available in the current tenant.
  - Response timeout (5 min) shows error message.
  - Ctrl+C gracefully disconnects.
- **Estimated effort:** M

---

## Stage 3E: Sandbox System

Port the Docker and Incus sandbox system. The sandbox provides isolated code execution environments per user. The `UnifiedSandboxManager` pattern (auto-select backend, fallback chain) ports cleanly; the main change is tenant-scoping sandbox sessions.

### WI-3.13: Docker + Incus Sandbox Port

- **Depends on:** Phase 2 (tool executor)
- **Port from:** `../mypalclara/mypalclara/sandbox/docker.py` (~1,160 lines — `DockerSandboxManager` with per-user containers, stateful Python execution, file I/O, package installation, web search, git integration, idle timeout cleanup), `../mypalclara/mypalclara/sandbox/incus.py` (~850 lines — `IncusSandboxManager` with Incus CLI subprocess wrapper, container/VM mode, Python profile setup, same API surface as Docker), `../mypalclara/mypalclara/sandbox/manager.py` (350 lines — `UnifiedSandboxManager` with auto mode selection, backend fallback, unified forwarding, lifecycle management)
- **Deliverables:**
  - `mypal/tools/sandbox/__init__.py`
  - `mypal/tools/sandbox/docker.py` — Port `DockerSandboxManager`. Keep: `ExecutionResult` dataclass, per-user container lifecycle, `execute_code`, `run_shell`, `install_package`, `ensure_packages`, `read_file`, `write_file`, `list_files`, `unzip_file`, `web_search`, idle timeout cleanup, Docker availability detection. Change: session key from `user_id` to `(tenant_id, user_id)` for tenant isolation. Add: resource limits configurable per tenant.
  - `mypal/tools/sandbox/incus.py` — Port `IncusSandboxManager`. Keep: `IncusSession` tracking, `_run_incus` subprocess wrapper, container/VM mode, Python profile initialization, same method surface. Change: instance naming from `clara-{user_id}` to `mypal-{tenant_id}-{user_id}` for tenant isolation.
  - `mypal/tools/sandbox/manager.py` — Port `UnifiedSandboxManager`. Keep: auto/docker/incus/incus-vm mode selection, lazy initialization, health check, stats, cleanup. Change: all methods accept `tenant_id` parameter alongside `user_id`.
- **Acceptance criteria:**
  - `manager.execute_code("t-abc", "user1", "print('hello')")` runs in an isolated Docker container and returns `ExecutionResult(success=True, output="hello\n")`.
  - Two users in the same tenant get separate containers; variables don't leak.
  - Two users in different tenants get separate containers even if `user_id` collides.
  - `SANDBOX_MODE=auto` uses Incus if available, Docker otherwise.
  - `SANDBOX_MODE=incus-vm` creates Incus VMs (not containers).
  - Idle containers are cleaned up after `DOCKER_SANDBOX_TIMEOUT` seconds.
  - `manager.get_stats()` reports active sessions, backend type, and resource usage.
  - `install_package` persists across `execute_code` calls within the same session.
- **Estimated effort:** L

### WI-3.14: Sandbox Tool Integration

- **Depends on:** WI-3.13, Phase 2 (tool registry, tool executor)
- **Port from:** `../mypalclara/mypalclara/sandbox/docker.py` (`DOCKER_TOOLS` list — tool definitions for `execute_python`, `install_package`, `read_file`, `write_file`, `list_files`, `unzip_file`, `run_shell`, `web_search`)
- **Deliverables:**
  - `mypal/tools/builtins/sandbox_tools.py` — Register sandbox tools with MyPal's tool registry. Each tool wraps a `UnifiedSandboxManager` method. Tools: `execute_python`, `install_package`, `read_file`, `write_file`, `list_files`, `run_shell`. Tool definitions use the registry's format (not raw OpenAI dict). Tenant-scoped: tools automatically inject `tenant_id` from the current session context.
  - `mypal/tools/sandbox/cleanup.py` — Background task (arq job) for idle session cleanup. Runs every 5 minutes. Calls `manager.cleanup_idle_sessions()` for each backend.
- **Acceptance criteria:**
  - Agent can call `execute_python` tool and the code runs in a sandbox.
  - Tool executor injects `tenant_id` from session context; no tool can access another tenant's sandbox.
  - Sandbox tools appear in the tool registry and respect the three-level permission system (ALLOW/DENY/ASK per user).
  - Cleanup job removes containers idle for >15 minutes (configurable).
  - `get_stats()` endpoint reports sandbox health.
- **Estimated effort:** M

---

## Stage 3F: Rook v2 FSRS Dynamics

Implement FSRS-6 memory dynamics within Rook v2. The core FSRS algorithm is already implemented in mypalclara; this stage integrates it with Rook's storage and retrieval pipelines and adds the reinforcement signal system that drives memory strength updates without explicit user review.

### WI-3.15: FSRS State Machine and Core Integration

- **Depends on:** Phase 2 (memory models, Rook manager)
- **Port from:** `../mypalclara/mypalclara/core/memory/dynamics/fsrs.py` (511 lines — `Grade` enum, `FsrsParams` (21 FSRS-6 parameters), `MemoryState` dataclass, `ReviewResult`, core functions: `retrievability()` (power-law decay), `initial_stability()`, `initial_difficulty()`, `update_stability_success()`, `update_stability_failure()`, `update_dual_strength()` (Bjork's desirable difficulty), `review()` (main entry point), `infer_grade_from_signal()`, `calculate_memory_score()`)
- **Deliverables:**
  - `mypal/memory/dynamics.py` — Port all FSRS functions verbatim. The math is well-tested and correct. Add: `MemoryPhase` enum (`NEW`, `LEARNING`, `REVIEW`, `RELEARNING`) and phase transition logic. `NEW` -> `LEARNING` on first signal. `LEARNING` -> `REVIEW` after stability exceeds threshold (configurable, default 1 day). `REVIEW` -> `RELEARNING` on `AGAIN` grade. `RELEARNING` -> `REVIEW` on successful recall.
  - `mypal/db/models/memory.py` (update) — Add FSRS columns to the memory model: `fsrs_stability` (float), `fsrs_difficulty` (float), `fsrs_retrieval_strength` (float), `fsrs_storage_strength` (float), `fsrs_last_review` (datetime), `fsrs_review_count` (int), `fsrs_phase` (enum). These are nullable for backward compatibility; memories without FSRS state are treated as `NEW`.
  - Alembic migration adding FSRS columns.
- **Acceptance criteria:**
  - `review(state, Grade.GOOD)` returns a `ReviewResult` with updated stability, difficulty, and dual-strength values matching the FSRS-6 algorithm.
  - `retrievability(days_elapsed=10, stability=5)` returns a value less than 0.9 (power-law decay).
  - Phase transitions: a new memory in `NEW` phase transitions to `LEARNING` after first signal, then to `REVIEW` after stability exceeds 1 day.
  - `REVIEW` memory receiving `Grade.AGAIN` transitions to `RELEARNING`.
  - `MemoryState` round-trips to/from DB columns.
  - Migration runs cleanly; existing memories get `NULL` FSRS fields (treated as `NEW`).
- **Estimated effort:** M

### WI-3.16: Retrieval-Time FSRS Scoring

- **Depends on:** WI-3.15, Phase 2 (retriever)
- **Port from:** Architecture spec section 5.5 (pseudocode for FSRS-weighted retrieval ranking)
- **Deliverables:**
  - `mypal/memory/retriever.py` (update) — Integrate FSRS scoring into the retrieval pipeline. After vector search returns candidates, calculate current `retrievability` for each memory using `fsrs.retrievability(elapsed_days, stability)`. Compute composite `retrieval_score = similarity * 0.5 + retrievability * 0.3 + importance * 0.2` (weights from architecture spec). Sort by `retrieval_score` descending. Apply token budget.
  - `mypal/memory/dynamics.py` (update) — Add `score_for_retrieval(memory, now)` helper that computes the current retrievability and composite score for a given memory at a given time.
- **Acceptance criteria:**
  - A memory with high similarity but low retrievability (not accessed in months, low stability) ranks lower than a memory with moderate similarity but high retrievability.
  - A memory reviewed yesterday with stability=10 has retrievability near 1.0.
  - A memory last reviewed 30 days ago with stability=5 has retrievability significantly below 0.9.
  - Token budget is respected: if top-ranked memories exceed budget, lower-ranked ones are dropped.
  - Memories with `NULL` FSRS fields (pre-migration) default to retrievability=0.5 (neutral, don't dominate or disappear).
- **Estimated effort:** M

### WI-3.17: Reinforcement Signals

- **Depends on:** WI-3.15, WI-3.16, Phase 2 (memory writer, agent runtime)
- **Port from:** `../mypalclara/mypalclara/core/memory/dynamics/fsrs.py` (`infer_grade_from_signal` function — maps signal types to FSRS grades)
- **Deliverables:**
  - `mypal/memory/signals.py` — `MemorySignalProcessor`. Listens for usage events and applies FSRS reviews to affected memories. Signal types and their inferred grades:
    - `retrieved` (memory appeared in retrieval results): `Grade.GOOD` — basic reinforcement
    - `referenced` (agent explicitly used this memory in response): `Grade.EASY` — strong success
    - `confirmed` (user confirmed or agreed with memory content): `Grade.EASY`
    - `corrected` (user contradicted or corrected memory): `Grade.AGAIN` — triggers supersession check
    - `contradicted` (new information contradicts this memory): `Grade.AGAIN`
    - `partial_recall` (memory retrieved but not used): `Grade.HARD`
  - `mypal/memory/signals.py` — Integration with agent runtime: emit `retrieved` signal when memories are included in context; emit `referenced` when agent cites a memory; emit `corrected` when extraction detects contradiction with existing memory.
- **Acceptance criteria:**
  - When a memory is retrieved and included in agent context, its FSRS state updates with `Grade.GOOD`.
  - When a user corrects a memory ("actually I live in Portland, not Seattle"), the old memory receives `Grade.AGAIN` (stability drops) and a supersession check triggers.
  - Signal processing is async/background (does not block the response pipeline).
  - Signal processing is idempotent: the same signal for the same memory in the same session is applied only once.
  - Signal history is logged for debugging (not persisted long-term).
- **Estimated effort:** M

### WI-3.18: Scope-Specific Decay Multipliers

- **Depends on:** WI-3.15, WI-3.16
- **Port from:** Architecture spec section 5.5 (`SCOPE_STABILITY_MULTIPLIER` dict)
- **Deliverables:**
  - `mypal/memory/dynamics.py` (update) — Apply scope-specific stability multipliers during FSRS state initialization and retrieval scoring. Multipliers per architecture spec:
    - `SYSTEM`: 10.0x (system knowledge barely decays)
    - `TENANT`: 5.0x (org knowledge is durable)
    - `AGENT`: 3.0x (agent's own knowledge)
    - `USER`: 2.0x (user facts)
    - `USER_AGENT`: 1.0x (relationship-specific, standard decay)
    - `SESSION`: 0.1x (session context decays fast)
  - Multiplier is applied to initial stability: `initial_stability(grade, params) * SCOPE_STABILITY_MULTIPLIER[scope]`.
  - Multiplier also affects retrievability calculation: `effective_stability = stability * multiplier`.
- **Acceptance criteria:**
  - A `SYSTEM`-scoped memory has 10x the effective stability of a `USER_AGENT`-scoped memory with the same raw FSRS state.
  - A `SESSION`-scoped memory with stability=1 day has effective stability of 0.1 days (decays in ~2.4 hours to R=0.9).
  - `TENANT`-scoped memory remains retrievable for weeks without reinforcement.
  - Multipliers are configurable per tenant (stored in tenant config, defaults to architecture spec values).
- **Estimated effort:** S

---

## Stage 3G: Cross-Agent Memory

Implement the cross-agent memory sharing model from architecture spec section 5.8. Agents within a tenant can read each other's knowledge, with a permission model controlling visibility. Also implement the group conversation model from section 9.2.

### WI-3.19: Cross-Agent Memory Sharing

- **Depends on:** WI-3.15, Phase 2 (memory scopes, retriever)
- **Port from:** Architecture spec section 5.8 ("Agents within the same tenant can read each other's AGENT-scoped memories by default")
- **Deliverables:**
  - `mypal/memory/sharing.py` — `CrossAgentMemoryService`. When an agent retrieves memories, it can optionally include other agents' `AGENT`-scoped memories from the same tenant. The retriever is updated to accept a `include_cross_agent: bool` flag. Cross-agent memories are ranked lower by default (multiplied by 0.7 relevance factor) to prefer the agent's own knowledge.
  - `mypal/memory/retriever.py` (update) — Add cross-agent retrieval path. When `include_cross_agent=True`, query vector store for `AGENT`-scoped memories where `agent_id != current_agent AND tenant_id == current_tenant AND visibility IN (TENANT_READ, TENANT_WRITE)`.
- **Acceptance criteria:**
  - Agent Clara can retrieve Agent Rex's `AGENT`-scoped memories when both belong to the same tenant.
  - Cross-agent memories appear in results but rank below Clara's own memories (0.7 factor applied).
  - Memories with `visibility=PRIVATE` are never returned to other agents.
  - Cross-agent retrieval is off by default; agents opt in via their `memory_config`.
  - An agent in tenant A cannot access memories from tenant B regardless of visibility settings.
- **Estimated effort:** M

### WI-3.20: Memory Visibility Permission Model

- **Depends on:** WI-3.19, Phase 2 (memory models)
- **Port from:** Architecture spec section 5.8 ("Permission model: PRIVATE, TENANT_READ, TENANT_WRITE, EXPLICIT")
- **Deliverables:**
  - `mypal/memory/visibility.py` — `MemoryVisibility` enum and enforcement.
    - `PRIVATE`: Only the owning agent can read/write. Default for `USER_AGENT` scope.
    - `TENANT_READ`: Any agent in the same tenant can read. Default for `AGENT` scope.
    - `TENANT_WRITE`: Any agent in the same tenant can read or update (e.g., add reinforcement signals).
    - `EXPLICIT`: Only agents explicitly listed in `memory.shared_with` can access.
  - `mypal/db/models/memory.py` (update) — Add `visibility` column (enum, default `TENANT_READ` for `AGENT` scope, `PRIVATE` for `USER_AGENT` scope) and `shared_with` column (JSON array of agent_ids, used only for `EXPLICIT` visibility).
  - `mypal/memory/writer.py` (update) — Enforce visibility on write. Set default visibility based on scope. Allow explicit override via `visibility` parameter.
  - `mypal/memory/retriever.py` (update) — Filter by visibility during retrieval.
  - Alembic migration adding `visibility` and `shared_with` columns.
- **Acceptance criteria:**
  - New `AGENT`-scoped memory defaults to `TENANT_READ` visibility.
  - New `USER_AGENT`-scoped memory defaults to `PRIVATE` visibility.
  - A memory with `PRIVATE` visibility is returned only when the requesting agent matches the owning agent.
  - A memory with `EXPLICIT` visibility and `shared_with=["rex"]` is returned to Rex but not to Clara.
  - `TENANT_WRITE` allows another agent to add a reinforcement signal to the memory.
  - Visibility can be changed after creation via the memory API.
- **Estimated effort:** M

### WI-3.21: Sub-Agent Memory Inheritance

- **Depends on:** WI-3.19, WI-3.20, Phase 2 (sub-agent orchestrator)
- **Port from:** Architecture spec section 3.3 ("Sub-agents receive scoped context from the parent")
- **Deliverables:**
  - `mypal/memory/inheritance.py` — `SubAgentMemoryProvider`. When a parent agent spawns a sub-agent, the sub-agent receives a scoped view of the parent's memory. Rules:
    - Sub-agent can read parent's `AGENT`-scoped memories (filtered by task relevance).
    - Sub-agent can read `USER` and `USER_AGENT` memories for the current user (inherited from parent's session context).
    - Sub-agent cannot write to parent's memory scopes. Writes go to the sub-agent's own ephemeral scope.
    - On sub-agent completion, parent can selectively promote sub-agent memories to its own scope.
  - `mypal/agents/orchestrator.py` (update) — When creating a sub-agent runtime, inject `SubAgentMemoryProvider` as the memory interface.
- **Acceptance criteria:**
  - Sub-agent can retrieve parent's relevant `AGENT`-scoped memories during task execution.
  - Sub-agent's memory writes are isolated (ephemeral scope, cleaned up on completion).
  - Parent agent can call `promote_memories(sub_agent_result)` to persist selected sub-agent discoveries.
  - Sub-agent cannot access other tenants' or other parents' memories.
  - Memory inheritance depth matches sub-agent depth limit (max 2 per architecture spec).
- **Estimated effort:** M

### WI-3.22: Memory Cleanup Background Job

- **Depends on:** WI-3.15, WI-3.16, WI-3.17
- **Port from:** New
- **Deliverables:**
  - `mypal/memory/cleanup.py` — `MemoryCleanupJob` (arq background task). Runs periodically (configurable, default daily). Operations:
    - **Soft-delete faded memories:** Memories where `retrievability < 0.05` AND `storage_strength < 0.1` AND `phase == RELEARNING` AND `last_review > 90 days ago`. Marks as `status=FADED` (soft delete, not hard delete). These are excluded from retrieval but preserved for audit.
    - **Soft-delete superseded memories:** Memories where `superseded_by IS NOT NULL` AND `superseded_at > 30 days ago`. The superseding memory is the active one; the old memory is kept for history but excluded from retrieval.
    - **Session scope cleanup:** `SESSION`-scoped memories older than 7 days are hard-deleted (these are ephemeral by design).
    - **Orphaned memory cleanup:** Memories whose owning agent or tenant no longer exists.
  - `mypal/api/v1/admin.py` (update) — Add `/admin/memory/cleanup` endpoint to trigger cleanup manually and `/admin/memory/stats` to show cleanup candidates.
- **Acceptance criteria:**
  - Faded memories (R < 0.05, low storage strength, stale) are marked `FADED` and excluded from retrieval results.
  - Superseded memories older than 30 days are soft-deleted.
  - Session-scoped memories older than 7 days are hard-deleted.
  - Cleanup job runs without blocking the main application.
  - `/admin/memory/stats` reports: total memories, faded count, superseded count, session cleanup candidates.
  - Cleanup is tenant-isolated: job processes one tenant at a time, no cross-tenant data access.
  - Soft-deleted memories can be restored via admin API if needed.
- **Estimated effort:** M

### WI-3.23: Group Conversation Model

- **Depends on:** WI-3.19, WI-3.20, Phase 2 (session model, agent router)
- **Port from:** Architecture spec section 9.2 (`Conversation` dataclass with participants, agents, shared message history)
- **Deliverables:**
  - `mypal/db/models/conversation.py` — `Conversation` model: `id`, `tenant_id`, `channel_id`, `participants` (user_id list), `agents` (agent_id list), `created_at`, `updated_at`. Represents a multi-user, multi-agent conversation in a single channel.
  - `mypal/sessions/conversation.py` — `ConversationManager`. Creates/retrieves conversations for group channels. Each agent maintains its own `Session` within the conversation (per architecture spec: "each agent maintains its own Session within the conversation for memory continuity, but sees the shared message history"). Handles: participant join/leave, agent activation/deactivation, shared message history access.
  - `mypal/sessions/session.py` (update) — Add `conversation_id` foreign key to `Session` model. A session with `conversation_id=None` is a 1:1 chat; with a value, it's part of a group conversation.
  - Alembic migration for `conversations` table and `conversation_id` FK on `sessions`.
- **Acceptance criteria:**
  - In a Discord server channel with multiple users, a `Conversation` is created automatically.
  - When user A talks to Clara and user B talks to Rex in the same channel, both agents see the shared message history but maintain separate sessions (separate memory contexts).
  - `conversation.participants` tracks who has spoken in the conversation.
  - `conversation.agents` tracks which agents have been invoked.
  - Each agent's session within the conversation has its own `summary` and `last_activity_at`.
  - 1:1 DMs don't create a `Conversation` record (simple session only).
  - Migration runs cleanly; existing sessions get `conversation_id=NULL`.
- **Estimated effort:** M

---

## Dependency Graph

```
Phase 2 ─┬─► WI-3.1 (Adapter Base) ─┬─► WI-3.2 (Tenant Resolver) ─► WI-3.3 (Normalizer)
          │                           │         │                           │
          │                           │         ├─► WI-3.5 (Discord Tenant) │
          │                           │         │                           │
          │                           │   WI-3.3 ┼─► WI-3.4 (Discord Core) ┼─► WI-3.6 (Discord Routing)
          │                           │         │                           │
          │                           │         ├─► WI-3.9 (Slack)          │
          │                           │         ├─► WI-3.10 (Teams)         │
          │                           │         ├─► WI-3.11 (Telegram)      │
          │                           │         └─► WI-3.12 (CLI)           │
          │                           │                                     │
          │                           └─► WI-3.7 (Stream Protocol) ────► WI-3.8 (Stream Integration)
          │
          ├─► WI-3.13 (Sandbox Port) ─► WI-3.14 (Sandbox Tools)
          │
          ├─► WI-3.15 (FSRS Core) ─┬─► WI-3.16 (FSRS Retrieval)
          │                         ├─► WI-3.17 (Signals) ─► WI-3.22 (Cleanup Job)
          │                         └─► WI-3.18 (Scope Multipliers)
          │
          └─► WI-3.19 (Cross-Agent) ─┬─► WI-3.20 (Visibility Model)
                                      ├─► WI-3.21 (Sub-Agent Inheritance)
                                      └─► WI-3.23 (Group Conversations)
```

## Effort Summary

| Work Item | Stage | Effort | Priority |
|-----------|-------|--------|----------|
| WI-3.1  Adapter Base + Protocols | 3A | M | P0 |
| WI-3.2  Tenant Resolver | 3A | M | P0 |
| WI-3.3  IncomingMessage Normalization | 3A | M | P0 |
| WI-3.4  Discord Adapter Core | 3B | L | P0 |
| WI-3.5  Discord Multi-Tenant | 3B | M | P0 |
| WI-3.6  Discord Agent Routing | 3B | M | P0 |
| WI-3.7  Streaming Protocol | 3C | M | P0 |
| WI-3.8  Stream Integration | 3C | M | P0 |
| WI-3.9  Slack Adapter | 3D | L | P1 |
| WI-3.10 Teams Adapter (Port) | 3D | L | P1 |
| WI-3.11 Telegram Adapter | 3D | M | P2 |
| WI-3.12 CLI Adapter (Port) | 3D | M | P1 |
| WI-3.13 Sandbox Port | 3E | L | P1 |
| WI-3.14 Sandbox Tool Integration | 3E | M | P1 |
| WI-3.15 FSRS State Machine | 3F | M | P0 |
| WI-3.16 FSRS Retrieval Scoring | 3F | M | P0 |
| WI-3.17 Reinforcement Signals | 3F | M | P1 |
| WI-3.18 Scope Decay Multipliers | 3F | S | P1 |
| WI-3.19 Cross-Agent Sharing | 3G | M | P1 |
| WI-3.20 Visibility Model | 3G | M | P1 |
| WI-3.21 Sub-Agent Inheritance | 3G | M | P2 |
| WI-3.22 Memory Cleanup Job | 3G | M | P1 |
| WI-3.23 Group Conversations | 3G | M | P2 |

**Total: 23 work items** (4S + 13M + 6L)

## Suggested Execution Order

1. **Critical path (P0):** WI-3.1 -> WI-3.2 -> WI-3.3 -> WI-3.7 -> WI-3.4 (Discord working end-to-end)
2. **FSRS core (P0, parallelizable with Discord):** WI-3.15 -> WI-3.16
3. **Discord multi-tenant + routing:** WI-3.5 -> WI-3.6 -> WI-3.8
4. **Sandbox + signals (P1):** WI-3.13 -> WI-3.14, WI-3.17, WI-3.18
5. **Additional adapters (P1):** WI-3.12 (CLI), WI-3.10 (Teams), WI-3.9 (Slack)
6. **Cross-agent memory (P1):** WI-3.19 -> WI-3.20 -> WI-3.22
7. **P2 items (after core stable):** WI-3.11 (Telegram), WI-3.21 (Sub-Agent Inheritance), WI-3.23 (Group Conversations)
