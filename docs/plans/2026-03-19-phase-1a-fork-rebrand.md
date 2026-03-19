# Phase 1a: Fork & Rebrand Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Hard-fork MyPal master into the MyPal repo and rebrand all references from MyPal/OpenClaw to MyPal.

**Architecture:** Clone MyPal's Go monorepo (backend + SolidJS frontend, single-binary embed) into the MyPal repo, then systematically rename the binary, CLI commands, environment variable prefix, module path, frontend branding, and Docker artifacts. Verify the build and tests still pass after each rename step.

**Tech Stack:** Go 1.25+, SolidJS + Vite, pnpm workspaces, Turbo, gqlgen, GORM, Docker multi-stage build

---

## Context: MyPal Repo Structure

```
apps/
  backend/
    cmd/mypal/         # CLI entry point (Cobra)
      main.go                # Binary name "mypal"
      serve/                 # serve command (app, channels, graphql, http, database, mcp, services, config, lifecycle)
    internal/
      domain/                # Models, ports (interfaces), services, handlers, repos, events, errors
      application/           # GraphQL resolvers, health, MCP, metrics, registry, webhooks
      infrastructure/        # Adapters (AI, messaging, MCP, browser, filesystem, terminal, memory, audio), config, persistence, secrets, logging
    go.mod                   # module github.com/Neirth/MyPal/apps/backend
    go.sum
  frontend/
    src/                     # SolidJS app (components, graphql, hooks, views, stores, styles)
    package.json             # @mypal/frontend
    vite.config.ts
    index.html
  ui/                        # Shared component library
    package.json             # @mypal/ui
schema/                      # GraphQL schema files (*.graphql)
.docker/Dockerfile.static    # Multi-stage production build
turbo.json                   # Monorepo task runner
pnpm-workspace.yaml          # Workspace config
package.json                 # Root package.json
```

**Key branding touchpoints:**
- Go module path: `github.com/Neirth/MyPal/apps/backend`
- Binary name: `mypal`
- Env var prefix: `MYPAL_*`
- pnpm package names: `@mypal/*`
- Frontend title/branding strings
- Docker image references
- CLI help text and version strings
- Config file references

---

### Task 1: Import MyPal Source

**Files:**
- Create: All MyPal source files in MyPal repo root

**Step 1: Clone MyPal into a temporary directory**

```bash
git clone --depth 1 https://github.com/Neirth/MyPal.git /tmp/mypal-fork
```

**Step 2: Copy source into MyPal repo (excluding .git)**

```bash
rsync -av --exclude='.git' /tmp/mypal-fork/ /Users/heidornj/Code/MyPal/
```

This preserves our existing `docs/` directory and git history while bringing in all MyPal source.

**Step 3: Verify the file tree landed correctly**

```bash
ls apps/backend/cmd/mypal/main.go  # should exist
ls apps/frontend/src/App.tsx             # should exist
ls turbo.json                            # should exist
```

**Step 4: Commit the raw fork**

```bash
git add -A
git commit -m "Fork MyPal master as MyPal base"
```

This is the "before any changes" snapshot. All subsequent commits are our modifications.

---

### Task 2: Rename Go Module Path

**Files:**
- Modify: `apps/backend/go.mod`
- Modify: All `*.go` files containing the import path

**Step 1: Update go.mod module declaration**

In `apps/backend/go.mod`, change:
```
module github.com/Neirth/MyPal/apps/backend
```
to:
```
module github.com/BangRocket/MyPal/apps/backend
```

**Step 2: Find and replace all Go import paths**

```bash
cd apps/backend
find . -name '*.go' -exec sed -i '' 's|github.com/Neirth/MyPal/apps/backend|github.com/BangRocket/MyPal/apps/backend|g' {} +
```

**Step 3: Verify no stale imports remain**

```bash
grep -r "Neirth/MyPal" apps/backend/ --include='*.go' | head -20
```

Expected: no matches.

**Step 4: Verify Go module resolves**

```bash
cd apps/backend && go mod tidy
```

Expected: no errors. If there are dependency issues, resolve them.

**Step 5: Commit**

```bash
git add apps/backend/
git commit -m "Rename Go module path to github.com/BangRocket/MyPal"
```

---

### Task 3: Rename Binary and CLI

