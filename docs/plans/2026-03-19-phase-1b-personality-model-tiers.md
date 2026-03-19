# Phase 1b: Personality Engine & Model Tier Routing

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add personality engine with per-user relationships, model tier routing, and organic response system to the MyPal platform.

**Architecture:** Follow OpenLobster's hexagonal architecture: domain models → ports → repositories → services → GraphQL resolvers → frontend views. New DB tables via GORM AutoMigrate. New GraphQL schema files. New SolidJS dashboard views.

**Tech Stack:** Go 1.25+, GORM, gqlgen, SolidJS + Vite, TanStack Solid Query

---

## Codebase Patterns Reference

**Adding a new feature end-to-end:**

1. Define domain model struct in `internal/domain/models/` with GORM tags
2. Add to `AutoMigrate()` in `internal/infrastructure/persistence/migrate.go`
3. Define port interface in `internal/domain/ports/repositories.go`
4. Implement repository in `internal/infrastructure/persistence/repositories/<name>/`
5. Create domain service in `internal/domain/services/<name>/`
6. Add GraphQL schema in `schema/<name>.graphql`
7. Run `go generate` to regenerate gqlgen types
8. Implement resolvers in `internal/application/graphql/resolvers/`
9. Wire dependencies in `resolvers/deps.go` and `cmd/mypal/serve/services.go`
10. Add frontend view in `apps/frontend/src/views/`

**Config pattern:** Struct in `config.go` → `mapstructure` tags → `setDefaults()` → `Validate()`

**AI provider:** `AIProviderPort` interface, factory in `ai/factory/factory.go`, single active provider per config.

**Message pipeline:** `MessageHandler.Handle()` → `contextInjector.BuildContext()` → `agenticRunner.runAgenticLoop()` → `aiProvider.Chat()`

---

### Task 1: Personality Domain Model + Migration

**Files:**
- Create: `apps/backend/internal/domain/models/personality.go`
- Modify: `apps/backend/internal/infrastructure/persistence/migrate.go`

**Step 1: Create the personality model**

```go
// apps/backend/internal/domain/models/personality.go
package models

import "time"

// PersonalityModel defines a personality configuration.
type PersonalityModel struct {
	ID          string            `gorm:"primaryKey;type:text" json:"id"`
	Name        string            `gorm:"type:text;not null" json:"name"`
	BasePrompt  string            `gorm:"type:text" json:"base_prompt"`
	Traits      string            `gorm:"type:text" json:"traits"`           // JSON array
	Tone        string            `gorm:"type:text" json:"tone"`
	Boundaries  string            `gorm:"type:text" json:"boundaries"`       // JSON array
	Quirks      string            `gorm:"type:text" json:"quirks"`           // JSON array
	Adaptations string            `gorm:"type:text" json:"adaptations"`      // JSON object: channel -> tweak
	IsDefault   bool              `gorm:"default:false" json:"is_default"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

func (PersonalityModel) TableName() string { return "personalities" }

