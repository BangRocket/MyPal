# Phase 1: Foundation

**Scope:** Project bootstrap through first end-to-end chat response
**Duration:** ~6 weeks
**Stages:** 8 (1A-1H), 34 work items
**Constraints:**
- Postgres + Redis run in Docker Compose; MyPal app runs locally (bare metal)
- Phase 1 LLM providers: Anthropic + OpenAI only (no OpenRouter, Bedrock, Azure, NanoGPT)
- Test-after, not TDD
- Frontend scaffold is a copy from `feat/web-ui-rebuild` branch of `../mypalclara`

**Architecture reference:** `docs/MyPal_Master_Architecture.md` -- sections referenced inline as (arch SS.N)

---

## Stage 1A: Project Bootstrap

Goal: Runnable FastAPI app with health check, config, and Docker infrastructure.

---

### WI-1.1: pyproject.toml + Project Skeleton
- **Depends on:** None
- **Port from:** New
- **Deliverables:**
  - `pyproject.toml` (project metadata + all dependencies)
  - `mypal/__init__.py`
  - `mypal/py.typed` (PEP 561 marker)
  - `.python-version` (3.12+)
  - `.gitignore`
- **Acceptance criteria:**
  - `uv sync` (or `pip install -e ".[dev]"`) completes without errors
  - `python -c "import mypal"` succeeds
- **Estimated effort:** S

**Dependencies to declare in pyproject.toml:**

```
# Core
fastapi >= 0.115
uvicorn[standard] >= 0.34
pydantic >= 2.10
pydantic-settings >= 2.7
sqlalchemy[asyncio] >= 2.0.36
asyncpg >= 0.30
alembic >= 1.14
redis >= 5.2
pgvector >= 0.3
httpx >= 0.28

# LLM (Phase 1: Anthropic + OpenAI only)
anthropic >= 0.42
openai >= 1.60

# Auth
pyjwt[crypto] >= 2.10
cryptography >= 44.0

# Dev
pytest >= 8.3
pytest-asyncio >= 0.25
ruff >= 0.9
```

---

### WI-1.2: Docker Compose (Postgres + Redis)
- **Depends on:** None
- **Port from:** New
- **Deliverables:**
  - `docker-compose.yml`
  - `scripts/init-db.sql` (enables pgvector extension)
- **Acceptance criteria:**
  - `docker compose up -d` starts Postgres 17 (with pgvector) + Redis 7 containers
  - `psql -h localhost -U mypal -d mypal -c "SELECT 1"` returns 1
  - `redis-cli ping` returns PONG
  - `psql -h localhost -U mypal -d mypal -c "CREATE EXTENSION IF NOT EXISTS vector; SELECT extversion FROM pg_extension WHERE extname = 'vector';"` returns a version
- **Estimated effort:** S

**docker-compose.yml specifics:**
- Postgres image: `pgvector/pgvector:pg17` (includes pgvector pre-installed)
- Redis image: `redis:7-alpine`
- Postgres port: 5432, Redis port: 6379
- Named volumes for data persistence
- `init-db.sql` mounted to `/docker-entrypoint-initdb.d/` for auto-setup
- Environment: `POSTGRES_USER=mypal`, `POSTGRES_PASSWORD=mypal`, `POSTGRES_DB=mypal`

---

### WI-1.3: Config via Pydantic BaseSettings
- **Depends on:** WI-1.1
- **Port from:** New (informed by `../mypalclara/identity/config.py` patterns)
- **Deliverables:**
  - `mypal/config.py`