**Files:**
- Rename: `apps/backend/cmd/mypal/` → `apps/backend/cmd/mypal/`
- Modify: `apps/backend/cmd/mypal/main.go` (binary name, version strings, help text)
- Modify: `apps/backend/cmd/mypal/serve/*.go` (any "mypal" references in help/logging)
- Modify: `.docker/Dockerfile.static` (binary build path and output name)
- Modify: `turbo.json` (if it references the binary name)
- Modify: Root `package.json` and `Makefile` (if they reference the binary)

**Step 1: Rename the cmd directory**

```bash
mv apps/backend/cmd/mypal apps/backend/cmd/mypal
```

**Step 2: Update main.go**

Replace all "mypal" / "MyPal" references with "mypal" / "MyPal" in:
- `apps/backend/cmd/mypal/main.go`
- All files in `apps/backend/cmd/mypal/serve/`

Search for: `mypal`, `MyPal`, `open-lobster`, `open_lobster`

**Step 3: Update Dockerfile build path**

In `.docker/Dockerfile.static`, change:
```
go build -o /app/mypal ./cmd/mypal
```
to:
```
go build -o /app/mypal ./cmd/mypal
```

Also update the ENTRYPOINT/CMD if it references the binary name.

**Step 4: Update turbo.json and root package.json**

Search for "mypal" in:
- `turbo.json`
- `package.json` (root)
- `pnpm-workspace.yaml`

Replace with "mypal" where appropriate.

**Step 5: Verify build compiles**

```bash
cd apps/backend && go build -o /tmp/mypal ./cmd/mypal
/tmp/mypal version
```

Expected: binary builds, version command outputs something (even if it says "dev").

**Step 6: Commit**

```bash
git add -A
git commit -m "Rename binary and CLI from mypal to mypal"
```

---

### Task 4: Rename Environment Variable Prefix

**Files:**
- Modify: `apps/backend/internal/infrastructure/config/config.go` (env var binding logic)
- Modify: All Go files that reference `MYPAL_*` env vars
- Modify: `.env.example` or any example config files
- Modify: `.docker/Dockerfile.static` (if env vars are set there)
- Modify: `docker-compose*.yml` (if they exist)
- Modify: Documentation/README referencing env vars

**Step 1: Find all MYPAL_ references**

```bash
grep -r "MYPAL" --include='*.go' --include='*.yaml' --include='*.yml' --include='*.env*' --include='*.md' --include='*.json' -l
```

**Step 2: Replace MYPAL_ with MYPAL_ in all files**

The config system in `config.go` binds env vars with a prefix. Find the prefix definition and change it:
```go
// Before
envPrefix = "MYPAL"
// After
envPrefix = "MYPAL"
```

Then do a global replacement across all file types:
```bash
grep -rl "MYPAL" --include='*.go' --include='*.yaml' --include='*.yml' --include='*.env*' --include='*.md' --include='*.json' --include='*.ts' --include='*.tsx' | xargs sed -i '' 's/MYPAL/MYPAL/g'
```

**Step 3: Verify no stale references**

```bash
grep -r "MYPAL" --include='*.go' --include='*.yaml' --include='*.yml' --include='*.env*' | head -20
```

