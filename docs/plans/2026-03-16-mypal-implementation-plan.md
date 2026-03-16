# MyPal Implementation Plan

**Created:** 2026-03-16
**Architecture Reference:** `docs/MyPal_Master_Architecture.md`
**Source Codebase:** `../mypalclara/` (backend: main branch, frontend: `feat/web-ui-rebuild` branch)

---

## Overview

Work-order style implementation plan for MyPal: a multi-agent, multi-user, multi-tenant personal AI platform. 6 phases, ~146 work items, ~26 weeks.

Each work item specifies dependencies, port sources, deliverables, runnable acceptance criteria, and effort estimates. Execute top-to-bottom within each phase.

## Constraints

- Postgres + Redis in Docker Compose; MyPal app runs locally
- Phase 1 LLM providers: Anthropic + OpenAI only
- Test-after (not TDD)
- Frontend scaffold from `feat/web-ui-rebuild` branch
- All tables include `tenant_id` for row-level isolation

## Phase Documents

| Phase | Weeks | Focus | Work Items | Document |
|-------|-------|-------|------------|----------|
| **1** | 1-6 | Foundation | 34 | [phase-1-foundation.md](phase-1-foundation.md) |
| **2** | 7-10 | Multi-Agent + Tools | 32 | [phase-2-multi-agent-tools.md](phase-2-multi-agent-tools.md) |
| **3** | 11-14 | Platform Adapters | 23 | [phase-3-platform-adapters.md](phase-3-platform-adapters.md) |
| **4** | 15-18 | Multi-Input | 16 | [phase-4-5-6-remaining.md](phase-4-5-6-remaining.md#phase-4-multi-input-weeks-15-18) |
| **5** | 19-22 | Web UI & Polish | 25 | [phase-4-5-6-remaining.md](phase-4-5-6-remaining.md#phase-5-web-ui--polish-weeks-19-22) |
| **6** | 23-26 | Multi-Tenancy & Production | 16 | [phase-4-5-6-remaining.md](phase-4-5-6-remaining.md#phase-6-multi-tenancy--production-weeks-23-26) |

**Total: 146 work items across 6 phases**

## Critical Path

```
Phase 1A (bootstrap) → 1B (models) → 1C (auth) → 1F (agent runtime)
                          ↓                           ↑
                        1D (LLM providers) ───────────┘
                          ↓                           ↑
                        1E (Rook v2 core) ────────────┘
```

Phase 1D and 1E can be parallelized off of 1B. Phase 1F requires all three upstream stages.

## Key Porting Decisions

| Component | Source | Strategy |
|-----------|--------|----------|
| LLM Providers | `mypalclara/core/llm/` | Extract wholesale, drop LangChain, async-only |
| Identity/Pairing | `identity/` | Port CanonicalUser + PlatformLink, drop custom JWT (Clerk handles it) |
| Gateway Pipeline | `mypalclara/gateway/` | Port processor + orchestrator structure, strip platform coupling |
| Tools System | `mypalclara/tools/` | Extract wholesale, very clean |
| Plugin System | `mypalclara/core/plugins/` | Extract wholesale |
| MCP System | `clara_core/mcp/` | Extract adapter + client manager |
| Adapters | `mypalclara/adapters/` | Port base class + error recovery only, rewrite platform adapters |
| Memory System | `mypalclara/core/memory/` | New implementation (Rook v2), informed by architecture |
| Frontend | `web-ui/frontend/` (feat/web-ui-rebuild) | ~35% copy-as-is, ~65% adapt for multi-agent/tenant |
| DB Models | `mypalclara/db/models.py` | Port core tables, add tenant_id everywhere, UUID PKs |

## Tech Stack

**Backend:** Python 3.12+, FastAPI, SQLAlchemy 2.0 (async), Alembic, asyncpg, Redis, pgvector
**Frontend:** React 19, TypeScript 5.9, Vite 7, Tailwind CSS 4, shadcn/ui, assistant-ui, Zustand 5, Clerk
**Data:** PostgreSQL 17 (with pgvector), Redis 7, FalkorDB (optional, future)
**LLM:** Anthropic SDK, OpenAI SDK (Phase 1); more providers in later phases
