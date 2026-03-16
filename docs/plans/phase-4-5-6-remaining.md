# MyPal Implementation Plan: Phases 4, 5 & 6

**Covers:** Weeks 15-26
**Prerequisite:** Phases 1-3 complete (FastAPI skeleton, Clerk auth, agent runtime, Rook v2 FSRS, adapters, streaming protocol)
**Architecture reference:** `docs/MyPal_Master_Architecture.md` sections 6, 8, 11, 12, 15

---

## Phase 4: Multi-Input (Weeks 15-18)

Phase 4 turns MyPal from a chat-only platform into an input-agnostic system. Every trigger — REST call, WebSocket stream, cron job, webhook, email — produces an `IncomingMessage` that flows through the same agent pipeline. The loopback dispatcher is the key architectural piece: non-chat sources create synthetic messages that the agent processes identically to human input.

### Stage 4A: API Gateway (REST + WebSocket)

Exposes the agent pipeline to external clients (web UI, mobile apps, third-party integrations) through authenticated HTTP and WebSocket endpoints.

#### WI-4A.1: REST Chat Endpoint
- **Depends on:** Phase 3 (agent runtime, streaming protocol)
- **Port from:** New (replaces Rails API)
- **Deliverables:**
  - `mypal/api/v1/chat.py` — `POST /api/v1/chat` synchronous and `POST /api/v1/chat/stream` SSE endpoints
  - `mypal/inputs/message.py` — `IncomingMessage` Pydantic model (from arch doc section 6.1)
- **Acceptance criteria:**
  - `POST /api/v1/chat` with `{"text": "hello", "agent_id": "clara"}` returns a JSON response with the agent's reply
  - Request with invalid/missing Clerk JWT returns 401
  - Request to agent the user lacks access to returns 403
  - Response includes `conversation_id` for continuity
- **Estimated effort:** M

#### WI-4A.2: WebSocket Streaming Endpoint
- **Depends on:** WI-4A.1
- **Port from:** New (the existing `useGatewayWebSocket.ts` on `feat/web-ui-rebuild` expects this shape)
- **Deliverables:**
  - `mypal/api/v1/chat.py` — `WS /api/v1/chat/stream` WebSocket handler
  - `mypal/transport/websocket.py` — WebSocket connection manager with heartbeat, auth, reconnect support
- **Acceptance criteria:**
  - WebSocket connects with Clerk JWT in `Authorization` header or first-message auth
  - Server sends chunked `text_delta` frames matching the protocol in `useGatewayWebSocket.ts` (text_delta, tool_call_begin, tool_call_result, done)
  - Heartbeat ping/pong keeps connection alive (30s interval)
  - Concurrent connections from same user to different agents work independently
  - Connection drops trigger cleanup of in-progress streams
- **Estimated effort:** L

#### WI-4A.3: Agent Management Endpoints
- **Depends on:** Phase 2 (agent registry)
- **Port from:** New
- **Deliverables:**
  - `mypal/api/v1/agents.py` — CRUD for agent definitions, list agents for tenant
- **Acceptance criteria:**
  - `GET /api/v1/agents` returns all agents visible to the current user's tenant
  - `POST /api/v1/agents` (admin+ only) creates a new agent with persona, LLM config, tools
  - `PATCH /api/v1/agents/{id}` updates agent configuration
  - `DELETE /api/v1/agents/{id}` soft-deletes (admin+ only)
  - Non-admin users get 403 on write operations
- **Estimated effort:** M

#### WI-4A.4: Task and Webhook Registration Endpoints
- **Depends on:** WI-4A.1
- **Port from:** New
- **Deliverables:**
  - `mypal/api/v1/tasks.py` — CRUD for scheduled tasks
  - `mypal/api/v1/webhooks.py` — webhook registration and management
- **Acceptance criteria:**
  - `POST /api/v1/tasks` creates a scheduled task (cron, one-shot, or interval) scoped to the tenant
  - `GET /api/v1/tasks` lists tasks for the current tenant
  - `POST /api/v1/webhooks` registers a webhook endpoint with a signing secret
  - `GET /api/v1/webhooks` lists registered webhooks for the tenant
  - All endpoints enforce tenant isolation via middleware
- **Estimated effort:** M

### Stage 4B: Loopback Dispatcher

The loopback dispatcher converts non-chat triggers into synthetic `IncomingMessage` instances that enter the standard agent pipeline. This is the architectural guarantee that scheduled tasks, webhooks, and email events get memory context, respect tool permissions, and can route responses to any channel.

#### WI-4B.1: LoopbackDispatcher Core
- **Depends on:** WI-4A.1 (IncomingMessage model)
- **Port from:** New (design from arch doc section 6.4)
- **Deliverables:**
  - `mypal/inputs/loopback.py` — `LoopbackDispatcher` class
  - `mypal/db/models.py` — `ChannelTarget` model (for response routing)
- **Acceptance criteria:**
  - `LoopbackDispatcher.dispatch(tenant_id, agent_id, content, source="scheduler")` creates an `IncomingMessage` with `platform="loopback"` and `channel_id="loopback:scheduler"`
  - The synthetic message routes through the agent runtime identically to a user message
  - Agent response is delivered to `target_channel` if specified, otherwise stored in loopback channel history
  - System user is resolved for the tenant when no `user_id` is provided
  - Metadata includes `{"source": "scheduler", "loopback": true}`
- **Estimated effort:** M

#### WI-4B.2: Loopback Response Routing
- **Depends on:** WI-4B.1
- **Port from:** New
- **Deliverables:**
  - `mypal/inputs/loopback.py` — response routing logic in `LoopbackDispatcher`
  - `mypal/transport/stream.py` — `ChannelTarget` resolution (adapter type + channel ID)
- **Acceptance criteria:**
  - A loopback message with `target_channel={"platform": "discord", "channel_id": "12345"}` delivers the agent response to Discord channel 12345
  - A loopback message with no target_channel stores the response in the database (retrievable via API)
  - Response routing failure logs an error but does not crash the pipeline
  - Integration test: scheduler triggers loopback -> agent processes -> response appears in target
- **Estimated effort:** M

### Stage 4C: Scheduler Event Source

Port and evolve MyPalClara's scheduler. The existing `scheduler.py` (729 lines) supports cron, one-shot, and interval tasks with shell command and Python handler execution. MyPal's version adds tenant scoping, DB persistence, loopback dispatch, and a conversational creation tool.

#### WI-4C.1: Scheduler Engine (Port + Evolve)
- **Depends on:** WI-4B.1 (LoopbackDispatcher)
- **Port from:** `../mypalclara/mypalclara/gateway/scheduler.py` (729 lines — `CronParser`, `Scheduler`, `ScheduledTask`, `TaskResult` classes)
- **Deliverables:**
  - `mypal/services/scheduler.py` — ported scheduler with tenant scoping
  - `mypal/inputs/events.py` — `SchedulerEventSource` wrapper that connects scheduler to loopback
- **Acceptance criteria:**
  - `CronParser` passes all existing cron expression tests (minute, hour, day, month, weekday, ranges, steps, lists)
  - `CronParser.next_run("0 9 * * 1-5")` returns next weekday at 9 AM
  - Scheduler supports cron, one-shot (ISO 8601 datetime or delay-seconds), and interval task types
  - Each task is scoped to a `tenant_id` and targets an `agent_id`
  - When a task fires, it calls `LoopbackDispatcher.dispatch()` with the task's content and metadata
  - `Scheduler.start()` begins the async loop; `Scheduler.stop()` cancels cleanly
  - Task execution results are persisted to the DB (not just in-memory like the original)
- **Estimated effort:** L