// UserPersonaRelationshipModel tracks per-user familiarity with a personality.
type UserPersonaRelationshipModel struct {
	ID            string    `gorm:"primaryKey;type:text" json:"id"`
	UserID        string    `gorm:"type:text;not null;index:idx_upr_user" json:"user_id"`
	PersonalityID string    `gorm:"type:text;not null;index:idx_upr_personality" json:"personality_id"`
	Familiarity   float64   `gorm:"default:0.0" json:"familiarity"`       // 0.0-1.0
	Preferences   string    `gorm:"type:text" json:"preferences"`          // JSON object
	InteractionCount int64  `gorm:"default:0" json:"interaction_count"`
	LastInteraction  time.Time `json:"last_interaction"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (UserPersonaRelationshipModel) TableName() string { return "user_persona_relationships" }
```

**Step 2: Add to AutoMigrate**

In `migrate.go`, add `&domainmodels.PersonalityModel{}` and `&domainmodels.UserPersonaRelationshipModel{}` to the `AutoMigrate()` call.

**Step 3: Verify migration runs**

```bash
cd apps/backend && go build -o /tmp/mypal ./cmd/mypal
```

**Step 4: Commit**

```bash
git add apps/backend/internal/domain/models/personality.go apps/backend/internal/infrastructure/persistence/migrate.go
git commit -m "feat: add Personality and UserPersonaRelationship domain models"
```

---

### Task 2: Personality Repository

**Files:**
- Modify: `apps/backend/internal/domain/ports/repositories.go` (add port interface)
- Create: `apps/backend/internal/infrastructure/persistence/repositories/personality/personality_repository.go`

**Step 1: Define the port interface**

Add to `ports/repositories.go`:

```go
type PersonalityRepositoryPort interface {
	Create(ctx context.Context, p *models.PersonalityModel) error
	GetByID(ctx context.Context, id string) (*models.PersonalityModel, error)
	GetDefault(ctx context.Context) (*models.PersonalityModel, error)
	List(ctx context.Context) ([]models.PersonalityModel, error)
	Update(ctx context.Context, p *models.PersonalityModel) error
	Delete(ctx context.Context, id string) error
	SetDefault(ctx context.Context, id string) error
}

type UserPersonaRelationshipRepositoryPort interface {
	GetOrCreate(ctx context.Context, userID, personalityID string) (*models.UserPersonaRelationshipModel, error)
	Update(ctx context.Context, r *models.UserPersonaRelationshipModel) error
	GetByUser(ctx context.Context, userID string) ([]models.UserPersonaRelationshipModel, error)
	IncrementInteraction(ctx context.Context, userID, personalityID string) error
}
```

**Step 2: Implement the personality repository**

Create `repositories/personality/personality_repository.go`. Follow the pattern from message or task repositories:
- Constructor takes `*gorm.DB`
- Methods use `db.WithContext(ctx)` for all queries
- Return domain models directly (GORM models ARE the domain models in this codebase)

**Step 3: Implement the relationship repository**

Create `repositories/personality/relationship_repository.go`.
- `GetOrCreate`: Find by (userID, personalityID) or create with defaults (familiarity=0.0)
- `IncrementInteraction`: Increment count, update last_interaction, bump familiarity using formula: `min(1.0, familiarity + 0.01 * (1.0 - familiarity))`

**Step 4: Write tests**

Create `repositories/personality/personality_repository_test.go` with tests for CRUD operations using SQLite in-memory DB.

**Step 5: Verify**

```bash
cd apps/backend && go test ./internal/infrastructure/persistence/repositories/personality/...
```

**Step 6: Commit**

```bash
git commit -m "feat: add Personality and UserPersonaRelationship repositories"
```

---

### Task 3: Personality Service

**Files:**
- Create: `apps/backend/internal/domain/services/personality/personality_service.go`

**Step 1: Create the service**

```go
type Service struct {
	personalityRepo ports.PersonalityRepositoryPort
	relationshipRepo ports.UserPersonaRelationshipRepositoryPort
}

func NewService(pRepo ports.PersonalityRepositoryPort, rRepo ports.UserPersonaRelationshipRepositoryPort) *Service

// CRUD for personalities
func (s *Service) Create(ctx context.Context, p *models.PersonalityModel) error
func (s *Service) Get(ctx context.Context, id string) (*models.PersonalityModel, error)
func (s *Service) List(ctx context.Context) ([]models.PersonalityModel, error)
func (s *Service) Update(ctx context.Context, p *models.PersonalityModel) error
func (s *Service) Delete(ctx context.Context, id string) error
func (s *Service) SetDefault(ctx context.Context, id string) error

// Relationship management
func (s *Service) GetRelationship(ctx context.Context, userID, personalityID string) (*models.UserPersonaRelationshipModel, error)
func (s *Service) GetUserRelationships(ctx context.Context, userID string) ([]models.UserPersonaRelationshipModel, error)
func (s *Service) RecordInteraction(ctx context.Context, userID, personalityID string) error

// Context building: returns the system prompt with personality + familiarity adjustments
func (s *Service) BuildPersonalityPrompt(ctx context.Context, personalityID, userID, channelType string) (string, error)
```

`BuildPersonalityPrompt` is the key method:
1. Load personality config
2. Load user relationship (familiarity score)
3. Build system prompt from base_prompt + traits + tone + boundaries + quirks
4. Apply channel-specific adaptations (look up channelType in Adaptations JSON)
5. Adjust formality/humor/initiative based on familiarity score
6. Return the assembled prompt string

**Step 2: Write tests**

Test `BuildPersonalityPrompt` with different familiarity levels and channel types. Use mock repositories.

**Step 3: Commit**

```bash
git commit -m "feat: add Personality service with prompt building"
```

---

### Task 4: Personality GraphQL Schema + Resolvers

**Files:**
- Create: `schema/personality.graphql`
- Modify: `schema/root.graphql` (extend Query/Mutation)
- Run: `go generate` to regenerate gqlgen types
- Modify: `apps/backend/internal/application/graphql/resolvers/` (new resolver file)
- Modify: `apps/backend/internal/application/graphql/resolvers/deps.go` (wire service)

**Step 1: Create the GraphQL schema**

```graphql
# schema/personality.graphql

type Personality {
  id: String!
  name: String!
  basePrompt: String!
  traits: [String!]!
  tone: String!
  boundaries: [String!]!
  quirks: [String!]!
  adaptations: String  # JSON string of channel -> tweak
  isDefault: Boolean!
  createdAt: String!
  updatedAt: String!
}

type UserRelationship {
  userId: String!
  personalityId: String!
  familiarity: Float!
  preferences: String  # JSON
  interactionCount: Int!
  lastInteraction: String
}

input PersonalityInput {
  name: String!
  basePrompt: String!
  traits: [String!]
  tone: String
  boundaries: [String!]
  quirks: [String!]
  adaptations: String
  isDefault: Boolean
}

extend type Query {
  personalities: [Personality!]!
  personality(id: String!): Personality
  userRelationships(userId: String!): [UserRelationship!]!
}

extend type Mutation {
  createPersonality(input: PersonalityInput!): Personality!
  updatePersonality(id: String!, input: PersonalityInput!): Personality!
  deletePersonality(id: String!): Boolean!
  setDefaultPersonality(id: String!): Boolean!
}
```

**Step 2: Regenerate gqlgen types**

```bash
cd apps/backend && go generate ./...
```

If gqlgen isn't configured to pick up the new schema file, update `gqlgen.yml` to include it.

**Step 3: Implement resolvers**

Create `resolvers/personality_resolver.go`. Follow the pattern from `tasks_resolver.go`:
- Thin resolvers that delegate to Deps
- Mappers to convert between domain models and generated types

**Step 4: Wire in Deps**

Add personality service methods to `deps.go`:
- `Personalities(ctx) -> []generated.Personality`
- `CreatePersonality(ctx, input) -> generated.Personality`
- etc.

**Step 5: Wire in services.go**

In `cmd/mypal/serve/services.go`:
- Create personality repositories
- Create personality service
- Pass to GraphQL deps

**Step 6: Verify**

```bash
cd apps/backend && go build -o /tmp/mypal ./cmd/mypal
```

**Step 7: Commit**

```bash
git commit -m "feat: add Personality GraphQL API"
```

---

### Task 5: Seed Default Personality

**Files:**
- Modify: `apps/backend/cmd/mypal/serve/services.go` or appropriate startup location

**Step 1: Create a seed function**

On startup, check if any personality exists. If not, create a default "MyPal" personality:

```go
func seedDefaultPersonality(ctx context.Context, repo ports.PersonalityRepositoryPort) {
    existing, _ := repo.GetDefault(ctx)
    if existing != nil {
        return
    }
    repo.Create(ctx, &models.PersonalityModel{
        ID:         uuid.New().String(),
        Name:       "MyPal",
        BasePrompt: "You are MyPal, a friendly and capable personal AI assistant. You're warm, thoughtful, and genuinely interested in helping.",
        Traits:     `["helpful","curious","warm","thoughtful"]`,
        Tone:       "friendly",
        Boundaries: `["no medical advice","no legal advice","no financial advice"]`,
        Quirks:     `[]`,
        Adaptations: `{"sms":"Be brief and direct","email":"Use proper greeting and sign-off","discord":"Be casual and expressive"}`,
        IsDefault:  true,
    })
}
```

**Step 2: Call from startup**

Add to the service initialization in `services.go`.

**Step 3: Commit**

```bash
git commit -m "feat: seed default MyPal personality on first boot"
```

---

### Task 6: Integrate Personality into Message Pipeline

**Files:**
- Modify: `apps/backend/internal/domain/handlers/message_handler.go`
- Modify: `apps/backend/internal/domain/services/context/context_injector.go` (or equivalent)

**Step 1: Add personality service to MessageHandler**

Add `personalitySvc *personality.Service` to `MessageHandler` struct and constructor.

**Step 2: Inject personality prompt into context**

In `Handle()`, before the agentic loop:
1. Determine which personality to use (default, or user-assigned)
2. Call `personalitySvc.BuildPersonalityPrompt(ctx, personalityID, userID, channelType)`
3. Prepend the personality prompt to the system prompt (or replace it if the personality's base_prompt is meant to be the full system prompt)
4. Call `personalitySvc.RecordInteraction(ctx, userID, personalityID)` to update familiarity

**Step 3: Wire in services.go**

Pass personality service to MessageHandler constructor.

**Step 4: Test end-to-end**

Build and verify the binary compiles. Test with a manual message if possible.

**Step 5: Commit**

```bash
git commit -m "feat: integrate personality prompt into message pipeline"
```

---

### Task 7: Model Tier Configuration

**Files:**
- Modify: `apps/backend/internal/infrastructure/config/config.go`

**Step 1: Add tier config structs**

```go
type ModelTierConfig struct {
	Name     string  `mapstructure:"name"`     // "high", "mid", "low"
	Provider string  `mapstructure:"provider"` // "anthropic", "openai", "ollama"
	Model    string  `mapstructure:"model"`    // "claude-opus-4", "claude-sonnet-4", etc.
	Prefix   string  `mapstructure:"prefix"`   // "!high", "!mid", "!low"
	CostCap  float64 `mapstructure:"cost_cap"` // optional daily cost limit
	Default  bool    `mapstructure:"default"`  // which tier is default
}

type ModelTiersConfig struct {
	Enabled bool             `mapstructure:"enabled"`
	Tiers   []ModelTierConfig `mapstructure:"tiers"`
}
```

**Step 2: Add to main Config struct**

```go
type Config struct {
    // ... existing fields
    ModelTiers ModelTiersConfig `mapstructure:"model_tiers"`
}
```

**Step 3: Set defaults**

```go
viper.SetDefault("model_tiers.enabled", false)
```

When disabled, the system uses the single provider from the existing config (backwards compatible).

**Step 4: Validate**

When enabled, at least one tier must be marked default, and each tier's provider must have valid credentials in the providers config.

**Step 5: Commit**

```bash
git commit -m "feat: add model tier configuration"
```

---

### Task 8: Model Tier Router

**Files:**
- Create: `apps/backend/internal/domain/services/modeltier/router.go`
- Modify: `apps/backend/internal/infrastructure/adapters/ai/factory/factory.go`

**Step 1: Create the tier router**

```go
type Router struct {
	tiers    map[string]ports.AIProviderPort  // "high" -> provider, "mid" -> provider, etc.
	prefixes map[string]string                 // "!high" -> "high", "!mid" -> "mid"
	defaultTier string
}

func NewRouter(cfg config.ModelTiersConfig, providerFactory func(provider, model string) ports.AIProviderPort) *Router

// Route parses the message for a tier prefix, strips it, and returns the provider + cleaned message
func (r *Router) Route(message string) (provider ports.AIProviderPort, cleanedMessage string)
```

**Step 2: Extend the AI factory**

Add `BuildProvider(providerName, model string) ports.AIProviderPort` that creates a provider for a specific provider+model combo (used by the tier router to build one provider per tier).

**Step 3: Integrate with MessageHandler**

In `Handle()`, before sending to the agentic loop:
1. If model tiers enabled, call `tierRouter.Route(message)` to get the right provider
2. Pass that provider to `runAgenticLoop()` instead of the default

The agentic runner already accepts an `aiProvider` — pass the tier-selected one.

**Step 4: Wire in services.go**

If `cfg.ModelTiers.Enabled`, build the Router and inject it into MessageHandler.

**Step 5: Test**

Write unit tests for the router:
- Message with "!high" prefix → returns high-tier provider + stripped message
- Message with no prefix → returns default-tier provider + original message
- Message with "!low" prefix → returns low-tier provider + stripped message

**Step 6: Commit**

```bash
git commit -m "feat: add model tier router with prefix-based routing"
```

---

### Task 9: Model Tier GraphQL Schema + Resolvers

**Files:**
- Create: `schema/modeltiers.graphql`
- Modify: resolvers and deps

**Step 1: Create schema**

```graphql
# schema/modeltiers.graphql

type ModelTier {
  name: String!
  provider: String!
  model: String!
  prefix: String!
  costCap: Float
  isDefault: Boolean!
}

type ModelTiersConfig {
  enabled: Boolean!
  tiers: [ModelTier!]!
}

extend type Query {
  modelTiers: ModelTiersConfig!
}
```

Note: Model tier configuration is read from config — mutations would need to modify the encrypted config file. For now, expose as read-only via GraphQL. Configuration happens via YAML/env vars.

**Step 2: Regenerate, implement resolver, wire deps**

Follow same pattern as Task 4.

**Step 3: Commit**

```bash
git commit -m "feat: add ModelTiers GraphQL query"
```

---

### Task 10: Organic Response System

**Files:**
- Create: `apps/backend/internal/domain/models/organic_response.go`
- Create: `apps/backend/internal/domain/services/organic/organic_service.go`
- Modify: `apps/backend/internal/domain/handlers/message_handler.go`

**Step 1: Create the config model**

```go
// Per-channel organic response configuration (stored in DB)
type OrganicResponseConfigModel struct {
	ID                 string        `gorm:"primaryKey;type:text"`
	ChannelID          string        `gorm:"type:text;not null;uniqueIndex"`
	Enabled            bool          `gorm:"default:false"`
	CooldownSeconds    int           `gorm:"default:300"`           // 5 minutes
	RelevanceThreshold float64       `gorm:"default:0.7"`
	MaxDailyOrganic    int           `gorm:"default:20"`
	AllowReactions     bool          `gorm:"default:false"`
	ThreadPolicy       string        `gorm:"type:text;default:'joined_only'"`
	QuietHoursStart    string        `gorm:"type:text"`             // "22:00"
	QuietHoursEnd      string        `gorm:"type:text"`             // "08:00"
	CreatedAt          time.Time
	UpdatedAt          time.Time
}
```

**Step 2: Create the organic response service**

```go
type Service struct {
	configRepo   OrganicConfigRepositoryPort
	aiProvider   ports.AIProviderPort
	lastResponse map[string]time.Time  // channelID -> last organic response time (in-memory)
	dailyCount   map[string]int         // channelID -> count today
	mu           sync.RWMutex
}

// ShouldRespond evaluates whether the bot should organically respond to a group message
func (s *Service) ShouldRespond(ctx context.Context, input OrganicInput) (bool, error)

type OrganicInput struct {
	ChannelID   string
	Message     string
	SenderName  string
	IsGroup     bool
	IsMentioned bool
	ThreadID    string
	IsInThread  bool
}
```

`ShouldRespond` logic:
1. If not a group message, return false (organic only in groups)
2. If mentioned, return true (direct mention bypasses organic checks)
3. Load channel config; if not enabled, return false
4. Check quiet hours
5. Check cooldown (time since last organic response in this channel)
6. Check daily limit
7. Use LLM to evaluate relevance (short prompt asking "should you respond?" with the message context, return yes/no + confidence score)
8. Compare confidence to threshold
9. Return decision

**Step 3: Integrate into MessageHandler**

In `Handle()`, for group messages where `!isMentioned`:
1. Call `organicSvc.ShouldRespond(ctx, input)`
2. If false, return early (don't process)
3. If true, continue with normal message processing

**Step 4: Add OrganicResponseConfig to AutoMigrate**

**Step 5: Add GraphQL schema for organic config CRUD**

```graphql
# schema/organic.graphql
type OrganicResponseConfig {
  channelId: String!
  enabled: Boolean!
  cooldownSeconds: Int!
  relevanceThreshold: Float!
  maxDailyOrganic: Int!
  allowReactions: Boolean!
  threadPolicy: String!
  quietHoursStart: String
  quietHoursEnd: String
}

input OrganicResponseConfigInput {
  enabled: Boolean
  cooldownSeconds: Int
  relevanceThreshold: Float
  maxDailyOrganic: Int
  allowReactions: Boolean
  threadPolicy: String
  quietHoursStart: String
  quietHoursEnd: String
}

extend type Query {
  organicConfig(channelId: String!): OrganicResponseConfig
}

extend type Mutation {
  updateOrganicConfig(channelId: String!, input: OrganicResponseConfigInput!): OrganicResponseConfig!
}
```

**Step 6: Wire everything, commit**

```bash
git commit -m "feat: add organic response system for group chats"
```

---

### Task 11: Frontend — Personality Settings View

**Files:**
- Create: `apps/frontend/src/views/PersonalityView/PersonalityView.tsx`
- Create: `apps/frontend/src/views/PersonalityView/PersonalityView.css`
- Modify: `apps/frontend/src/App.tsx` (add route)
- Create: GraphQL queries/mutations in `packages/ui/src/graphql/`

**Step 1: Create GraphQL operations**

Add personality queries and mutations to the UI package's GraphQL definitions.

**Step 2: Create the view**

Follow the pattern from SettingsView:
- List all personalities with CRUD actions
- Form for creating/editing a personality (name, base prompt, traits, tone, boundaries, quirks, channel adaptations)
- Per-user relationship viewer (table showing users, familiarity scores, interaction counts)
- "Set as default" button

**Step 3: Add routing**

Add the new view to the app router.

**Step 4: Verify frontend builds**

```bash
pnpm install && pnpm build
```

**Step 5: Commit**

```bash
git commit -m "feat: add Personality settings view to dashboard"
```

---

### Task 12: Frontend — Model Tiers Settings View

**Files:**
- Create: `apps/frontend/src/views/ModelTiersView/ModelTiersView.tsx`
- Create: `apps/frontend/src/views/ModelTiersView/ModelTiersView.css`
- Modify: `apps/frontend/src/App.tsx` (add route)

**Step 1: Create the view**

Read-only display of current model tier configuration:
- Table showing tier name, provider, model, prefix, cost cap, default flag
- Note indicating config is managed via YAML/env vars (not editable in dashboard yet)

**Step 2: Add routing and verify build**

**Step 3: Commit**

```bash
git commit -m "feat: add Model Tiers settings view to dashboard"
```

---

## Summary

| Task | Description | Dependencies |
|------|-------------|-------------|
| 1 | Personality domain models + migration | None |
| 2 | Personality repositories | Task 1 |
| 3 | Personality service | Task 2 |
| 4 | Personality GraphQL schema + resolvers | Task 3 |
| 5 | Seed default personality | Task 2 |
| 6 | Integrate personality into message pipeline | Task 3 |
| 7 | Model tier configuration | None |
| 8 | Model tier router | Task 7 |
| 9 | Model tier GraphQL schema | Task 8 |
| 10 | Organic response system | Task 3 |
| 11 | Frontend: Personality settings | Task 4 |
| 12 | Frontend: Model tiers settings | Task 9 |

**After this plan:** Phase 2 (proactive engine + email channel) can begin.