- **Acceptance criteria:**
  - `from mypal.config import settings` loads config from environment / `.env` file
  - Missing required values (DATABASE_URL) raise a clear validation error at startup, not deep in a request handler
  - `settings.database_url` returns async-compatible URL (postgresql+asyncpg://...)
- **Estimated effort:** S

**Settings fields (Phase 1):**

```python
class Settings(BaseSettings):
    # Database
    database_url: str = "postgresql+asyncpg://mypal:mypal@localhost:5432/mypal"
    database_url_sync: str = "postgresql://mypal:mypal@localhost:5432/mypal"  # For Alembic

    # Redis
    redis_url: str = "redis://localhost:6379/0"

    # Auth
    clerk_publishable_key: str = ""
    clerk_secret_key: str = ""
    clerk_jwks_url: str = "https://api.clerk.com/v1/jwks"  # overridden per-instance
    service_secret: str = ""  # X-Service-Secret for adapter auth

    # LLM
    anthropic_api_key: str = ""
    openai_api_key: str = ""
    default_llm_provider: str = "anthropic"
    default_model_tier: str = "mid"

    # App
    debug: bool = False
    cors_origins: list[str] = ["http://localhost:5173"]  # Vite dev server

    model_config = SettingsConfigDict(env_file=".env", env_file_encoding="utf-8")
```

**Changes from mypalclara identity/config.py:** Replaces bare `os.getenv()` calls with validated Pydantic model. Uses `postgresql+asyncpg://` URL format (identity service used sync `postgresql://`). No more `dotenv.load_dotenv()` -- Pydantic handles `.env` natively.

---

### WI-1.4: FastAPI main.py with Health Check
- **Depends on:** WI-1.1, WI-1.3
- **Port from:** New (informed by `../mypalclara/identity/app.py` for structure)
- **Deliverables:**
  - `mypal/main.py`
- **Acceptance criteria:**
  - `uvicorn mypal.main:app` starts without errors
  - `curl http://localhost:8000/health` returns `200` with body `{"status": "ok", "service": "mypal"}`
  - `curl http://localhost:8000/api/v1/health` also works (API-prefixed route)
  - CORS headers present for configured origins
  - Lifespan handler logs startup/shutdown
  - `/docs` serves Swagger UI
- **Estimated effort:** S

**main.py specifics:**
- FastAPI app with `lifespan` context manager (not deprecated `on_event`)
- Lifespan creates async DB engine + Redis connection pool on startup, disposes on shutdown
- CORS middleware with `settings.cors_origins`
- API router mounted at `/api/v1/`
- No auth middleware yet -- that comes in Stage 1C

**Structural difference from identity service:** identity's `app.py` uses `create_app()` factory. MyPal uses a module-level `app` instance with lifespan, which is the FastAPI convention for uvicorn/gunicorn.

---

## Stage 1B: Database & Models

Goal: Alembic migrations running, all Phase 1 tables created with tenant isolation.

---

### WI-1.5: Alembic Init + Async Engine
- **Depends on:** WI-1.2, WI-1.3, WI-1.4
- **Port from:** New
- **Deliverables:**
  - `alembic.ini`
  - `alembic/env.py` (async-aware, uses `settings.database_url_sync` for Alembic and `settings.database_url` for the app)
  - `alembic/versions/` (empty, ready for migrations)
  - `mypal/db/__init__.py`
  - `mypal/db/session.py` (async engine + sessionmaker)
  - `mypal/db/base.py` (declarative base)
- **Acceptance criteria:**
  - `alembic revision --autogenerate -m "init"` creates a migration file
  - `alembic upgrade head` runs without errors against the Docker Compose Postgres
  - `from mypal.db.session import async_session_factory` returns a working `async_sessionmaker`
  - pgvector extension is enabled (either via init-db.sql or first migration)
- **Estimated effort:** S

**Key difference from mypalclara:** mypalclara's `db/` uses sync SQLAlchemy (`create_engine`, `sessionmaker`). MyPal uses async throughout: `create_async_engine` from `sqlalchemy.ext.asyncio`, `async_sessionmaker`, `AsyncSession`. Alembic's `env.py` needs the sync URL since Alembic's migration runner is sync.

---

### WI-1.6: Tenant Model
- **Depends on:** WI-1.5
- **Port from:** New (schema from arch SS4.1)
- **Deliverables:**
  - `mypal/db/models/tenant.py`
  - Alembic migration for `tenants` table
- **Acceptance criteria:**
  - Migration creates `tenants` table with columns: `id` (UUID PK), `slug` (unique), `name`, `owner_id`, `plan` (enum: free/pro/enterprise), `settings` (JSONB), `pairing_config` (JSONB), `created_at`, `updated_at`
  - `slug` has a unique index
  - Insert + select round-trip works in a test script
- **Estimated effort:** S

**Schema (from arch SS4.1):**

```python
class Tenant(Base):
    __tablename__ = "tenants"

    id: Mapped[uuid.UUID] = mapped_column(Uuid, primary_key=True, default=uuid.uuid4)
    slug: Mapped[str] = mapped_column(String(63), unique=True, index=True)
    name: Mapped[str] = mapped_column(String(255))
    owner_id: Mapped[uuid.UUID | None] = mapped_column(Uuid, ForeignKey("users.id"), nullable=True)
    plan: Mapped[str] = mapped_column(String(20), default="free")  # free, pro, enterprise
    settings: Mapped[dict] = mapped_column(JSONB, default=dict)
    pairing_config: Mapped[dict] = mapped_column(JSONB, default=dict)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now())
    updated_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now(), onupdate=func.now())
```

**Note:** `owner_id` FK to `users.id` is initially nullable because tenants and users have a circular dependency (user has tenant_id, tenant has owner_id). First migration creates both tables with nullable FKs, second migration can add NOT NULL constraints after seed data.

---

### WI-1.7: User / CanonicalUser Model
- **Depends on:** WI-1.5, WI-1.6
- **Port from:** `../mypalclara/identity/db.py` (CanonicalUser class, lines 34-47) and `../mypalclara/mypalclara/db/models.py` (CanonicalUser class, lines 612-632)
- **Deliverables:**
  - `mypal/db/models/user.py`
  - Alembic migration for `users` table
- **Acceptance criteria:**
  - Migration creates `users` table with columns: `id` (UUID PK), `tenant_id` (FK to tenants), `display_name`, `primary_email` (unique, nullable), `avatar_url`, `role` (enum: owner/admin/member/guest), `status` (active/suspended/pending), `preferences` (JSONB), `created_at`, `updated_at`
  - Composite index on `(tenant_id, primary_email)` exists
  - Insert + select round-trip works
- **Estimated effort:** S

**Changes when porting from identity service CanonicalUser:**
- **Add** `tenant_id` (UUID FK to `tenants.id`, NOT NULL) -- identity service had no multi-tenancy
- **Add** `role` column (String, enum: owner/admin/member/guest) -- identity service used `is_admin` boolean
- **Add** `preferences` (JSONB) -- for default agent, notification settings
- **Remove** `is_admin` -- replaced by `role`
- **Keep** `display_name`, `primary_email`, `avatar_url`, `status`, timestamps
- **Change** PK type from String to UUID (identity service used `str` PKs with `gen_uuid()`)
- **Change** ORM style from SQLAlchemy 1.x `Column()` to 2.0 `Mapped[]` / `mapped_column()`
- **Remove** `web_sessions` relationship (Clerk handles sessions)
- **Remove** `oauth_tokens` relationship from user model (still exists as separate model, linked via PlatformLink's canonical_user_id)

---

### WI-1.8: PlatformLink Model
- **Depends on:** WI-1.5, WI-1.7
- **Port from:** `../mypalclara/identity/db.py` (PlatformLink class, lines 50-64) and `../mypalclara/mypalclara/db/models.py` (PlatformLink class, lines 635-655)
- **Deliverables:**
  - `mypal/db/models/platform_link.py`
  - Alembic migration for `platform_links` table
- **Acceptance criteria:**
  - Migration creates `platform_links` table with columns: `id` (UUID PK), `canonical_user_id` (FK to users), `platform` (String), `platform_user_id` (String), `display_name`, `linked_via` (oauth/auto/manual/clerk), `linked_at`
  - Unique composite index on `(platform, platform_user_id)` exists
  - Round-trip: create user, create link with platform="clerk", query back by platform + platform_user_id
- **Estimated effort:** S

**Changes when porting:**
- **Remove** `prefixed_user_id` column -- MyPal uses the `(platform, platform_user_id)` composite key directly instead of the `discord-123` string format
- **Add** `linked_via` value "clerk" to supported values
- **Change** PK type from String to UUID
- **Change** ORM style to SQLAlchemy 2.0 mapped_column
- **Keep** `canonical_user_id`, `platform`, `platform_user_id`, `display_name`, `linked_at`, `linked_via`
- **Keep** unique composite index `(platform, platform_user_id)` -- this is the core lookup index

---

### WI-1.9: AgentDefinition Model
- **Depends on:** WI-1.5, WI-1.6
- **Port from:** New (schema from arch SS3.1)
- **Deliverables:**
  - `mypal/db/models/agent.py`
  - Alembic migration for `agent_definitions` table
- **Acceptance criteria:**
  - Migration creates `agent_definitions` table with columns: `id` (UUID PK), `tenant_id` (FK to tenants, NOT NULL), `name`, `persona_config` (JSONB), `llm_config` (JSONB), `memory_config` (JSONB), `tools` (JSONB array), `capabilities` (JSONB array), `sub_agents` (JSONB array), `max_concurrent` (int, default 10), `metadata` (JSONB), `is_default` (bool), `created_at`, `updated_at`
  - Index on `tenant_id` exists
  - Unique constraint on `(tenant_id, name)` -- no duplicate agent names within a tenant
  - Round-trip works
- **Estimated effort:** S

**Schema (from arch SS3.1):**

```python
class AgentDefinition(Base):
    __tablename__ = "agent_definitions"

    id: Mapped[uuid.UUID] = mapped_column(Uuid, primary_key=True, default=uuid.uuid4)
    tenant_id: Mapped[uuid.UUID] = mapped_column(Uuid, ForeignKey("tenants.id"), nullable=False, index=True)
    name: Mapped[str] = mapped_column(String(100))
    persona_config: Mapped[dict] = mapped_column(JSONB, default=dict)
    # persona_config example: {"system_prompt": "...", "personality": "...", "tone": "warm"}
    llm_config: Mapped[dict] = mapped_column(JSONB, default=dict)
    # llm_config example: {"provider": "anthropic", "model_tier": "mid", "temperature": 0.7, "max_tokens": 4096}
    memory_config: Mapped[dict] = mapped_column(JSONB, default=dict)
    # memory_config example: {"scopes": ["user_agent", "user", "agent", "tenant"], "budget_tokens": 2000}
    tools: Mapped[list] = mapped_column(JSONB, default=list)
    capabilities: Mapped[list] = mapped_column(JSONB, default=list)  # ["CHAT", "CODE", "VISION", ...]
    sub_agents: Mapped[list] = mapped_column(JSONB, default=list)
    max_concurrent: Mapped[int] = mapped_column(Integer, default=10)
    metadata_: Mapped[dict] = mapped_column("metadata", JSONB, default=dict)
    is_default: Mapped[bool] = mapped_column(Boolean, default=False)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now())
    updated_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now(), onupdate=func.now())

    __table_args__ = (UniqueConstraint("tenant_id", "name"),)
```

---

### WI-1.10: Session Model
- **Depends on:** WI-1.5, WI-1.6, WI-1.7, WI-1.9
- **Port from:** `../mypalclara/mypalclara/db/models.py` (Session class, lines 44-63), adapted per arch SS9.1
- **Deliverables:**
  - `mypal/db/models/session.py`
  - Alembic migration for `sessions` table
- **Acceptance criteria:**
  - Migration creates `sessions` table with columns: `id` (UUID PK), `tenant_id` (FK to tenants, NOT NULL), `user_id` (FK to users), `agent_id` (FK to agent_definitions), `channel_id`, `conversation_id` (nullable), `started_at`, `last_activity_at`, `summary` (text, nullable), `previous_session_id` (self-referencing FK, nullable)
  - Composite index on `(tenant_id, user_id, agent_id)` exists
  - Round-trip works
- **Estimated effort:** S

**Changes when porting from mypalclara Session:**
- **Add** `tenant_id` (FK to tenants) -- mypalclara had `project_id`
- **Add** `agent_id` (FK to agent_definitions) -- mypalclara sessions were agent-agnostic (only Clara)
- **Add** `conversation_id` -- for group conversations (arch SS9.1)
- **Add** `channel_id` -- replaces `context_id` with clearer name
- **Rename** `session_summary` -> `summary`
- **Remove** `project_id` -- replaced by `tenant_id`
- **Remove** `context_id` -- replaced by `channel_id`
- **Remove** `title` -- can add later; not needed for Phase 1
- **Remove** `archived` (string "true"/"false") -- if needed, use proper boolean later
- **Remove** `context_snapshot` -- Rook v2 handles context differently
- **Change** PK from String to UUID
- **Change** ORM to SQLAlchemy 2.0

---

### WI-1.11: Tool Permission Table
- **Depends on:** WI-1.5, WI-1.6, WI-1.7, WI-1.9
- **Port from:** New (schema from arch SS8.3)
- **Deliverables:**
  - `mypal/db/models/permissions.py`
  - Alembic migration for `tool_permissions` table
- **Acceptance criteria:**
  - Migration creates `tool_permissions` table with columns: `id` (UUID PK), `tenant_id` (FK to tenants, NOT NULL), `user_id` (FK to users, nullable), `agent_id` (FK to agent_definitions, nullable), `tool_id` (String, NOT NULL), `policy` (String: allow/deny/ask, NOT NULL), `set_by` (FK to users, nullable), `reason` (text, nullable), `created_at`, `updated_at`
  - Index on `(tenant_id, tool_id)` exists
  - Round-trip works
- **Estimated effort:** S

**Schema (from arch SS8.3):**

The permission resolution chain (arch SS8.3) is: user-specific override > agent-specific default > tenant-wide default > role-based default > system default (ALLOW). Phase 1 creates the table; the full resolver is Phase 2.

```python
class ToolPermission(Base):
    __tablename__ = "tool_permissions"

    id: Mapped[uuid.UUID] = mapped_column(Uuid, primary_key=True, default=uuid.uuid4)
    tenant_id: Mapped[uuid.UUID] = mapped_column(Uuid, ForeignKey("tenants.id"), nullable=False)
    user_id: Mapped[uuid.UUID | None] = mapped_column(Uuid, ForeignKey("users.id"), nullable=True)
    agent_id: Mapped[uuid.UUID | None] = mapped_column(Uuid, ForeignKey("agent_definitions.id"), nullable=True)
    tool_id: Mapped[str] = mapped_column(String(200), nullable=False)
    policy: Mapped[str] = mapped_column(String(10), nullable=False)  # allow, deny, ask
    set_by: Mapped[uuid.UUID | None] = mapped_column(Uuid, ForeignKey("users.id"), nullable=True)
    reason: Mapped[str | None] = mapped_column(Text, nullable=True)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now())
    updated_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now(), onupdate=func.now())

    __table_args__ = (
        Index("ix_tool_permission_lookup", "tenant_id", "tool_id"),
        Index("ix_tool_permission_user", "tenant_id", "user_id", "tool_id"),
    )
```

---

### WI-1.12: DB Models Package + Migration
- **Depends on:** WI-1.6 through WI-1.11
- **Port from:** New
- **Deliverables:**
  - `mypal/db/models/__init__.py` (re-exports all models)
  - Single consolidated Alembic migration for all Phase 1 tables (or chained migrations if cleaner)
- **Acceptance criteria:**
  - `alembic upgrade head` creates all 6 tables: `tenants`, `users`, `platform_links`, `agent_definitions`, `sessions`, `tool_permissions`
  - `alembic downgrade base` drops them cleanly
  - All foreign keys and indexes verified via `\d+ tablename` in psql
  - Every table has `tenant_id` except `tenants` itself (row-level isolation foundation)
- **Estimated effort:** S

---

## Stage 1C: Auth Layer

Goal: Clerk JWT verification, service secret auth, Clerk-to-CanonicalUser bridge, tenant isolation.

---

### WI-1.13: Clerk JWT Verification Middleware
- **Depends on:** WI-1.4, WI-1.7
- **Port from:** New (replaces `../mypalclara/identity/jwt_service.py` -- Clerk handles JWT issuance now)
- **Deliverables:**
  - `mypal/auth/clerk.py`
- **Acceptance criteria:**
  - Middleware fetches JWKS from Clerk's endpoint (cacheable, refresh on key rotation)
  - Decodes + validates JWT from `Authorization: Bearer <token>` header
  - Rejects expired tokens with 401
  - Rejects tokens with invalid signature with 401
  - Extracts `sub` (Clerk userId) and makes it available downstream
  - Works with a test JWT (can use `pyjwt` to create one signed with a test key, and configure JWKS endpoint to return that key)
- **Estimated effort:** M

**Implementation notes:**
- Use `pyjwt[crypto]` with `PyJWKClient` for JWKS fetching and caching
- The JWKS URL is `https://{clerk_frontend_api}/.well-known/jwks.json` or configurable via `settings.clerk_jwks_url`
- Cache the JWKS keyset; `PyJWKClient` handles this natively
- This replaces identity service's custom `jwt_service.encode/decode` (arch SS12.2 -- "Drop -- Clerk handles this")

---

### WI-1.14: Service Secret Auth
- **Depends on:** WI-1.4
- **Port from:** `../mypalclara/identity/app.py` (function `require_service_secret`, lines 61-65)
- **Deliverables:**
  - `mypal/auth/service.py`
- **Acceptance criteria:**
  - FastAPI dependency that checks `X-Service-Secret` header against `settings.service_secret`
  - Returns 401 if header missing or mismatched
  - If `settings.service_secret` is empty, dependency is a no-op (dev mode)
  - Test: request with valid header passes, request without header returns 401
- **Estimated effort:** S

**Changes when porting:** Essentially identical to identity service's `require_service_secret()`. Only difference is it reads from `settings.service_secret` (Pydantic) instead of bare `os.environ`.

---

### WI-1.15: Clerk-to-CanonicalUser Bridge
- **Depends on:** WI-1.13, WI-1.7, WI-1.8
- **Port from:** `../mypalclara/identity/app.py` (function `find_or_create_user`, lines 85-163, and `/users/ensure-link` endpoint, lines 278-317)
- **Deliverables:**
  - `mypal/auth/pairing.py` (bridge logic)
  - `mypal/api/v1/deps.py` (FastAPI dependencies: `get_current_user`, `get_current_tenant`)
- **Acceptance criteria:**
  - On first Clerk sign-in: if no PlatformLink with `platform="clerk"` and `platform_user_id=<clerk_userId>` exists, create a CanonicalUser + PlatformLink
  - On subsequent sign-ins: look up existing CanonicalUser via PlatformLink
  - `get_current_user` dependency: extracts Clerk JWT -> resolves to CanonicalUser -> returns User ORM object
  - `get_current_tenant` dependency: extracts tenant_id from the resolved user -> returns Tenant ORM object
  - Test: two requests with the same Clerk userId return the same CanonicalUser id
  - Test: first request creates the user, second request finds the existing one
- **Estimated effort:** M

**Changes when porting from identity service:**
- `find_or_create_user()` in identity service takes OAuth profile data (display_name, avatar, email). The Clerk bridge is simpler: Clerk JWT contains `sub` (userId) and we can call Clerk's backend API for profile data, or accept display_name in JWT claims.
- Identity service's `ensure-link` creates a CanonicalUser + PlatformLink in one shot. Same pattern, but now the user gets a `tenant_id` (from JWT metadata or a default tenant).
- The key architectural move (arch SS12.2): Clerk's `userId` becomes `PlatformLink(platform="clerk", platform_user_id=clerk_userId)`. All downstream code uses `canonical_user_id`, never `clerk_userId`.

---

### WI-1.16: Pairing Service (4 Modes)
- **Depends on:** WI-1.15, WI-1.6
- **Port from:** `../mypalclara/identity/app.py` (`ensure-link` endpoint, lines 278-317) + New logic from arch SS3.5
- **Deliverables:**
  - `mypal/agents/pairing.py`
- **Acceptance criteria:**
  - `PairingService.check_pairing(platform, platform_user_id, tenant_id)` returns `PairingResult` with state: PAIRED, UNKNOWN, PAIRING_STARTED, or BLOCKED
  - **Open mode:** unknown identity auto-creates user + link (same as current `ensure-link`)
  - **Approval mode:** unknown identity creates a pending pairing request; returns PAIRING_STARTED
  - **Invite-only mode:** unknown identity without valid pairing code is rejected
  - **Closed mode:** unknown identity is rejected outright
  - Tenant's pairing mode is read from `tenants.pairing_config` JSONB
  - Test: each mode tested with an unknown platform identity
- **Estimated effort:** M

**Changes when porting:**
- Identity service's `ensure-link` only supports what is essentially "open" mode (auto-create). MyPal adds 3 more modes per arch SS3.5.3.
- Pairing state machine (UNKNOWN -> PAIRING_STARTED -> PAIRED or BLOCKED) is new.
- Security property (arch SS3.5.4): pairing responses are templated strings, NOT LLM-generated. An unpaired user cannot reach the agent runtime.

---

### WI-1.17: Tenant Row-Level Isolation Middleware
- **Depends on:** WI-1.15
- **Port from:** New (arch SS4.3)
- **Deliverables:**
  - `mypal/db/isolation.py` (tenant-scoped session factory or query filter)
  - Update `mypal/api/v1/deps.py` to inject `tenant_id` into DB session context
- **Acceptance criteria:**
  - Every DB query automatically includes `WHERE tenant_id = :current_tenant_id` for tenant-scoped tables
  - Test: create records in tenant A and tenant B; query scoped to tenant A returns only tenant A records
  - Test: attempt to access tenant B record from tenant A context returns empty / 404, not the record
- **Estimated effort:** M

**Implementation approach:** SQLAlchemy event listener on `Session.do_orm_execute` that appends a `tenant_id` filter to all SELECT queries on tenant-scoped models. This is a query-interception approach rather than per-query manual filtering, which prevents accidentally forgetting the tenant filter.

---

## Stage 1D: LLM Providers

Goal: Anthropic and OpenAI providers working with typed message pipeline and model tiers.

---

### WI-1.18: Port Provider Base + Message Types
- **Depends on:** WI-1.1
- **Port from:**
  - `../mypalclara/mypalclara/core/llm/providers/base.py` (228 lines -- LLMProvider ABC, `_normalize_tools`)
  - `../mypalclara/mypalclara/core/llm/messages.py` (271 lines -- SystemMessage, UserMessage, AssistantMessage, ToolResultMessage, ContentPart)
  - `../mypalclara/mypalclara/core/llm/tools/schema.py` (110 lines -- ToolSchema)
  - `../mypalclara/mypalclara/core/llm/tools/response.py` (234 lines -- ToolCall, ToolResponse)
- **Deliverables:**
  - `mypal/llm/__init__.py`
  - `mypal/llm/base.py`
  - `mypal/llm/messages.py`
  - `mypal/llm/tools.py` (merges schema.py + response.py into one file)
- **Acceptance criteria:**
  - `from mypal.llm import LLMProvider, SystemMessage, UserMessage, AssistantMessage, ToolSchema, ToolResponse` works
  - Message types serialize to/from OpenAI dict format: `UserMessage("hello").to_dict()` returns `{"role": "user", "content": "hello"}`
  - `ToolSchema.from_openai_dict({"type": "function", "function": {"name": "test", ...}})` round-trips
  - Multimodal ContentPart (IMAGE_BASE64, IMAGE_URL) serializes correctly
- **Estimated effort:** M

**Changes when porting:**
- **Remove** LangChain dependency from base class: drop `get_langchain_model()` abstract method. MyPal uses direct SDKs only (no LangChain). This removes the largest transitive dependency tree.
- **Remove** sync methods (`complete`, `stream`): MyPal is async-first. Keep only `acomplete`, `acomplete_with_tools`, `astream`. Remove the `run_in_executor` wrappers -- direct async implementations instead.
- **Remove** `compat.py` entirely -- no backward compatibility layer needed for a new project.
- **Remove** `formats.py` (messages_to_langchain, messages_to_openai, etc.) -- providers handle their own format conversion internally.
- **Keep** all message types exactly as-is -- they're clean, well-tested, provider-neutral.
- **Keep** ToolSchema, ToolCall, ToolResponse -- these are the typed tool pipeline.
- **Merge** `tools/schema.py` and `tools/response.py` into one `tools.py` -- they're closely related and small enough.

---

### WI-1.19: Anthropic Direct Provider
- **Depends on:** WI-1.18, WI-1.3
- **Port from:** `../mypalclara/mypalclara/core/llm/providers/langchain.py` (DirectAnthropicProvider class, lines 257-367)
- **Deliverables:**
  - `mypal/llm/providers/anthropic.py`
- **Acceptance criteria:**
  - `provider = AnthropicProvider(api_key=settings.anthropic_api_key)`
  - `response = await provider.acomplete([SystemMessage("You are helpful"), UserMessage("Say hello")], config)` returns a non-empty string
  - `tool_response = await provider.acomplete_with_tools(messages, tools, config)` returns ToolResponse with correct tool_calls when given a tool-use prompt
  - `async for chunk in provider.astream(messages, config): ...` yields string chunks
  - Handles API errors gracefully (logs, raises typed exception)
- **Estimated effort:** M

**Changes when porting from DirectAnthropicProvider:**
- **Remove** sync methods (`complete`, `stream`, `complete_with_tools`) -- async only
- **Remove** `get_langchain_model()` -- no LangChain
- **Make** `_get_client` return `AsyncAnthropic` (from `anthropic` package) instead of sync `Anthropic`
- **Use** `await client.messages.create(...)` instead of sync `client.messages.create(...)`
- **Use** `async with client.messages.stream(...) as stream:` for streaming
- **Keep** the client caching pattern (cache by api_key + base_url)
- **Keep** `messages_to_anthropic()` conversion (extract system prompt, convert to Anthropic message format)
- **Keep** `ToolResponse.from_anthropic()` parsing

---

### WI-1.20: OpenAI Direct Provider
- **Depends on:** WI-1.18, WI-1.3
- **Port from:** `../mypalclara/mypalclara/core/llm/providers/langchain.py` (DirectOpenAIProvider class, lines 370-472)
- **Deliverables:**
  - `mypal/llm/providers/openai.py`
- **Acceptance criteria:**
  - `provider = OpenAIProvider(api_key=settings.openai_api_key)`
  - `response = await provider.acomplete([UserMessage("Say hello")], config)` returns a non-empty string
  - `tool_response = await provider.acomplete_with_tools(messages, tools, config)` returns ToolResponse with correct tool_calls
  - `async for chunk in provider.astream(messages, config): ...` yields string chunks
  - Handles API errors gracefully
- **Estimated effort:** M

**Changes when porting from DirectOpenAIProvider:**
- **Same pattern as Anthropic port:** remove sync methods, use `AsyncOpenAI`, native async calls
- **Use** `await client.chat.completions.create(...)` instead of sync call
- **Use** `stream=True` with `async for` for streaming
- **Keep** client caching, OpenAI message format conversion, `ToolResponse.from_openai()` parsing
- **Keep** the proxy string-response handling (`if isinstance(stream, str): yield stream`) -- useful for OpenAI-compatible proxies

---

### WI-1.21: Model Tier System + Provider Registry
- **Depends on:** WI-1.19, WI-1.20
- **Port from:**
  - `../mypalclara/mypalclara/core/llm/tiers.py` (245 lines -- ModelTier, DEFAULT_MODELS, get_model_for_tier)
  - `../mypalclara/mypalclara/core/llm/providers/registry.py` (133 lines -- ProviderRegistry)
  - `../mypalclara/mypalclara/core/llm/config.py` (261 lines -- LLMConfig)
- **Deliverables:**
  - `mypal/llm/tiers.py`
  - `mypal/llm/config.py`
  - `mypal/llm/registry.py`
- **Acceptance criteria:**
  - `ModelTier.HIGH`, `ModelTier.MID`, `ModelTier.LOW` enum values exist
  - `get_model_for_tier("high", "anthropic")` returns `"claude-opus-4-5"`
  - `get_model_for_tier("mid", "openai")` returns `"gpt-4o"`
  - `LLMConfig(provider="anthropic", model="claude-sonnet-4-5", api_key="...")` constructs without error
  - `LLMConfig.from_env(provider="anthropic", tier="high")` picks up `settings.anthropic_api_key` and resolves model from tier
  - `ProviderRegistry.get_provider("anthropic")` returns AnthropicProvider instance
  - `ProviderRegistry.get_provider("openai")` returns OpenAIProvider instance
  - Config supports per-agent override: `LLMConfig(provider="openai", ...)` can differ from `LLMConfig(provider="anthropic", ...)`
- **Estimated effort:** M

**Changes when porting:**

tiers.py:
- **Remove** openrouter, nanogpt, bedrock, azure from `DEFAULT_MODELS` -- Phase 1 is Anthropic + OpenAI only
- **Remove** corresponding branches in `get_model_for_tier()` and `get_base_model()`
- **Keep** ModelTier enum, DEFAULT_TIER, `get_tool_model()` (tools never use "low" tier)
- **Simplify** env var lookup -- use `settings` object instead of raw `os.getenv()`

config.py:
- **Remove** `from_env()` class method's openrouter, nanogpt, bedrock, azure branches
- **Remove** `aws_region`, `azure_deployment`, `azure_api_version` fields
- **Remove** `_get_cf_access_headers()` -- no Cloudflare tunnels
- **Remove** `tool_choice` from config (use per-call kwarg instead)
- **Use** `settings` for API keys instead of `os.getenv()`
- **Keep** `with_tier()` method for creating tier-specific configs from a base config

registry.py:
- **Replace** singleton pattern with a simpler dict-based registry
- **Remove** LangChain provider references
- **Map** `"anthropic"` -> `AnthropicProvider`, `"openai"` -> `OpenAIProvider`

---

## Stage 1E: Rook v2 Core

Goal: Memory table with pgvector, embedding generation, vector search, and round-trip tests for every scope.

---

### WI-1.22: Memories Table + pgvector HNSW Index
- **Depends on:** WI-1.5, WI-1.6, WI-1.7, WI-1.9, WI-1.10
- **Port from:** New (schema from arch SS5.4)
- **Deliverables:**
  - `mypal/db/models/memory.py`
  - Alembic migration for `memories` table with pgvector column + HNSW index
- **Acceptance criteria:**
  - Migration creates `memories` table matching arch SS5.4 schema (id, tenant_id, scope, agent_id, user_id, session_id, content, category, source, embedding vector(1536), embedding_model, FSRS fields, importance, confidence, supersedes, superseded_by, access_count, is_active, created_at, updated_at, expires_at)
  - CHECK constraint `valid_scope_refs` enforced (arch SS5.4): session scope requires session_id, user_agent requires both user_id and agent_id, etc.
  - HNSW index created: `CREATE INDEX idx_memories_embedding ON memories USING hnsw (embedding vector_cosine_ops) WITH (m = 16, ef_construction = 64)`
  - Insert a row with a 1536-dim vector, select it back, verify vector data integrity
- **Estimated effort:** M

**Full schema from arch SS5.4:**

```python
class Memory(Base):
    __tablename__ = "memories"

    id: Mapped[uuid.UUID] = mapped_column(Uuid, primary_key=True, default=uuid.uuid4)
    tenant_id: Mapped[uuid.UUID] = mapped_column(Uuid, ForeignKey("tenants.id"), nullable=False)
    scope: Mapped[str] = mapped_column(String(20), nullable=False)  # system, tenant, agent, user, user_agent, session
    agent_id: Mapped[uuid.UUID | None] = mapped_column(Uuid, ForeignKey("agent_definitions.id"), nullable=True)
    user_id: Mapped[uuid.UUID | None] = mapped_column(Uuid, ForeignKey("users.id"), nullable=True)
    session_id: Mapped[uuid.UUID | None] = mapped_column(Uuid, ForeignKey("sessions.id"), nullable=True)

    content: Mapped[str] = mapped_column(Text, nullable=False)
    category: Mapped[str] = mapped_column(String(20), nullable=False)  # fact, preference, observation, relationship, skill, context, instruction, event
    source: Mapped[str] = mapped_column(String(20), nullable=False)    # conversation, reflection, ingestion, api, user_edit, cross_agent, event

    embedding = mapped_column(Vector(1536), nullable=True)  # pgvector
    embedding_model: Mapped[str | None] = mapped_column(String(50), nullable=True)

    # FSRS fields (Phase 1: store defaults, full dynamics in Phase 3)
    fsrs_stability: Mapped[float] = mapped_column(Float, default=1.0)
    fsrs_difficulty: Mapped[float] = mapped_column(Float, default=0.5)
    fsrs_last_review: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    fsrs_next_review: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    fsrs_retrievability: Mapped[float] = mapped_column(Float, default=1.0)
    fsrs_reps: Mapped[int] = mapped_column(Integer, default=0)
    fsrs_lapses: Mapped[int] = mapped_column(Integer, default=0)
    fsrs_state: Mapped[str] = mapped_column(String(10), default="new")

    importance: Mapped[float] = mapped_column(Float, default=0.5)
    confidence: Mapped[float] = mapped_column(Float, default=1.0)
    supersedes: Mapped[uuid.UUID | None] = mapped_column(Uuid, ForeignKey("memories.id"), nullable=True)
    superseded_by: Mapped[uuid.UUID | None] = mapped_column(Uuid, ForeignKey("memories.id"), nullable=True)
    access_count: Mapped[int] = mapped_column(Integer, default=0)
    is_active: Mapped[bool] = mapped_column(Boolean, default=True)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now())
    updated_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now(), onupdate=func.now())
    expires_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)

    __table_args__ = (
        CheckConstraint("""
            (scope = 'session' AND session_id IS NOT NULL) OR
            (scope = 'user_agent' AND user_id IS NOT NULL AND agent_id IS NOT NULL) OR
            (scope = 'user' AND user_id IS NOT NULL) OR
            (scope = 'agent' AND agent_id IS NOT NULL) OR
            (scope IN ('tenant', 'system'))
        """, name="valid_scope_refs"),
        Index("ix_memories_tenant_scope", "tenant_id", "scope"),
        Index("ix_memories_tenant_user", "tenant_id", "user_id"),
        Index("ix_memories_tenant_agent", "tenant_id", "agent_id"),
    )
```

---

### WI-1.23: Session Summaries Table
- **Depends on:** WI-1.5, WI-1.10
- **Port from:** New (arch SS5.4)
- **Deliverables:**
  - `mypal/db/models/session_summary.py`
  - Alembic migration for `session_summaries` table
- **Acceptance criteria:**
  - Migration creates `session_summaries` table: `id`, `tenant_id`, `session_id` (FK), `summary_text`, `message_count`, `created_at`
  - Round-trip works
- **Estimated effort:** S

---

### WI-1.24: Memory CRUD + Scopes
- **Depends on:** WI-1.22
- **Port from:** New
- **Deliverables:**
  - `mypal/memory/__init__.py`
  - `mypal/memory/scopes.py` (MemoryScope enum, MemoryCategory enum, MemorySource enum)
  - `mypal/memory/crud.py` (create_memory, get_memory, update_memory, delete_memory, list_memories_by_scope)
- **Acceptance criteria:**
  - `MemoryScope` enum has values: SYSTEM, TENANT, AGENT, USER, USER_AGENT, SESSION
  - `MemoryCategory` enum has values: FACT, PREFERENCE, OBSERVATION, RELATIONSHIP, SKILL, CONTEXT, INSTRUCTION, EVENT
  - `await create_memory(tenant_id=..., scope=MemoryScope.USER, user_id=..., content="likes blue", ...)` returns a Memory with a UUID id
  - `await get_memory(memory_id)` returns the same memory
  - `await update_memory(memory_id, content="likes green")` updates in place
  - `await delete_memory(memory_id)` soft-deletes (sets `is_active=False`)
  - `await list_memories_by_scope(tenant_id, scope=MemoryScope.USER, user_id=user_id)` returns only matching memories
  - All operations enforce tenant_id scoping
- **Estimated effort:** M

---

### WI-1.25: Embedding Generation
- **Depends on:** WI-1.20 (OpenAI provider, for embeddings API)
- **Port from:** New
- **Deliverables:**
  - `mypal/memory/embeddings.py`
- **Acceptance criteria:**
  - `embedding = await generate_embedding("User's favorite color is blue")` returns a list of 1536 floats
  - Uses OpenAI `text-embedding-3-small` model via `AsyncOpenAI` client
  - Handles API errors (rate limit, timeout) with retry
  - Embedding is normalized (L2 norm ~ 1.0)
  - Batch support: `embeddings = await generate_embeddings(["text1", "text2"])` returns list of 2 vectors
- **Estimated effort:** S

---

### WI-1.26: Vector Search with Cosine Similarity
- **Depends on:** WI-1.22, WI-1.25
- **Port from:** New (arch SS5.4, SS5.5)
- **Deliverables:**
  - `mypal/memory/retriever.py`
- **Acceptance criteria:**
  - `results = await vector_search(query_embedding, tenant_id, scopes=[MemoryScope.USER], user_id=..., limit=10)` returns memories ordered by cosine similarity (highest first)
  - Search respects tenant_id filter (never returns cross-tenant results)
  - Search respects scope + scope refs (user_id for USER scope, agent_id for AGENT scope, etc.)
  - Only returns `is_active=True` memories
  - Returns similarity score alongside each memory
  - Performance: <100ms for 10k memories (HNSW index)
- **Estimated effort:** M

**SQL pattern:**
```sql
SELECT *, 1 - (embedding <=> :query_embedding) AS similarity
FROM memories
WHERE tenant_id = :tenant_id
  AND scope = :scope
  AND user_id = :user_id  -- when scope requires it
  AND is_active = TRUE
ORDER BY embedding <=> :query_embedding
LIMIT :limit
```

---

### WI-1.27: Scoped Retrieval (All 6 Scopes)
- **Depends on:** WI-1.26, WI-1.24
- **Port from:** New (arch SS5.3, SS5.5)
- **Deliverables:**
  - `mypal/memory/manager.py` (high-level RookManager class)
- **Acceptance criteria:**
  - `manager = RookManager(session)`
  - `await manager.ingest(content="...", scope=MemoryScope.USER, tenant_id=..., user_id=..., category=MemoryCategory.FACT)` creates a memory with embedding
  - `results = await manager.retrieve(query="...", tenant_id=..., scopes=[(MemoryScope.USER, {"user_id": uid}), (MemoryScope.AGENT, {"agent_id": aid})])` queries across multiple scopes
  - Results ordered by `similarity * 0.5 + retrievability * 0.3 + importance * 0.2` (arch SS5.5 scoring formula)
  - Retrieval across all 6 scopes works: SYSTEM, TENANT, AGENT, USER, USER_AGENT, SESSION
  - Scope priority order (arch SS5.3): SESSION > USER_AGENT > USER > AGENT > TENANT > SYSTEM
- **Estimated effort:** M

---

## Stage 1F: Agent Runtime

Goal: POST /api/v1/chat accepts a message, builds context from memory, calls LLM, returns response.

---

### WI-1.28: Basic AgentRuntime
- **Depends on:** WI-1.21 (providers), WI-1.27 (memory), WI-1.9 (agent model), WI-1.10 (session model)
- **Port from:** `../mypalclara/mypalclara/gateway/processor.py` (MessageProcessor class, pipeline structure) and `../mypalclara/mypalclara/gateway/llm_orchestrator.py` (LLMOrchestrator class, LLM call loop)
- **Deliverables:**
  - `mypal/agents/runtime.py`
- **Acceptance criteria:**
  - `runtime = AgentRuntime(definition=agent_def, session=session, llm=provider, memory=rook_manager)`
  - `response = await runtime.process(message="Hello")` returns an LLM-generated text response
  - Processing pipeline:
    1. Load agent definition (persona, llm_config)
    2. Build system prompt from persona_config
    3. Retrieve relevant memories via RookManager
    4. Build message list: [system_prompt, ...memories_as_context, ...session_history, user_message]
    5. Call LLM provider with message list
    6. Return response text
  - Agent's `llm_config` is used to select provider + model tier
  - Session history is loaded from DB (last N messages)
  - Memory context is injected into the system prompt or as separate system messages
- **Estimated effort:** L

**What's ported from processor.py and what changes:**
- The core pipeline structure (context build -> LLM call -> response) maps from `MessageProcessor.process_message()` in processor.py
- **Remove** WebSocket streaming protocol -- Phase 1 is HTTP request/response, not streaming
- **Remove** tool execution loop -- Phase 1 has no tool execution (tools are Phase 2)
- **Remove** auto-tier classification -- not needed yet
- **Remove** target classifier -- not needed (single-agent Phase 1)
- **Remove** per-user VM integration
- **Remove** channel summary management
- **Keep** the pattern of: fetch history, fetch memories, build context, call LLM
- **Simplify** from LLMOrchestrator's multi-turn loop to single LLM call (no tools)

---

### WI-1.29: POST /api/v1/chat Endpoint
- **Depends on:** WI-1.28, WI-1.15 (auth)
- **Port from:** New (arch SS6.3)
- **Deliverables:**
  - `mypal/api/v1/chat.py`
- **Acceptance criteria:**
  - `POST /api/v1/chat` with body `{"message": "Hello", "agent_id": "<uuid>"}` and valid Clerk JWT returns `{"response": "...", "session_id": "..."}`
  - Auth required: request without JWT returns 401
  - Creates a new session if none exists for (user, agent, channel)
  - Persists both user message and assistant response to `sessions`/messages
  - Returns the agent's text response
  - Test: `curl -X POST http://localhost:8000/api/v1/chat -H "Authorization: Bearer <jwt>" -H "Content-Type: application/json" -d '{"message": "Hello"}'` returns a 200 with JSON body containing an LLM response
- **Estimated effort:** M

**Request/response schema:**

```python
class ChatRequest(BaseModel):
    message: str
    agent_id: uuid.UUID | None = None  # None = tenant default agent
    session_id: uuid.UUID | None = None  # None = create or find existing session
    channel_id: str = "web"  # Default channel for web UI

class ChatResponse(BaseModel):
    response: str
    session_id: uuid.UUID
    agent_id: uuid.UUID
    message_id: uuid.UUID  # ID of the persisted assistant message
```

---

## Stage 1G: Frontend Scaffold

Goal: React frontend from `feat/web-ui-rebuild` copied into `web/`, buildable, and proxied to FastAPI in dev.

---

### WI-1.30: Copy Frontend from feat/web-ui-rebuild
- **Depends on:** None (can happen in parallel with backend work)
- **Port from:** `../mypalclara` branch `feat/web-ui-rebuild`, path `web-ui/frontend/` -> `web/`
- **Deliverables:**
  - `web/` directory with full React app
- **Acceptance criteria:**
  - `cd web && npm install` (or `pnpm install`) completes without errors
  - `npm run dev` starts Vite dev server on port 5173
  - Browser at `http://localhost:5173` shows the app (Clerk sign-in page or chat UI)
  - All source files present matching the file listing from `feat/web-ui-rebuild`:
    - `src/App.tsx`, `src/main.tsx`, `src/index.css`
    - `src/auth/ClerkProvider.tsx`, `src/auth/TokenBridge.tsx`
    - `src/components/ui/` (18 shadcn primitives)
    - `src/components/assistant-ui/` (thread, markdown, attachment, tool-fallback, tooltip-icon-button)
    - `src/components/chat/` (ArtifactPanel, BranchSidebar, ChatRuntimeProvider, MergeDialog, TierSelector, ToolCallBlock)
    - `src/components/knowledge/` (MemoryCard, MemoryEditor, MemoryGrid, MemoryList, SearchBar)
    - `src/components/layout/` (AppLayout, UnifiedSidebar)
    - `src/components/settings/` (AdapterLinking)
    - `src/hooks/` (useBranches, useGatewayWebSocket, useMemories, useTheme)
    - `src/stores/` (artifactStore, chatRuntime, chatStore, savedSets)
    - `src/pages/` (Chat, KnowledgeBase, Settings)
    - `src/api/client.ts`
    - `src/lib/` (attachmentAdapter, threadGroups, utils)
    - `src/utils/fileProcessing.ts`
    - `vite.config.ts`, `tsconfig.json`, `package.json`, `components.json`
- **Estimated effort:** S

**How to copy:**
```bash
cd /Users/heidornj/Code/mypalclara
git archive feat/web-ui-rebuild -- web-ui/frontend/ | tar -x -C /tmp/
cp -r /tmp/web-ui/frontend/ /Users/heidornj/Code/MyPal/web/
```

---

### WI-1.31: Wire Frontend to FastAPI
- **Depends on:** WI-1.30, WI-1.4
- **Port from:** New
- **Deliverables:**
  - Update `web/vite.config.ts` with API proxy configuration
  - Add static file serving to `mypal/main.py` (production mode)
- **Acceptance criteria:**
  - **Dev mode:** `npm run dev` (Vite on :5173) proxies `/api/*` requests to FastAPI on :8000
  - **Dev mode:** `curl http://localhost:5173/api/v1/health` returns `{"status": "ok"}` (proxied to FastAPI)
  - **Production mode:** `npm run build` produces `web/dist/`; FastAPI serves `web/dist/` as static files at `/`
  - **Production mode:** `curl http://localhost:8000/` returns the React app's index.html
  - **Production mode:** `curl http://localhost:8000/api/v1/health` still returns the API response (API routes take priority over static files)
- **Estimated effort:** S

**Vite proxy config addition:**
```typescript
// vite.config.ts
export default defineConfig({
  server: {
    proxy: {
      '/api': {
        target: 'http://localhost:8000',
        changeOrigin: true,
      },
      '/ws': {
        target: 'ws://localhost:8000',
        ws: true,
      },
    },
  },
  // ... rest of existing config
});
```

**FastAPI static file serving:**
```python
# In main.py, after all API routes:
if not settings.debug:
    app.mount("/", StaticFiles(directory="web/dist", html=True), name="frontend")
```

---

## Stage 1H: Integration Tests

Goal: Prove the system works end-to-end. The Cortex lesson: if you don't test the round-trip, it's a stub.

---

### WI-1.32: Memory Round-Trip Tests (Every Scope)
- **Depends on:** WI-1.27
- **Port from:** New (test pattern from arch SS5.11)
- **Deliverables:**
  - `tests/integration/test_memory_roundtrip.py`
- **Acceptance criteria:**
  - **6 tests, one per scope.** Each test:
    1. Creates a memory via `RookManager.ingest()` with the correct scope and scope refs
    2. Retrieves it via `RookManager.retrieve()` with a semantically related query
    3. Asserts the original content appears in results
  - **SYSTEM scope:** ingest with scope=SYSTEM, retrieve with scope filter [SYSTEM], verify found
  - **TENANT scope:** ingest with scope=TENANT + tenant_id, retrieve scoped to same tenant, verify found; retrieve scoped to different tenant, verify NOT found
  - **AGENT scope:** ingest with scope=AGENT + agent_id, retrieve with agent_id filter, verify found
  - **USER scope:** ingest with scope=USER + user_id, retrieve with user_id filter, verify found
  - **USER_AGENT scope:** ingest with scope=USER_AGENT + user_id + agent_id, retrieve with both filters, verify found
  - **SESSION scope:** ingest with scope=SESSION + session_id, retrieve with session_id filter, verify found
  - **Cross-tenant isolation test:** memory in tenant A is NOT returned when querying tenant B
  - All tests run against real Postgres (Docker Compose), not mocks
  - `pytest tests/integration/test_memory_roundtrip.py -v` -- all pass
- **Estimated effort:** M

**This is the Cortex lesson (arch SS5.11):** Cortex had `_store_longterm()` and `_semantic_search()` as stubs that silently did nothing. These tests prove every storage path actually works. If any test fails, the memory system is broken. No silent failures.

---

### WI-1.33: End-to-End Chat Test
- **Depends on:** WI-1.29
- **Port from:** New
- **Deliverables:**
  - `tests/integration/test_chat_e2e.py`
- **Acceptance criteria:**
  - Test creates a tenant, user, agent definition, and PlatformLink in the DB
  - Test calls `POST /api/v1/chat` with a valid auth context (mock or test JWT)
  - Response contains a non-empty LLM response string
  - A new session was created in the DB
  - Both user message and assistant response are persisted in the DB
  - `pytest tests/integration/test_chat_e2e.py -v` passes
- **Estimated effort:** M

**Test structure:**
```python
async def test_chat_e2e(test_client, test_db):
    # Setup: create tenant, user, agent, platform link
    tenant = await create_test_tenant(test_db)
    user = await create_test_user(test_db, tenant_id=tenant.id)
    agent = await create_test_agent(test_db, tenant_id=tenant.id)

    # Act: send chat message
    response = await test_client.post(
        "/api/v1/chat",
        json={"message": "Hello, what is 2+2?", "agent_id": str(agent.id)},
        headers={"Authorization": f"Bearer {make_test_jwt(user.id)}"},
    )

    # Assert
    assert response.status_code == 200
    body = response.json()
    assert body["response"]  # Non-empty
    assert body["session_id"]
    assert body["agent_id"] == str(agent.id)

    # Verify persistence
    session = await test_db.get(Session, uuid.UUID(body["session_id"]))
    assert session is not None
    assert session.tenant_id == tenant.id
```

---

### WI-1.34: Auth Flow Test
- **Depends on:** WI-1.15, WI-1.13
- **Port from:** New
- **Deliverables:**
  - `tests/integration/test_auth.py`
- **Acceptance criteria:**
  - **Clerk JWT -> CanonicalUser resolution:**
    - Create a test JWT signed with a test RSA key
    - Configure test JWKS endpoint to serve the corresponding public key
    - Call an authenticated endpoint with the JWT
    - Verify a CanonicalUser was created with a PlatformLink(platform="clerk")
    - Call again with same JWT -- verify same CanonicalUser is returned (no duplicate)
  - **Missing/invalid JWT returns 401:**
    - Request with no Authorization header -> 401
    - Request with expired JWT -> 401
    - Request with JWT signed by wrong key -> 401
  - **Service secret auth:**
    - Request with valid X-Service-Secret header -> passes
    - Request with invalid X-Service-Secret header -> 401
    - Request with no X-Service-Secret header -> 401
  - `pytest tests/integration/test_auth.py -v` -- all pass
- **Estimated effort:** M

---

## Dependency Graph Summary

```
WI-1.1 (pyproject.toml)
WI-1.2 (Docker Compose)          -- independent of 1.1
WI-1.3 (Config)                  -- depends on 1.1
WI-1.4 (main.py)                 -- depends on 1.1, 1.3

WI-1.5 (Alembic)                 -- depends on 1.2, 1.3, 1.4
WI-1.6 (Tenant model)            -- depends on 1.5
WI-1.7 (User model)              -- depends on 1.5, 1.6
WI-1.8 (PlatformLink model)      -- depends on 1.5, 1.7
WI-1.9 (AgentDef model)          -- depends on 1.5, 1.6
WI-1.10 (Session model)          -- depends on 1.5, 1.6, 1.7, 1.9
WI-1.11 (ToolPermission model)   -- depends on 1.5, 1.6, 1.7, 1.9
WI-1.12 (Models package)         -- depends on 1.6-1.11

WI-1.13 (Clerk JWT)              -- depends on 1.4, 1.7
WI-1.14 (Service secret)         -- depends on 1.4
WI-1.15 (Clerk bridge)           -- depends on 1.13, 1.7, 1.8
WI-1.16 (Pairing service)        -- depends on 1.15, 1.6
WI-1.17 (Tenant isolation)       -- depends on 1.15

WI-1.18 (LLM base + messages)    -- depends on 1.1
WI-1.19 (Anthropic provider)     -- depends on 1.18, 1.3
WI-1.20 (OpenAI provider)        -- depends on 1.18, 1.3
WI-1.21 (Tiers + registry)       -- depends on 1.19, 1.20

WI-1.22 (Memories table)         -- depends on 1.5, 1.6, 1.7, 1.9, 1.10
WI-1.23 (Session summaries)      -- depends on 1.5, 1.10
WI-1.24 (Memory CRUD)            -- depends on 1.22
WI-1.25 (Embeddings)             -- depends on 1.20
WI-1.26 (Vector search)          -- depends on 1.22, 1.25
WI-1.27 (Scoped retrieval)       -- depends on 1.26, 1.24

WI-1.28 (AgentRuntime)           -- depends on 1.21, 1.27, 1.9, 1.10
WI-1.29 (Chat endpoint)          -- depends on 1.28, 1.15

WI-1.30 (Copy frontend)          -- no backend dependencies
WI-1.31 (Wire frontend)          -- depends on 1.30, 1.4

WI-1.32 (Memory tests)           -- depends on 1.27
WI-1.33 (Chat e2e test)          -- depends on 1.29
WI-1.34 (Auth test)              -- depends on 1.15, 1.13
```

**Critical path:** 1.1 -> 1.3 -> 1.4 -> 1.5 -> 1.7 -> 1.8 -> 1.13 -> 1.15 -> 1.29 -> 1.33

**Parallelizable streams:**
- LLM providers (1.18-1.21) can proceed in parallel with DB models (1.6-1.12)
- Frontend (1.30-1.31) can proceed in parallel with everything
- Memory system (1.22-1.27) can proceed once DB models are done, in parallel with auth (1.13-1.17)

---

## Project Structure After Phase 1

```
mypal/
├── mypal/
│   ├── __init__.py
│   ├── main.py                     # FastAPI app, lifespan, CORS
│   ├── config.py                   # Pydantic BaseSettings
│   │
│   ├── api/v1/
│   │   ├── __init__.py
│   │   ├── chat.py                 # POST /api/v1/chat
│   │   └── deps.py                 # get_current_user, get_current_tenant
│   │
│   ├── agents/
│   │   ├── __init__.py
│   │   ├── runtime.py              # AgentRuntime (context build -> LLM)
│   │   └── pairing.py              # PairingService (4 modes)
│   │
│   ├── auth/
│   │   ├── __init__.py
│   │   ├── clerk.py                # Clerk JWT verification (JWKS)
│   │   ├── pairing.py              # Clerk -> CanonicalUser bridge
│   │   └── service.py              # X-Service-Secret auth
│   │
│   ├── db/
│   │   ├── __init__.py
│   │   ├── base.py                 # Declarative base
│   │   ├── session.py              # Async engine + sessionmaker
│   │   ├── isolation.py            # Tenant row-level isolation
│   │   └── models/
│   │       ├── __init__.py         # Re-exports all models
│   │       ├── tenant.py
│   │       ├── user.py
│   │       ├── platform_link.py
│   │       ├── agent.py
│   │       ├── session.py
│   │       ├── memory.py
│   │       ├── session_summary.py
│   │       └── permissions.py
│   │
│   ├── llm/
│   │   ├── __init__.py
│   │   ├── base.py                 # LLMProvider ABC (async only)
│   │   ├── messages.py             # SystemMessage, UserMessage, etc.
│   │   ├── tools.py                # ToolSchema, ToolCall, ToolResponse
│   │   ├── tiers.py                # ModelTier, DEFAULT_MODELS
│   │   ├── config.py               # LLMConfig dataclass
│   │   ├── registry.py             # ProviderRegistry
│   │   └── providers/
│   │       ├── __init__.py
│   │       ├── anthropic.py        # AnthropicProvider
│   │       └── openai.py           # OpenAIProvider
│   │
│   └── memory/
│       ├── __init__.py
│       ├── scopes.py               # MemoryScope, MemoryCategory, MemorySource enums
│       ├── crud.py                 # Memory CRUD operations
│       ├── embeddings.py           # text-embedding-3-small
│       ├── retriever.py            # Vector search + cosine similarity
│       └── manager.py              # RookManager (high-level API)
│
├── web/                            # React frontend (copied from feat/web-ui-rebuild)
│   ├── package.json
│   ├── vite.config.ts
│   ├── src/
│   │   └── ... (full React app)
│   └── ...
│
├── tests/
│   ├── conftest.py
│   └── integration/
│       ├── test_memory_roundtrip.py
│       ├── test_chat_e2e.py
│       └── test_auth.py
│
├── alembic/
│   ├── env.py
│   └── versions/
│
├── scripts/
│   └── init-db.sql
│
├── alembic.ini
├── docker-compose.yml
├── pyproject.toml
├── .env.example
└── .gitignore
```