Expected: no matches (docs may still have historical references — that's OK).

**Step 4: Verify config loads**

```bash
cd apps/backend && go build -o /tmp/mypal ./cmd/mypal
MYPAL_GRAPHQL_AUTH_TOKEN=test /tmp/mypal serve --help
```

Expected: help text displays without errors.

**Step 5: Commit**

```bash
git add -A
git commit -m "Rename env var prefix from MYPAL_ to MYPAL_"
```

---

### Task 5: Rename Frontend Package Names and Branding

**Files:**
- Modify: `apps/frontend/package.json` (`@mypal/frontend` → `@mypal/frontend`)
- Modify: `apps/ui/package.json` (`@mypal/ui` → `@mypal/ui`)
- Modify: Root `package.json` (any `@mypal/*` references)
- Modify: `apps/frontend/index.html` (page title)
- Modify: `apps/frontend/src/` files (any "MyPal" branding strings)
- Modify: All `*.ts`, `*.tsx`, `*.json` files importing `@mypal/*`

**Step 1: Update package.json names**

In each `package.json`, replace `@mypal/` with `@mypal/`.

**Step 2: Update all TypeScript/TSX imports**

```bash
grep -rl "@mypal/" --include='*.ts' --include='*.tsx' --include='*.json' | xargs sed -i '' 's/@mypal\//@mypal\//g'
```

**Step 3: Update user-visible branding strings**

Search for "MyPal", "Open Lobster", "mypal" in:
- `apps/frontend/index.html` (page title)
- `apps/frontend/src/**/*.tsx` (any visible text)
- `apps/frontend/src/locales/` (i18n strings)

Replace with "MyPal" / "mypal" as appropriate.

**Step 4: Verify frontend builds**

```bash
pnpm install
pnpm build
```

Expected: no errors. Frontend assets compile to `apps/backend/cmd/mypal/public/` (or wherever turbo routes them).

**Step 5: Commit**

```bash
git add -A
git commit -m "Rebrand frontend from MyPal to MyPal"
```

---

### Task 6: Update Docker and Deployment Artifacts

**Files:**
- Modify: `.docker/Dockerfile.static` (image labels, binary paths)
- Modify: `docker-compose*.yml` (if present — service names, image names)
- Modify: Any CI/CD config (`.github/workflows/`, if present)

**Step 1: Update Dockerfile**

- Change binary references from `mypal` to `mypal`
- Update LABEL metadata if present
- Verify ENTRYPOINT/CMD uses the new binary name

**Step 2: Update docker-compose files**

- Service names: `mypal` → `mypal`
- Image names: `mypal:latest` → `mypal:latest`

**Step 3: Update CI/CD if present**

Search `.github/` for any workflow files referencing "mypal" and update.

**Step 4: Verify Docker build (if Docker available)**

```bash
docker build -f .docker/Dockerfile.static -t mypal:dev .
```

Expected: builds successfully. If Docker isn't available locally, skip — the build verification from Task 3 Step 5 covers the Go binary.

**Step 5: Commit**

```bash
git add -A
git commit -m "Update Docker and deployment artifacts for MyPal branding"
```

---

### Task 7: Clean Up Remaining References

**Files:**
- Modify: Any remaining files with "MyPal", "OpenClaw", "Neirth" references
- Modify: `README.md` (if present)
- Modify: `LICENSE` (if attribution needed)

**Step 1: Global search for stale references**

```bash
grep -ri "mypal\|openclaw\|neirth" --include='*.go' --include='*.ts' --include='*.tsx' --include='*.json' --include='*.yaml' --include='*.yml' --include='*.toml' -l
```

**Step 2: Evaluate and replace**

For each hit:
- Code references → rename to MyPal
- License/attribution → keep original attribution per GPLv3 requirements (note the fork origin)
- Historical references in comments → update or remove

**Step 3: Update or create README.md**

Minimal README with:
- Project name: MyPal
- License: GPLv3
- One-line description
- Note: "Forked from MyPal (Neirth/MyPal)"

**Step 4: Verify full build end-to-end**

```bash
pnpm install && pnpm build
```

The turbo pipeline should build frontend → embed into backend → compile Go binary.

**Step 5: Run existing tests**

```bash
cd apps/backend && go test ./...
```

And frontend:
```bash
cd apps/frontend && pnpm test
```

Expected: tests pass (or fail for reasons unrelated to our renaming — document any failures).

**Step 6: Commit**

```bash
git add -A
git commit -m "Clean up remaining MyPal/OpenClaw references"
```

---

### Task 8: Verify and Tag

**Step 1: Final grep for any missed references**

```bash
grep -ri "mypal" --include='*.go' --include='*.ts' --include='*.tsx' --include='*.json' --include='*.yaml' --include='*.yml' | grep -v "node_modules" | grep -v ".git"
```

Expected: zero or only intentional references (license attribution).

**Step 2: Test the full binary**

```bash
cd apps/backend && go build -o /tmp/mypal ./cmd/mypal
/tmp/mypal version
/tmp/mypal serve --help
```

**Step 3: Tag the fork point**

```bash
git tag v0.0.1-fork -m "MyPal: hard fork of MyPal master"
```

This marks the clean fork point before any feature work begins.

---

## Summary

| Task | Description | Key Risk |
|------|-------------|----------|
| 1 | Import MyPal source | Merge conflicts with existing docs |
| 2 | Rename Go module path | Broken imports across ~100+ Go files |
| 3 | Rename binary and CLI | Dockerfile and turbo pipeline breakage |
| 4 | Rename env var prefix | Config loading failures |
| 5 | Rebrand frontend packages | pnpm workspace resolution failures |
| 6 | Update Docker/deployment | Build pipeline breakage |
| 7 | Clean up stale references | Missing a reference that breaks at runtime |
| 8 | Verify and tag | Catch anything missed |

**After this plan:** Phase 1b (Personality engine + model tier routing) can begin on top of the clean fork.