#### WI-4C.2: Scheduled Task DB Model + CRUD
- **Depends on:** WI-4C.1
- **Port from:** New (original uses YAML config + in-memory state)
- **Deliverables:**
  - `mypal/db/models.py` — `ScheduledTask` SQLAlchemy model (name, tenant_id, agent_id, type, cron/interval/run_at, content, target_channel, enabled, last_run, next_run, run_count)
  - `mypal/db/models.py` — `TaskExecution` model (task_id, success, output, error, duration_ms, timestamp)
  - Alembic migration for both tables
- **Acceptance criteria:**
  - `ScheduledTask` records survive server restarts (loaded from DB on startup)
  - `TaskExecution` history is queryable per-task with pagination
  - Unique constraint on `(tenant_id, name)` prevents duplicate task names per tenant
  - Soft-delete support (disabled flag vs hard delete)
- **Estimated effort:** M

#### WI-4C.3: Conversational Task Creation Tool
- **Depends on:** WI-4C.2
- **Port from:** New (arch doc section 6.4: "Users can create tasks conversationally")
- **Deliverables:**
  - `mypal/tools/builtins/schedule_task.py` — `create_scheduled_task` tool callable by agents
- **Acceptance criteria:**
  - User says "Remind me every Monday at 9am to check the pipeline" and agent calls `create_scheduled_task` tool with `{"name": "pipeline-check", "cron": "0 9 * * 1", "content": "Time to check the pipeline!", "agent_id": "clara"}`
  - Tool validates cron expression before creating task (rejects invalid expressions)
  - Tool returns confirmation with next 3 scheduled run times
  - Tool respects tenant scoping (task created under the user's tenant)
  - User can also say "Cancel the pipeline-check reminder" to disable it
- **Estimated effort:** M

### Stage 4D: Webhook Event Source

Incoming webhooks from external services (GitHub, CI/CD, monitoring) are converted to loopback messages so agents can process them.

#### WI-4D.1: Webhook Receiver + Registration
- **Depends on:** WI-4B.1 (LoopbackDispatcher)
- **Port from:** New
- **Deliverables:**
  - `mypal/inputs/webhooks.py` — `WebhookEventSource` class, `WebhookRegistration` model
  - `mypal/api/v1/webhooks.py` — `POST /api/v1/webhooks/incoming/{webhook_id}` receiver endpoint
  - `mypal/db/models.py` — `WebhookRegistration` SQLAlchemy model (id, tenant_id, agent_id, name, secret, url_path, transform_template, enabled)
- **Acceptance criteria:**
  - `POST /api/v1/webhooks` creates a registration and returns a unique webhook URL + signing secret
  - Incoming POST to webhook URL validates HMAC-SHA256 signature header
  - Valid webhook payload is transformed to text (via Jinja2 template or JSON summary) and dispatched through loopback
  - Invalid signature returns 401; disabled webhook returns 404
  - Agent receives the webhook content as a regular message with `metadata.source = "webhook"`
- **Estimated effort:** L

#### WI-4D.2: Webhook Payload Transforms
- **Depends on:** WI-4D.1
- **Port from:** New
- **Deliverables:**
  - `mypal/inputs/webhooks.py` — built-in transforms for GitHub (push, PR, issue), generic JSON summarizer
- **Acceptance criteria:**
  - GitHub push webhook produces: "GitHub: user pushed 3 commits to repo/branch. Latest: 'fix: typo in README'"
  - GitHub PR webhook produces: "GitHub: user opened PR #42 'Add feature X' on repo (3 files changed)"
  - Unknown webhook format produces a JSON summary with top-level keys and truncated values
  - Transform templates are configurable per-registration (admin can edit Jinja2 template via API)
- **Estimated effort:** M

### Stage 4E: Modality Processors (P2)

Voice STT, vision analysis, and document parsing for non-text attachments. These are P2 items but the interfaces should be defined now.

#### WI-4E.1: Modality Processor Interface + Document Parser
- **Depends on:** WI-4A.1 (IncomingMessage with attachments)
- **Port from:** New (design from arch doc section 6.5)
- **Deliverables:**
  - `mypal/inputs/modalities/__init__.py` — `ModalityProcessor` ABC
  - `mypal/inputs/modalities/document.py` — `DocumentProcessor` (PDF, DOCX, TXT parsing)
- **Acceptance criteria:**
  - `ModalityProcessor.process(attachment)` returns a `ProcessedInput` with extracted text and metadata
  - `DocumentProcessor` handles PDF (via `pymupdf` or `pdfplumber`), DOCX (via `python-docx`), and plain text
  - Extracted text is truncated to configurable max length (default 10,000 chars) with a note if truncated
  - Unsupported file types return a `ProcessedInput` with `text="[Unsupported file type: .xyz]"`
- **Estimated effort:** M

#### WI-4E.2: Voice STT Processor
- **Depends on:** WI-4E.1
- **Port from:** New
- **Deliverables:**
  - `mypal/inputs/modalities/voice.py` — `VoiceProcessor` using Whisper API or local model
- **Acceptance criteria:**
  - Audio attachments (wav, mp3, ogg, webm) are transcribed to text
  - Transcription result includes detected language and confidence
  - Configurable provider: OpenAI Whisper API (default) or local whisper.cpp
  - Transcription errors produce a graceful fallback: `"[Voice message could not be transcribed]"`
- **Estimated effort:** M

#### WI-4E.3: Vision Processor
- **Depends on:** WI-4E.1
- **Port from:** New
- **Deliverables:**
  - `mypal/inputs/modalities/vision.py` — `VisionProcessor` (image description, OCR)
- **Acceptance criteria:**
  - Image attachments are passed to the LLM's vision capability (if available) for description
  - OCR fallback via `pytesseract` or similar for text extraction from images
  - Result includes both description and any extracted text
  - Processor respects per-agent LLM config (uses agent's configured provider for vision)
- **Estimated effort:** M

### Stage 4F: Tenant-Scoped API Keys + Format Enforcement

Programmatic access for integrations that cannot use Clerk JWTs (CI/CD, scripts, external services).

#### WI-4F.1: API Key Management
- **Depends on:** Phase 1 (tenant model, auth middleware)
- **Port from:** New
- **Deliverables:**
  - `mypal/auth/api_keys.py` — API key generation, validation, rotation
  - `mypal/db/models.py` — `APIKey` model (id, tenant_id, created_by, name, key_hash, scopes, last_used, expires_at, revoked)
  - `mypal/api/v1/auth.py` — key management endpoints
- **Acceptance criteria:**
  - `POST /api/v1/api-keys` generates a key (shown once, stored as bcrypt hash)
  - Keys include configurable scopes: `["chat", "memory:read", "memory:write", "tasks", "webhooks", "admin"]`
  - API requests with `Authorization: Bearer mypal_...` header resolve to a tenant + user
  - Key rotation: create new key, grace period for old key, revoke old key
  - `GET /api/v1/api-keys` lists keys (name, scopes, last_used, created_at — never the key value)
  - Revoked or expired keys return 401
- **Estimated effort:** M

#### WI-4F.2: IncomingMessage Format Enforcement
- **Depends on:** WI-4A.1, WI-4B.1
- **Port from:** New
- **Deliverables:**
  - `mypal/inputs/message.py` — Pydantic validation, normalization middleware
- **Acceptance criteria:**
  - Every entry point (REST, WebSocket, adapter, loopback, webhook) produces a validated `IncomingMessage`
  - Missing `tenant_id` is rejected (400) at the API level, resolved from auth context internally
  - Missing `agent_id` is allowed (router selects based on rules from arch doc section 3.4)
  - `timestamp` defaults to `utcnow()` if not provided
  - `platform` field is set correctly per source: "api", "websocket", "discord", "loopback", "webhook", etc.
  - Attachment size limits enforced (configurable per tenant, default 25MB)
  - Integration test: send malformed messages through each entry point, verify rejection with descriptive errors
- **Estimated effort:** S

---

## Phase 5: Web UI & Polish (Weeks 19-22)

Phase 5 is a port-and-evolve, not a build-from-scratch. The `feat/web-ui-rebuild` branch of MyPalClara contains a functional React app with chat streaming, knowledge base, settings, and Clerk auth. The work here is adapting it for multi-tenant, multi-agent MyPal.

**Source:** `../mypalclara/` branch `feat/web-ui-rebuild`, path `web-ui/frontend/src/`

**Verified files on `feat/web-ui-rebuild`:**
- `auth/`: ClerkProvider.tsx, TokenBridge.tsx
- `stores/`: chatStore.ts, chatRuntime.ts, artifactStore.ts, savedSets.ts
- `hooks/`: useGatewayWebSocket.ts, useMemories.ts, useBranches.ts, useTheme.ts
- `api/`: client.ts
- `lib/`: utils.ts, attachmentAdapter.ts, threadGroups.ts
- `utils/`: fileProcessing.ts
- `components/ui/`: 17 shadcn primitives (avatar, badge, button, card, collapsible, dialog, dropdown-menu, input, label, scroll-area, select, separator, sheet, skeleton, tabs, textarea, tooltip)
- `components/assistant-ui/`: attachment.tsx, markdown-text.tsx, thread-list.tsx, thread.tsx, tool-fallback.tsx, tooltip-icon-button.tsx
- `components/chat/`: ArtifactPanel.tsx, BranchSidebar.tsx, ChatRuntimeProvider.tsx, MergeDialog.tsx, TierSelector.tsx, ToolCallBlock.tsx
- `components/knowledge/`: MemoryCard.tsx, MemoryEditor.tsx, MemoryGrid.tsx, MemoryList.tsx, SearchBar.tsx
- `components/layout/`: AppLayout.tsx, UnifiedSidebar.tsx
- `components/settings/`: AdapterLinking.tsx
- `pages/`: Chat.tsx, KnowledgeBase.tsx, Settings.tsx (plus others not ported: Blackjack.tsx, Checkers.tsx, etc.)

### Stage 5A: Foundation Port (Week 19)

Copy the design system, component library, and auth wiring. Establish the web/ directory structure and verify the frontend builds and connects to the FastAPI backend.

#### WI-5A.1: Design System + Scaffolding
- **Depends on:** Phase 4A (FastAPI API endpoints exist)
- **Port from:** `../mypalclara/` branch `feat/web-ui-rebuild`, path `web-ui/frontend/`
- **Deliverables:**
  - `web/package.json` — dependencies (React, Vite, Tailwind, shadcn, @clerk/react, assistant-ui, zustand)
  - `web/vite.config.ts` — Vite config with FastAPI proxy for dev
  - `web/index.html` — entry HTML
  - `web/src/index.css` — oklch design tokens, light/dark mode, shadcn bindings (copy as-is)
  - `web/src/main.tsx` — entry point with ClerkProvider (copy as-is)
  - `web/tsconfig.json`, `web/tailwind.config.ts`, `web/postcss.config.js`
- **Acceptance criteria:**
  - `cd web && npm install && npm run dev` starts Vite dev server without errors
  - Light/dark mode toggle works (oklch color tokens resolve correctly)
  - Vite proxy forwards `/api/*` requests to FastAPI backend on port 8000
  - `npm run build` produces a production bundle without errors
- **Estimated effort:** M

#### WI-5A.2: shadcn/ui Primitives (Copy)
- **Depends on:** WI-5A.1
- **Port from:** `../mypalclara/` branch `feat/web-ui-rebuild`, `web-ui/frontend/src/components/ui/` (17 files, copy as-is)
- **Deliverables:**
  - `web/src/components/ui/` — avatar.tsx, badge.tsx, button.tsx, card.tsx, collapsible.tsx, dialog.tsx, dropdown-menu.tsx, input.tsx, label.tsx, scroll-area.tsx, select.tsx, separator.tsx, sheet.tsx, skeleton.tsx, tabs.tsx, textarea.tsx, tooltip.tsx
- **Acceptance criteria:**
  - All 17 components import without errors
  - `<Button variant="default">Test</Button>` renders with correct oklch styling
  - No references to old identity service or Rails endpoints
- **Estimated effort:** S

#### WI-5A.3: assistant-ui Chat Components (Copy)
- **Depends on:** WI-5A.2
- **Port from:** `../mypalclara/` branch `feat/web-ui-rebuild`, `web-ui/frontend/src/components/assistant-ui/` (6 files, copy as-is: markdown-text.tsx, tooltip-icon-button.tsx; port with modifications: thread.tsx, thread-list.tsx, attachment.tsx, tool-fallback.tsx)
- **Deliverables:**
  - `web/src/components/assistant-ui/` — all 6 files
- **Acceptance criteria:**
  - Markdown rendering works (code blocks, lists, links, bold/italic)
  - Thread component renders message bubbles with user/assistant distinction
  - Thread-list component renders conversation list
  - No runtime errors in browser console
- **Estimated effort:** S

#### WI-5A.4: Clerk Auth Wiring (Copy)
- **Depends on:** WI-5A.1
- **Port from:** `../mypalclara/` branch `feat/web-ui-rebuild`, `web-ui/frontend/src/auth/` (ClerkProvider.tsx, TokenBridge.tsx — copy as-is)
- **Deliverables:**
  - `web/src/auth/ClerkProvider.tsx` — Clerk provider wrapper with publishable key
  - `web/src/auth/TokenBridge.tsx` — bridges Clerk's `getToken()` to the API client
- **Acceptance criteria:**
  - `ClerkProvider` wraps the app and provides `useAuth()`/`useUser()` hooks
  - `TokenBridge` makes Clerk JWT available to `api/client.ts` via context
  - Unauthenticated users see the Clerk sign-in page
  - After sign-in, Clerk `userId` is available throughout the app
- **Estimated effort:** S

#### WI-5A.5: API Client (Port with Modifications)
- **Depends on:** WI-5A.4
- **Port from:** `../mypalclara/` branch `feat/web-ui-rebuild`, `web-ui/frontend/src/api/client.ts` (port with modifications)
- **Deliverables:**
  - `web/src/api/client.ts` — typed API client with tenant header, agent endpoints, Rook v2 memory types
- **Acceptance criteria:**
  - Every API call includes `X-Tenant-ID` header from active tenant context
  - Client exposes: `sendMessage(agentId, text, options)`, `getAgents()`, `getMemories(filters)`, `getThreads(agentId)`, `getPermissions()`, `createTask()`, `getWebhooks()`
  - Memory types match Rook v2 schema: `scope`, `category`, `fsrs_stability`, `retrievability`, `confidence`, `supersedes`
  - Auth header uses Clerk JWT from TokenBridge
  - TypeScript types are exported for use in stores and components
  - 401 responses trigger re-auth via Clerk
- **Estimated effort:** M

#### WI-5A.6: Utility Libraries (Copy)
- **Depends on:** WI-5A.1
- **Port from:** `../mypalclara/` branch `feat/web-ui-rebuild` (copy as-is)
- **Deliverables:**
  - `web/src/lib/utils.ts` — `cn()` classname helper
  - `web/src/lib/attachmentAdapter.ts` — file upload to gateway wire format
  - `web/src/utils/fileProcessing.ts` — client-side file handling
  - `web/src/hooks/useTheme.ts` — light/dark mode hook
  - `web/src/stores/artifactStore.ts` — file/artifact state
  - `web/src/stores/savedSets.ts` — saved filter sets for knowledge base
- **Acceptance criteria:**
  - `cn("base", condition && "conditional")` produces correct class strings
  - `useTheme()` toggles between light and dark mode, persists preference
  - All files import without TypeScript errors
- **Estimated effort:** S

#### WI-5A.7: FastAPI Static Serving + Vite Dev Proxy
- **Depends on:** WI-5A.1
- **Port from:** New
- **Deliverables:**
  - `mypal/main.py` — static file mount for `web/dist/` in production
  - `web/vite.config.ts` — proxy config for `/api` to `localhost:8000` in dev
- **Acceptance criteria:**
  - Dev: `npm run dev` serves frontend on :5173, proxies API calls to FastAPI on :8000
  - Prod: FastAPI serves `web/dist/index.html` at `/`, `web/dist/assets/*` for static assets
  - SPA routing: all non-API, non-asset paths return `index.html` (for client-side routing)
  - `web/Dockerfile` builds frontend and copies dist into a FastAPI-serving image
- **Estimated effort:** S

### Stage 5B: Chat Evolution (Week 20)

Evolve the chat system from single-agent to multi-agent. Add agent selection, agent-scoped threads, and agent context to the message pipeline.

#### WI-5B.1: Agent Store (New)
- **Depends on:** WI-5A.5 (API client with agent endpoints)
- **Port from:** New
- **Deliverables:**
  - `web/src/stores/agentStore.ts` — Zustand store for agent state
- **Acceptance criteria:**
  - Store shape: `{ agents: Agent[], activeAgentId: string | null, isLoading: boolean, error: string | null }`
  - `fetchAgents()` calls `GET /api/v1/agents` and populates the list
  - `setActiveAgent(id)` switches the active agent, persists to `localStorage`
  - On app load, restores last active agent from `localStorage` (falls back to tenant default)
  - Agent type includes: `id`, `name`, `persona.avatar_url`, `capabilities`, `llm_config.model`
  - Re-exports `useAgentStore` hook for components
- **Estimated effort:** S

#### WI-5B.2: AgentSwitcher Component (New)
- **Depends on:** WI-5B.1
- **Port from:** New
- **Deliverables:**
  - `web/src/components/layout/AgentSwitcher.tsx` — sidebar dropdown for agent selection
- **Acceptance criteria:**
  - Renders in the sidebar (below tenant indicator, above thread list)
  - Shows active agent name + avatar, dropdown lists all available agents
  - Selecting a different agent calls `setActiveAgent()` and reloads threads for that agent
  - Shows agent capability badges (chat, code, voice, proactive)
  - Keyboard navigable (arrow keys, enter to select)
- **Estimated effort:** S

#### WI-5B.3: Chat Store (Port with Modifications)
- **Depends on:** WI-5B.1
- **Port from:** `../mypalclara/` branch `feat/web-ui-rebuild`, `web-ui/frontend/src/stores/chatStore.ts` (port with modifications)
- **Deliverables:**
  - `web/src/stores/chatStore.ts` — Zustand chat store with agent scoping
- **Acceptance criteria:**
  - Store adds `agentId: string` to state
  - `threads` are filtered by active `agentId` (each thread belongs to one agent)
  - `sendMessage()` includes `agent_id` in the outgoing message payload
  - Switching agents via `agentStore.setActiveAgent()` triggers thread list reload for the new agent
  - Message streaming (text_delta, tool_call, done) works per-agent
  - Branch operations (create, switch, merge) are scoped to the active agent's threads
- **Estimated effort:** M

#### WI-5B.4: Chat Runtime Provider (Port with Modifications)
- **Depends on:** WI-5B.3
- **Port from:** `../mypalclara/` branch `feat/web-ui-rebuild`, `web-ui/frontend/src/stores/chatRuntime.ts` and `web-ui/frontend/src/components/chat/ChatRuntimeProvider.tsx`
- **Deliverables:**
  - `web/src/stores/chatRuntime.ts` — assistant-ui runtime bridge with agent context
  - `web/src/components/chat/ChatRuntimeProvider.tsx` — provider component
- **Acceptance criteria:**
  - Runtime bridge converts assistant-ui messages to MyPal wire format, including `agent_id`
  - `ChatRuntimeProvider` wraps chat pages and provides runtime to assistant-ui components
  - Streaming works: user sends message, sees typing indicator, receives chunked response
  - Tool calls display inline via `ToolCallBlock` during streaming
  - Agent switching re-initializes the runtime with the new agent's context
- **Estimated effort:** M

#### WI-5B.5: WebSocket Hook (Copy As-Is)
- **Depends on:** WI-5A.4 (Clerk auth), WI-4A.2 (WebSocket endpoint)
- **Port from:** `../mypalclara/` branch `feat/web-ui-rebuild`, `web-ui/frontend/src/hooks/useGatewayWebSocket.ts` (copy as-is per arch doc)
- **Deliverables:**
  - `web/src/hooks/useGatewayWebSocket.ts` — WebSocket transport hook
- **Acceptance criteria:**
  - Hook connects to `WS /api/v1/chat/stream` with Clerk JWT
  - Exponential backoff reconnect on disconnect (1s, 2s, 4s, 8s, max 30s)
  - Heartbeat ping every 30s, reconnect on missed pong
  - Session recovery: on reconnect, resumes from last received message ID
  - Clerk's `getToken()` provides fresh JWT on each connection attempt
- **Estimated effort:** S

#### WI-5B.6: Branch Sidebar (Port with Modifications)
- **Depends on:** WI-5B.3
- **Port from:** `../mypalclara/` branch `feat/web-ui-rebuild`, `web-ui/frontend/src/components/chat/BranchSidebar.tsx` and `web-ui/frontend/src/hooks/useBranches.ts`
- **Deliverables:**
  - `web/src/components/chat/BranchSidebar.tsx` — conversation branching UI with agent awareness
  - `web/src/hooks/useBranches.ts` — branching hook with agent_id scoping
- **Acceptance criteria:**
  - Branch list shows only branches for the active agent
  - Creating a new branch associates it with the current `agent_id`
  - Merge dialog works between branches of the same agent
  - Branch metadata shows agent name + avatar
- **Estimated effort:** S

#### WI-5B.7: Chat Components (Copy As-Is)
- **Depends on:** WI-5B.4
- **Port from:** `../mypalclara/` branch `feat/web-ui-rebuild` (copy as-is)
- **Deliverables:**
  - `web/src/components/chat/TierSelector.tsx` — model tier selection (high/mid/low)
  - `web/src/components/chat/ToolCallBlock.tsx` — tool execution display
  - `web/src/components/chat/ArtifactPanel.tsx` — file/artifact viewer
  - `web/src/components/chat/MergeDialog.tsx` — branch merge confirmation dialog
- **Acceptance criteria:**
  - TierSelector shows high/mid/low options, selected tier is sent with each message
  - ToolCallBlock renders tool name, arguments (collapsible JSON), result, and duration
  - ArtifactPanel displays code files, images, and downloadable attachments
  - MergeDialog shows branch diff preview and confirms merge
  - All components render without console errors
- **Estimated effort:** S

#### WI-5B.8: Sidebar Evolution (Port with Modifications)
- **Depends on:** WI-5B.1, WI-5B.2
- **Port from:** `../mypalclara/` branch `feat/web-ui-rebuild`, `web-ui/frontend/src/components/layout/UnifiedSidebar.tsx` and `AppLayout.tsx`
- **Deliverables:**
  - `web/src/components/layout/UnifiedSidebar.tsx` — sidebar with agent list, tenant indicator, thread list
  - `web/src/components/layout/AppLayout.tsx` — main layout with sidebar + content area
- **Acceptance criteria:**
  - Sidebar shows: tenant name/indicator at top, AgentSwitcher below, thread list for active agent, nav links at bottom
  - Thread list groups threads by date (today, yesterday, this week, older) via `lib/threadGroups.ts`
  - Sidebar is collapsible on mobile (sheet overlay) and desktop (toggle button)
  - Active thread is highlighted; clicking a thread loads it in the main area
  - Nav links: Chat, Knowledge Base, Settings, Permissions (admin only)
- **Estimated effort:** M

### Stage 5C: Knowledge Base Evolution (Week 21)

Evolve the knowledge base UI from opaque mem0 vectors to rich Rook v2 memories with scopes, FSRS dynamics, and agent attribution.

#### WI-5C.1: Memories Hook (Port with Modifications)
- **Depends on:** WI-5A.5 (API client with Rook v2 memory types)
- **Port from:** `../mypalclara/` branch `feat/web-ui-rebuild`, `web-ui/frontend/src/hooks/useMemories.ts`
- **Deliverables:**
  - `web/src/hooks/useMemories.ts` — Rook v2 memory queries with scope and agent filters
- **Acceptance criteria:**
  - `useMemories({ scope, agentId, category, query, page })` fetches from `GET /api/v1/memory`
  - Supports filtering by scope (system, tenant, agent, user, user_agent, session)
  - Supports filtering by agent_id (show memories from a specific agent)
  - Supports filtering by category (fact, preference, observation, relationship, skill, etc.)
  - Text search queries use backend vector search
  - Returns `{ memories, total, isLoading, error, refetch }`
  - Pagination: page-based with configurable page size
- **Estimated effort:** M

#### WI-5C.2: Memory Card (Port with Modifications)
- **Depends on:** WI-5C.1
- **Port from:** `../mypalclara/` branch `feat/web-ui-rebuild`, `web-ui/frontend/src/components/knowledge/MemoryCard.tsx`
- **Deliverables:**
  - `web/src/components/knowledge/MemoryCard.tsx` — memory display card with Rook v2 fields
- **Acceptance criteria:**
  - Card displays: content text, category badge, scope badge (color-coded: system=purple, tenant=blue, agent=green, user=yellow, user_agent=orange)
  - Agent attribution badge shows which agent created/owns this memory
  - FSRS retrievability gauge: circular progress indicator (0-100%) showing current recall probability
  - Stability indicator: "fading" (< 0.3 retrievability), "stable" (0.3-0.7), "strong" (> 0.7)
  - Importance stars or bar (0.0-1.0)
  - Confidence indicator (0.0-1.0)
  - Supersession: if memory supersedes another, shows "Updated from: [link to original]"
  - Created/updated timestamps
  - Click opens MemoryEditor
- **Estimated effort:** M

#### WI-5C.3: Memory Editor (Port with Modifications)
- **Depends on:** WI-5C.2
- **Port from:** `../mypalclara/` branch `feat/web-ui-rebuild`, `web-ui/frontend/src/components/knowledge/MemoryEditor.tsx`
- **Deliverables:**
  - `web/src/components/knowledge/MemoryEditor.tsx` — memory edit dialog with Rook v2 fields
- **Acceptance criteria:**
  - Editable fields: content (text area), category (dropdown), importance (slider), confidence (slider)
  - Scope selector: dropdown showing available scopes (restricted by user role — guests cannot create TENANT or SYSTEM memories)
  - Supersession chain display: shows the history of this memory (original -> superseded by -> current), clickable links
  - FSRS state display (read-only): stability, difficulty, last_review, next_review, retrievability
  - Delete button with confirmation (soft-delete)
  - Save calls `PATCH /api/v1/memory/{id}` with changed fields
  - Validation: content required, importance/confidence must be 0.0-1.0
- **Estimated effort:** M

#### WI-5C.4: Knowledge Base Page (Port with Modifications)
- **Depends on:** WI-5C.1, WI-5C.2, WI-5C.3
- **Port from:** `../mypalclara/` branch `feat/web-ui-rebuild`, `web-ui/frontend/src/pages/KnowledgeBase.tsx`
- **Deliverables:**
  - `web/src/pages/KnowledgeBase.tsx` — full knowledge base page with scope/agent filters
  - `web/src/components/knowledge/MemoryGrid.tsx` — grid layout (copy as-is)
  - `web/src/components/knowledge/MemoryList.tsx` — list layout (copy as-is)
  - `web/src/components/knowledge/SearchBar.tsx` — search with filters (copy as-is, verified compatible)
- **Acceptance criteria:**
  - Page shows: search bar, filter chips (scope, category, agent), grid/list toggle, saved filter sets
  - Scope filter: dropdown with all 6 scopes + "all"
  - Agent filter: dropdown with all tenant agents + "all"
  - Category filter: multi-select chips for memory categories
  - Saved filter sets: save and restore named filter combinations (uses `savedSets` store)
  - Grid view: MemoryCard tiles in responsive grid
  - List view: compact table with sortable columns (content, scope, agent, retrievability, created)
  - Export: selected memories exportable as JSON or Markdown
  - Import: bulk import via JSON file upload (routed through Rook v2 ingestion pipeline)
  - Empty state: "No memories found" with hint to adjust filters
- **Estimated effort:** L

#### WI-5C.5: Permission-Aware Memory UI
- **Depends on:** WI-5C.4, Phase 2 (RBAC)
- **Port from:** New
- **Deliverables:**
  - `web/src/hooks/useMemories.ts` — permission-aware filtering
  - `web/src/components/knowledge/MemoryCard.tsx` — lock icon for memories the user can view but not edit
- **Acceptance criteria:**
  - Guest users see only USER and USER_AGENT scoped memories for themselves
  - Members see USER, USER_AGENT, and AGENT scoped memories
  - Admins see all scopes including TENANT and SYSTEM
  - Read-only memories show a lock icon; clicking edit shows "You don't have permission to edit this memory"
  - Scope filter only shows scopes the user has access to
- **Estimated effort:** S

### Stage 5D: Admin & Settings (Week 22)

Settings, permissions, onboarding wizard, and admin features.

#### WI-5D.1: Settings Page (Port with Modifications)
- **Depends on:** WI-5B.1 (agent store), WI-5A.5 (API client)
- **Port from:** `../mypalclara/` branch `feat/web-ui-rebuild`, `web-ui/frontend/src/pages/Settings.tsx`
- **Deliverables:**
  - `web/src/pages/Settings.tsx` — expanded settings with tenant and agent management sections
- **Acceptance criteria:**
  - Tabs: Profile, Agents, Tenant, Adapters, API Keys
  - Profile tab: display name, email (from Clerk), linked platforms (from PlatformLink)
  - Agents tab (admin+): list agents, create/edit/delete agent definitions, configure persona and LLM
  - Tenant tab (owner only): tenant name, plan tier, pairing mode, feature flags
  - Adapters tab: shows connected adapters with status (uses AdapterLinking)
  - API Keys tab: list, create, revoke API keys (from WI-4F.1)
  - Dynamic form rendering: fields show/hide based on enabled capabilities (e.g., voice settings only visible if VOICE capability enabled)
- **Estimated effort:** L

#### WI-5D.2: Adapter Linking (Port with Modifications)
- **Depends on:** WI-5D.1
- **Port from:** `../mypalclara/` branch `feat/web-ui-rebuild`, `web-ui/frontend/src/components/settings/AdapterLinking.tsx`
- **Deliverables:**
  - `web/src/components/settings/AdapterLinking.tsx` — platform linking with pairing modes
- **Acceptance criteria:**
  - Shows linked platforms: Discord (server ID, bot status), Telegram (bot name), Slack (workspace), API (active keys)
  - Connect button per platform with guided setup flow (Discord: bot token + server ID, Telegram: BotFather token, Slack: app manifest)
  - Pairing mode selector (owner only): Open, Approval, Invite-Only, Closed with explanations
  - For Invite-Only mode: generate/revoke pairing codes, show active codes
  - For Approval mode: show pending approval requests with approve/deny buttons
  - Disconnect button with confirmation
- **Estimated effort:** M

#### WI-5D.3: Permissions Page (New)
- **Depends on:** Phase 2 (tool permission API), WI-5A.5 (API client)
- **Port from:** New (arch doc section 8.3.7)
- **Deliverables:**
  - `web/src/pages/Permissions.tsx` — tool permission matrix UI
- **Acceptance criteria:**
  - Matrix view: rows = users, columns = tools, cells = Allow/Deny/Ask toggle
  - Column headers show tool name + description tooltip
  - Row headers show user display name + role badge
  - Bulk operations: "Set all to Allow" / "Set all to Deny" per user or per tool
  - Role defaults: show inherited permission from role in gray, overrides in color
  - Filter: search users, filter tools by category (builtin, MCP, agent-specific)
  - Changes save immediately (optimistic update with rollback on error)
  - Only Admin+ can access this page (Member/Guest see 403 redirect)
- **Estimated effort:** L

#### WI-5D.4: Onboarding Wizard (New)
- **Depends on:** WI-5D.1, WI-5A.5, Phase 6A (tenant management API)
- **Port from:** New (arch doc section 12.3)
- **Deliverables:**
  - `web/src/pages/Onboarding.tsx` — wizard page wrapper
  - `web/src/components/admin/OnboardingWizard.tsx` — 6-step wizard component
- **Acceptance criteria:**
  - **Step 1 (Your Space):** tenant name, admin display name, email (from Clerk). Creates tenant via API
  - **Step 2 (Your First Agent):** agent name, persona preset dropdown (Friendly Assistant, Code Reviewer, Research Helper, Custom), optional avatar upload. Creates agent via API
  - **Step 3 (AI Provider):** provider selector (Anthropic, OpenRouter, OpenAI, Custom OpenAI-compatible), API key input (masked), model selector, "Test Connection" button that calls the LLM with a hello message
  - **Step 4 (Connect Channel):** platform selector with per-platform guided setup. Discord: bot token + server ID with instructions. Telegram: BotFather walkthrough. API: "skip for now, use web chat"
  - **Step 5 (Capabilities):** toggle switches for: Memory (on by default), Web Search, Code Execution, File Handling, MCP Servers, Scheduled Tasks. Each toggle shows brief explanation
  - **Step 6 (Ready!):** summary card showing tenant name, agent name, provider, connected channels. Buttons: "Open Chat", "Invite Users", "Go to Dashboard"
  - Progress is saved to the backend (resumable if browser closes)
  - Steps 2-5 are skippable with sensible defaults (default agent "Assistant", no provider = demo mode, no channel = web-only, all capabilities off except Memory)
  - Optional import step appears between Steps 1 and 2 for users with a MyPalClara mem0 backup
- **Estimated effort:** L

#### WI-5D.5: App Routing (Port with Modifications)
- **Depends on:** WI-5D.1, WI-5D.3, WI-5D.4
- **Port from:** `../mypalclara/` branch `feat/web-ui-rebuild`, `web-ui/frontend/src/App.tsx`
- **Deliverables:**
  - `web/src/App.tsx` — router with auth guard and new routes
- **Acceptance criteria:**
  - Routes: `/` (redirect to `/chat`), `/chat` (Chat page), `/chat/:threadId`, `/knowledge` (KnowledgeBase), `/settings` (Settings), `/permissions` (Permissions, admin+), `/onboarding` (Onboarding wizard), `/login` (Clerk sign-in)
  - Auth guard: unauthenticated users redirect to `/login`
  - Role guard: `/permissions` returns 403 for non-admin users
  - New tenants (no tenant_id in user context) redirect to `/onboarding`
  - Clerk `<SignIn>` and `<UserButton>` components integrate with the layout
- **Estimated effort:** S

---

## Phase 6: Multi-Tenancy & Production (Weeks 23-26)

Phase 6 hardens MyPal for real-world use: full tenant lifecycle, RBAC enforcement across every endpoint, rate limiting, data portability, and production infrastructure.

### Stage 6A: Tenant Management API + Onboarding Backend

#### WI-6A.1: Tenant CRUD API
- **Depends on:** Phase 1 (tenant model), WI-5D.4 (onboarding wizard consumes this)
- **Port from:** New
- **Deliverables:**
  - `mypal/api/v1/tenants.py` — full tenant management endpoints
  - `mypal/tenants/service.py` — tenant service layer (create, update, delete, get, list)
- **Acceptance criteria:**
  - `POST /api/v1/tenants` creates a tenant + admin user + default agent in a single transaction
  - `GET /api/v1/tenants/current` returns the tenant for the authenticated user
  - `PATCH /api/v1/tenants/{id}` updates tenant settings (owner only)
  - `DELETE /api/v1/tenants/{id}` soft-deletes with 30-day grace period (owner only, requires confirmation token)
  - Tenant settings include: `name`, `slug`, `plan_tier`, `pairing_mode`, `feature_flags`, `limits`
  - Feature flags: `memory_enabled`, `web_search_enabled`, `code_execution_enabled`, `mcp_enabled`, `scheduled_tasks_enabled`, `voice_enabled`
  - Creating a tenant auto-creates a system user for loopback operations
- **Estimated effort:** M

#### WI-6A.2: Onboarding Wizard Backend
- **Depends on:** WI-6A.1
- **Port from:** New (arch doc section 12.3)
- **Deliverables:**
  - `mypal/api/v1/onboarding.py` — wizard state tracking endpoints
  - `mypal/db/models.py` — `OnboardingState` model (tenant_id, current_step, step_data JSON, completed_at)
- **Acceptance criteria:**
  - `POST /api/v1/onboarding/start` creates a new onboarding session
  - `PATCH /api/v1/onboarding/step/{step_number}` saves step data (resumable)
  - `GET /api/v1/onboarding/state` returns current step + all saved step data
  - `POST /api/v1/onboarding/complete` marks onboarding as finished
  - Step 3 "Test Connection" calls `POST /api/v1/onboarding/test-provider` which makes a real LLM call and returns success/error
  - Step 4 channel setup creates the adapter configuration in the DB
  - Step 5 capabilities update the tenant feature flags
  - Incomplete onboarding is resumable after browser close / re-login
- **Estimated effort:** M

#### WI-6A.3: Tenant User Management
- **Depends on:** WI-6A.1
- **Port from:** New
- **Deliverables:**
  - `mypal/api/v1/users.py` — user management within a tenant
- **Acceptance criteria:**
  - `GET /api/v1/tenants/{id}/users` lists users for a tenant (admin+)
  - `PATCH /api/v1/users/{id}/role` changes user role (admin+ only, cannot demote last owner)
  - `DELETE /api/v1/users/{id}` removes user from tenant (admin+ only, cannot remove owner)
  - `POST /api/v1/tenants/{id}/invite` generates an invite link/code (for invite-only pairing mode)
  - User list includes: display_name, role, linked_platforms, last_active, created_at
- **Estimated effort:** M

### Stage 6B: Full RBAC Enforcement

#### WI-6B.1: RBAC Middleware
- **Depends on:** Phase 1 (RBAC model), WI-6A.1
- **Port from:** New (arch doc section 8.1)
- **Deliverables:**
  - `mypal/auth/rbac.py` — RBAC enforcement middleware and decorators
  - `mypal/api/v1/deps.py` — FastAPI dependency injection for role checks
- **Acceptance criteria:**
  - Every API endpoint has an explicit role requirement (documented in OpenAPI schema)
  - Role hierarchy: Owner > Admin > Member > Guest
  - `require_role(TenantRole.ADMIN)` FastAPI dependency rejects requests from Member/Guest with 403
  - Role check uses the CanonicalUser's role within the active tenant
  - Endpoints organized by minimum role:
    - Guest: `GET /chat`, `GET /memory` (own scope), `GET /agents`
    - Member: `POST /chat`, `POST /memory`, `GET /threads`, `POST /tasks` (own)
    - Admin: user management, agent CRUD, permission matrix, webhook management, task management (all)
    - Owner: tenant settings, tenant delete, role changes, billing
  - Audit log: role-restricted actions (role change, user remove, tenant settings) are logged with actor, action, timestamp
- **Estimated effort:** L

#### WI-6B.2: Resource-Level Authorization
- **Depends on:** WI-6B.1
- **Port from:** New
- **Deliverables:**
  - `mypal/auth/rbac.py` — resource-level checks (beyond role checks)
- **Acceptance criteria:**
  - Users can only read/write their own USER and USER_AGENT scoped memories
  - Users can only read/write their own threads (unless admin)
  - Users can only view/manage their own API keys (unless admin)
  - Users can only view/modify their own scheduled tasks (unless admin)
  - Tenant isolation: no endpoint returns data from a different tenant, regardless of role
  - Test: authenticated user A cannot access user B's threads, memories, or tasks via direct ID guessing
- **Estimated effort:** M

### Stage 6C: Rate Limiting, Quotas, Usage Metering

#### WI-6C.1: Rate Limiting
- **Depends on:** WI-6B.1 (RBAC — rate limits vary by role)
- **Port from:** New
- **Deliverables:**
  - `mypal/auth/rate_limit.py` — rate limiting middleware (Redis-backed sliding window)
  - `mypal/api/v1/deps.py` — rate limit dependency
- **Acceptance criteria:**
  - Rate limits configurable per-tenant in `TenantSettings`:
    - `messages_per_minute` (default: 20)
    - `messages_per_hour` (default: 200)
    - `api_requests_per_minute` (default: 60)
  - Rate-limited responses return 429 with `Retry-After` header
  - Redis sliding window counter (falls back to in-memory if Redis unavailable)
  - Admin/Owner users have 2x rate limits
  - Rate limit headers on every response: `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset`
- **Estimated effort:** M

#### WI-6C.2: Usage Quotas per Plan Tier
- **Depends on:** WI-6C.1
- **Port from:** New
- **Deliverables:**
  - `mypal/tenants/quotas.py` — quota tracking and enforcement
  - `mypal/db/models.py` — `UsageRecord` model (tenant_id, period, metric, value)
- **Acceptance criteria:**
  - Plan tiers with limits:
    - FREE: 1,000 messages/month, 1 agent, 5,000 memories, 10 scheduled tasks
    - PRO: 10,000 messages/month, 10 agents, 100,000 memories, 100 scheduled tasks
    - ENTERPRISE: unlimited (configurable)
  - Quota check runs before each message send (fast: cached in Redis, refreshed hourly)
  - Exceeding quota returns 402 with `{"error": "quota_exceeded", "limit": "messages_per_month", "current": 1001, "max": 1000, "upgrade_url": "..."}`
  - `GET /api/v1/tenants/current/usage` returns current period usage for all metrics
  - Usage counters reset on the 1st of each month (UTC)
- **Estimated effort:** M

#### WI-6C.3: LLM Token Metering
- **Depends on:** WI-6C.2
- **Port from:** New
- **Deliverables:**
  - `mypal/llm/metering.py` — token counting wrapper around LLM provider calls
  - `mypal/db/models.py` — `TokenUsage` model (tenant_id, agent_id, user_id, model, input_tokens, output_tokens, cost_estimate, timestamp)
- **Acceptance criteria:**
  - Every LLM call records input_tokens, output_tokens, model, and estimated cost
  - Cost estimation uses a configurable price table ($/1K tokens per model)
  - `GET /api/v1/tenants/current/usage/tokens` returns token usage breakdown by agent, user, model, and time period
  - Daily/weekly/monthly aggregation for dashboard display
  - Token metering does not add measurable latency to LLM calls (async write)
- **Estimated effort:** M

### Stage 6D: Data Export/Import + mem0 Migration

#### WI-6D.1: Tenant Data Export
- **Depends on:** WI-6B.1 (RBAC — owner only)
- **Port from:** New
- **Deliverables:**
  - `mypal/api/v1/admin.py` — export endpoint
  - `mypal/services/export.py` — export service
- **Acceptance criteria:**
  - `POST /api/v1/tenants/current/export` queues an export job (returns job ID)
  - `GET /api/v1/tenants/current/export/{job_id}` returns status and download URL when ready
  - Export includes: tenant settings, agent definitions, all memories (JSON), conversation history, scheduled tasks, webhook registrations, user list (no passwords/tokens)
  - Export format: ZIP containing JSON files organized by entity type
  - Export is tenant-scoped (cannot leak other tenants' data)
  - Large exports run as background tasks (arq job queue)
  - Owner-only access
- **Estimated effort:** M

#### WI-6D.2: Tenant Data Import
- **Depends on:** WI-6D.1
- **Port from:** New
- **Deliverables:**
  - `mypal/api/v1/admin.py` — import endpoint
  - `mypal/services/import_service.py` — import service with validation
- **Acceptance criteria:**
  - `POST /api/v1/tenants/current/import` accepts a ZIP file from a previous export
  - Import runs as a background job with progress tracking
  - Dry-run mode: `?dry_run=true` validates the import without writing data, returns summary of what would be imported
  - Conflict resolution: skip duplicates (by content hash for memories, by name for agents/tasks)
  - Memories imported via Rook v2 ingestion pipeline (dedup + normalize)
  - Import report: counts of imported/skipped/errored items
  - Owner-only access
- **Estimated effort:** L

#### WI-6D.3: mem0 Migration Tool
- **Depends on:** WI-6D.2, Rook v2 ingestion pipeline (Phase 2)
- **Port from:** New (arch doc section 5.12)
- **Deliverables:**
  - `mypal/services/mem0_migration.py` — mem0 to Rook v2 converter
  - `mypal/api/v1/admin.py` — migration endpoints
- **Acceptance criteria:**
  - `POST /api/v1/admin/migrate/mem0` accepts a mem0 export file (JSON format from MyPalClara's export tool)
  - Preview mode: shows a table of memories to be imported with category guesses and dedup matches
  - User can select/deselect individual memories before confirming import
  - Imported memories get:
    - `scope = USER_AGENT` (default, since mem0 memories were per-user)
    - `source = INGESTION`
    - `category` = best-guess from content analysis (FACT, PREFERENCE, etc.)
    - `importance` and `confidence` = 0.5 (neutral, user can adjust)
    - FSRS state = NEW (fresh start for dynamics)
  - Dedup: memories with > 0.92 cosine similarity to existing memories are flagged (not auto-skipped, user decides)
  - Migration is resumable (tracks which memories have been processed)
- **Estimated effort:** L

### Stage 6E: Ops — Docker Compose, Monitoring, Backups, CI/CD

#### WI-6E.1: Docker Compose Production Stack
- **Depends on:** All prior phases
- **Port from:** `../mypalclara/docker-compose.yml` (370 lines — PostgreSQL, Redis, backup service, gateway, adapter containers)
- **Deliverables:**
  - `docker-compose.yml` — production stack for MyPal
  - `docker-compose.dev.yml` — development overrides (hot reload, debug ports)
  - `Dockerfile` — FastAPI backend
  - `web/Dockerfile` — frontend build + static serve
- **Acceptance criteria:**
  - Services: `postgres` (16, with pgvector extension), `redis` (7-alpine), `mypal-api` (FastAPI backend), `mypal-web` (frontend, optional — can be served from API container), `mypal-backup` (scheduled backups)
  - Qdrant removed (MyPal uses pgvector, not separate vector DB)
  - FalkorDB optional (only if graph memory enabled)
  - `docker compose up` starts all services with health checks
  - `docker compose up -d mypal-api` starts just the API (depends on postgres + redis)
  - Health checks: postgres (`pg_isready`), redis (`redis-cli ping`), API (`/health`), backup (`/health`)
  - Environment variables documented in `.env.example`
  - Volumes: `postgres-data`, `redis-data`, `mypal-backups`, `mypal-files`
  - Network: `mypal-network` for inter-service communication
  - All services use `restart: unless-stopped`
  - Dev overrides: mount source code, enable hot reload, expose debug ports
- **Estimated effort:** L

#### WI-6E.2: Backup Service (Port + Evolve)
- **Depends on:** WI-6E.1
- **Port from:** `../mypalclara/mypalclara/services/backup/` (1,506 lines — config.py, database.py, cli.py, cron.py, health.py, storage/local.py, storage/s3.py)
- **Deliverables:**
  - `mypal/services/backup/config.py` — backup configuration (from BackupConfig, add tenant awareness)
  - `mypal/services/backup/database.py` — pg_dump/pg_restore wrappers (port as-is, update DB URL references)
  - `mypal/services/backup/storage/local.py` — local filesystem storage (port as-is)
  - `mypal/services/backup/storage/s3.py` — S3/Wasabi storage (port as-is)
  - `mypal/services/backup/cli.py` — CLI for manual backup/restore (port, simplify from 3 DBs to 1)
  - `mypal/services/backup/cron.py` — cron-based scheduling (port as-is)
  - `mypal/services/backup/health.py` — health endpoint (port as-is)
  - `mypal/services/backup/Dockerfile` — backup service container
- **Acceptance criteria:**
  - `python -m mypal.services.backup dump` creates a compressed pg_dump of the MyPal database
  - `python -m mypal.services.backup restore <file>` restores from backup
  - `python -m mypal.services.backup list` shows available backups (local or S3)
  - Cron mode: runs on `BACKUP_CRON_SCHEDULE` (default `0 3 * * *` — 3 AM daily)
  - Retention: auto-delete backups older than `BACKUP_RETENTION_DAYS` (default 7)
  - Storage: local filesystem or S3-compatible (Wasabi, AWS, MinIO) based on config
  - Health endpoint at `/health` for Docker health checks
  - Simplification from original: single database (MyPal consolidates to one PostgreSQL), remove rook_db_url and config_files backup (config is in DB now)
  - Backup size logged on completion
- **Estimated effort:** M

#### WI-6E.3: Monitoring + Structured Logging
- **Depends on:** WI-6E.1
- **Port from:** New
- **Deliverables:**
  - `mypal/observability/logging.py` — structured JSON logging configuration
  - `mypal/observability/metrics.py` — Prometheus metrics (optional, via `prometheus-fastapi-instrumentator`)
  - `mypal/api/v1/admin.py` — `GET /api/v1/admin/health` and `GET /api/v1/admin/metrics` endpoints
- **Acceptance criteria:**
  - All log output is structured JSON: `{"timestamp", "level", "logger", "message", "tenant_id", "user_id", "agent_id", "request_id"}`
  - Request ID is generated per-request and propagated through all log entries
  - `GET /health` returns: database connectivity, Redis connectivity, active connections, uptime
  - `GET /api/v1/admin/metrics` (admin only) returns: messages processed (total, per-agent, per-tenant), LLM calls (count, avg latency, token usage), memory operations, active WebSocket connections, scheduler task stats
  - Log level configurable via `LOG_LEVEL` env var
  - Correlation: all log entries for a single request share a request_id
- **Estimated effort:** M

#### WI-6E.4: CI/CD Pipeline
- **Depends on:** WI-6E.1
- **Port from:** New
- **Deliverables:**
  - `.github/workflows/ci.yml` — CI pipeline
  - `.github/workflows/deploy.yml` — deployment pipeline (optional, depends on hosting)
- **Acceptance criteria:**
  - CI runs on every PR to `main`:
    - Backend: `ruff check`, `ruff format --check`, `mypy`, `pytest` (unit + integration with test DB)
    - Frontend: `npm run lint`, `npm run typecheck`, `npm run build`
  - Integration tests use a disposable PostgreSQL (via `testcontainers` or GitHub Actions service container)
  - Test coverage reported (minimum threshold: 70% backend, 50% frontend)
  - Build artifacts: Docker images tagged with git SHA and branch name
  - Deploy workflow (optional): push to container registry on merge to `main`
  - Pipeline completes in under 10 minutes
- **Estimated effort:** M

#### WI-6E.5: Environment + Configuration Documentation
- **Depends on:** WI-6E.1
- **Port from:** New
- **Deliverables:**
  - `.env.example` — documented environment variable template
- **Acceptance criteria:**
  - Every environment variable used by the application is listed with description, type, default value, and whether required
  - Grouped by concern: Database, Redis, Auth (Clerk), LLM Providers, Backup, Features
  - Comments explain non-obvious values (e.g., `CLERK_PUBLISHABLE_KEY` vs `CLERK_SECRET_KEY`)
  - `docker compose up` with only `.env.example` renamed to `.env` and required keys filled in starts successfully
- **Estimated effort:** S

---

## Summary

| Phase | Stage | Work Items | Estimated Weeks |
|-------|-------|------------|-----------------|
| **4: Multi-Input** | 4A: API Gateway | 4 | 15-16 |
| | 4B: Loopback Dispatcher | 2 | 16 |
| | 4C: Scheduler | 3 | 16-17 |
| | 4D: Webhooks | 2 | 17 |
| | 4E: Modality Processors | 3 | 17-18 |
| | 4F: API Keys + Format | 2 | 18 |
| **5: Web UI** | 5A: Foundation Port | 7 | 19 |
| | 5B: Chat Evolution | 8 | 20 |
| | 5C: Knowledge Base | 5 | 21 |
| | 5D: Admin & Settings | 5 | 22 |
| **6: Production** | 6A: Tenant Management | 3 | 23 |
| | 6B: RBAC Enforcement | 2 | 23-24 |
| | 6C: Rate Limits + Quotas | 3 | 24-25 |
| | 6D: Data Export/Import | 3 | 25 |
| | 6E: Ops | 5 | 25-26 |

**Total: 57 work items across 15 stages in 3 phases (12 weeks).**

### Key Dependencies Across Phases

- Phase 5 frontend depends on Phase 4A API endpoints existing
- Phase 5D onboarding wizard depends on Phase 6A tenant management API (can stub initially)
- Phase 6B RBAC depends on all endpoints from Phases 4-5 being identified
- Phase 6D mem0 migration depends on Phase 2 Rook v2 ingestion pipeline
- Phase 6E Docker Compose depends on all services being containerizable

### Files Ported from MyPalClara

| Source | Destination | Lines | Action |
|--------|-------------|-------|--------|
| `gateway/scheduler.py` | `mypal/services/scheduler.py` | 729 | Port + add tenant scoping, DB persistence, loopback integration |
| `services/email/` | `mypal/inputs/events.py` (email source) | ~1,998 | Port monitor.py + providers as email event source (P2) |
| `services/backup/` | `mypal/services/backup/` | ~1,506 | Port + simplify (1 DB instead of 3, remove config file backup) |
| `docker-compose.yml` | `docker-compose.yml` | 374 | Port + restructure (remove Qdrant, add mypal-api, mypal-web) |
| `web-ui/frontend/` (feat/web-ui-rebuild) | `web/src/` | ~30 files | Mix of copy-as-is and port-with-modifications per section 12.1 |
