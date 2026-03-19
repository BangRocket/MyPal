# Phase 2: Proactive Engine & Email Channel

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add a bot-modifiable heartbeat system for proactive engagement and an email channel (IMAP/SMTP) as a first-class messaging adapter.

**Architecture:** Heartbeat extends the existing Task/Scheduler system with richer metadata (priority, target user/channel, bot-modifiable). Email implements MessagingPort using IMAP polling for inbound and SMTP for outbound, following the same adapter pattern as Telegram/Discord/Slack.

**Tech Stack:** Go, GORM, gqlgen, go-imap (IMAP client), go-message + net/smtp (outbound), SolidJS

---

## Task 1: Heartbeat Domain Model + Migration

**Files:**
- Create: `apps/backend/internal/domain/models/heartbeat.go`
- Modify: `apps/backend/internal/infrastructure/persistence/migrate.go`

Create the HeartbeatItem and HeartbeatLog models:

```go
type HeartbeatItemModel struct {
    ID            string    `gorm:"primaryKey;column:id;type:text"`
    Title         string    `gorm:"column:title;type:text;not null"`
    Description   string    `gorm:"column:description;type:text"`
    Schedule      string    `gorm:"column:schedule;type:text"`          // cron or ISO 8601
    Priority      int       `gorm:"column:priority;default:3"`          // 1=critical, 5=low
    Status        string    `gorm:"column:status;type:text;default:'active'"` // active, completed, snoozed, cancelled
    CreatedBy     string    `gorm:"column:created_by;type:text"`        // "user:<id>", "system", "bot"
    TargetUser    string    `gorm:"column:target_user;type:text"`       // optional
    TargetChannel string    `gorm:"column:target_channel;type:text"`    // optional
    Context       string    `gorm:"column:context;type:text"`           // additional context
    LastRun       time.Time `gorm:"column:last_run"`
    NextRun       time.Time `gorm:"column:next_run;index:idx_hb_next_run"`
    CreatedAt     time.Time `gorm:"column:created_at;autoCreateTime:false"`
    UpdatedAt     time.Time `gorm:"column:updated_at;autoUpdateTime:false"`
}

type HeartbeatLogModel struct {
    ID              string    `gorm:"primaryKey;column:id;type:text"`
    HeartbeatItemID string    `gorm:"column:heartbeat_item_id;type:text;not null;index:idx_hbl_item"`
    Action          string    `gorm:"column:action;type:text;not null"` // executed, snoozed, modified, created
    Reason          string    `gorm:"column:reason;type:text"`
    Result          string    `gorm:"column:result;type:text"`
    Timestamp       time.Time `gorm:"column:timestamp;autoCreateTime:false"`
}
```

Add both to AutoMigrate. Verify build. Commit.

---

## Task 2: Heartbeat Repository

**Files:**
- Modify: `apps/backend/internal/domain/ports/repositories.go`
- Create: `apps/backend/internal/domain/repositories/heartbeat/heartbeat_repository.go`

Port interface:
```go
type HeartbeatRepositoryPort interface {
    Create(ctx context.Context, item *models.HeartbeatItemModel) error
    GetByID(ctx context.Context, id string) (*models.HeartbeatItemModel, error)
    ListActive(ctx context.Context) ([]models.HeartbeatItemModel, error)
    ListDue(ctx context.Context, now time.Time) ([]models.HeartbeatItemModel, error) // next_run <= now AND status = 'active'
    Update(ctx context.Context, item *models.HeartbeatItemModel) error
    Delete(ctx context.Context, id string) error
    AddLog(ctx context.Context, log *models.HeartbeatLogModel) error
    GetLogs(ctx context.Context, itemID string, limit int) ([]models.HeartbeatLogModel, error)
}
```

Follow existing repository patterns. Verify build. Commit.

---

## Task 3: Heartbeat Service

**Files:**
- Create: `apps/backend/internal/domain/services/heartbeat/heartbeat_service.go`

The service manages heartbeat items and the evaluation loop:

```go
type Service struct {
    repo       ports.HeartbeatRepositoryPort
    dispatcher ports.TaskDispatcherPort  // reuse the existing loopback dispatcher
    ticker     *time.Ticker
    stopCh     chan struct{}
}

func NewService(repo ports.HeartbeatRepositoryPort, dispatcher ports.TaskDispatcherPort) *Service
```

Methods:
- **CRUD**: Create, Get, List, Update, Delete, Snooze, Complete, Cancel
- **Bot self-modification**: `BotCreate`, `BotModify`, `BotComplete` — same as CRUD but set CreatedBy="bot" and log the action with a reason
- **Evaluation loop**: `Run(ctx context.Context)` — ticker fires every N minutes (configurable, default 15):
  1. Load all due items (`ListDue(now)`)
  2. Sort by priority
  3. For each item: build a dispatch prompt that includes the heartbeat context, target user, and instructions
  4. Call `dispatcher.Dispatch(ctx, prompt)`
  5. Update last_run, compute next_run (reuse scheduler's cron parsing)
  6. Log the execution
- **Notify**: `Notify()` — signal to re-evaluate (for when items are added/modified)

The dispatch prompt for a heartbeat item should look like:
```
[Heartbeat Task] Title: {title}
Description: {description}
Target user: {targetUser}
Target channel: {targetChannel}
Context: {context}
Priority: {priority}

Execute this heartbeat task. If it involves sending a message to a user, compose and send it via the appropriate channel.
```

Verify build. Commit.

---

## Task 4: Heartbeat GraphQL Schema + Resolvers

**Files:**
- Create: `schema/heartbeat.graphql`
- Regenerate gqlgen
- Create resolver, wire deps, wire services.go

Schema:
```graphql
type HeartbeatItem {
  id: String!
  title: String!
  description: String
  schedule: String
  priority: Int!
  status: String!
  createdBy: String!
  targetUser: String
  targetChannel: String
  context: String
  lastRun: String
  nextRun: String
  createdAt: String!
  updatedAt: String!
}

type HeartbeatLog {
  id: String!
  heartbeatItemId: String!
  action: String!
  reason: String
  result: String
  timestamp: String!
}

input HeartbeatItemInput {
  title: String!
  description: String
  schedule: String
  priority: Int
  targetUser: String
  targetChannel: String
  context: String
}

extend type Query {
  heartbeatItems: [HeartbeatItem!]!
  heartbeatItem(id: String!): HeartbeatItem
  heartbeatLogs(itemId: String!, limit: Int): [HeartbeatLog!]!
}

extend type Mutation {
  createHeartbeatItem(input: HeartbeatItemInput!): HeartbeatItem!
  updateHeartbeatItem(id: String!, input: HeartbeatItemInput!): HeartbeatItem!
  deleteHeartbeatItem(id: String!): Boolean!
  snoozeHeartbeatItem(id: String!, until: String!): HeartbeatItem!
  completeHeartbeatItem(id: String!): Boolean!
}
```

Wire the heartbeat service into GraphQL deps and start the evaluation loop in lifecycle.go. Verify build. Commit.

---

## Task 5: Heartbeat Bot Tools

**Files:**
- Create: `apps/backend/internal/infrastructure/adapters/tools/heartbeat_tools.go`
- Modify: tool registration

Create LLM-callable tools so the bot can self-modify the heartbeat:

- `heartbeat_create`: Create a new heartbeat item (title, description, schedule, priority, target_user, target_channel, context)
- `heartbeat_list`: List active heartbeat items
- `heartbeat_complete`: Mark an item as completed
- `heartbeat_snooze`: Snooze an item until a given time
- `heartbeat_cancel`: Cancel an item

Follow the existing tool registration pattern. Each tool is a function definition with a JSON schema for parameters. The tool handler calls the heartbeat service.

Register these tools in the tool registry during startup. Verify build. Commit.

---

## Task 6: Email Channel Config

**Files:**
- Modify: `apps/backend/internal/infrastructure/config/config.go`

Add email config:
```go
type EmailConfig struct {
    Enabled        bool   `mapstructure:"enabled"`
    IMAPHost       string `mapstructure:"imap_host"`
    IMAPPort       int    `mapstructure:"imap_port"`
    IMAPUser       string `mapstructure:"imap_user"`
    IMAPPass       string `mapstructure:"imap_pass"`
    IMAPTLS        bool   `mapstructure:"imap_tls"`
    SMTPHost       string `mapstructure:"smtp_host"`
    SMTPPort       int    `mapstructure:"smtp_port"`
    SMTPUser       string `mapstructure:"smtp_user"`
    SMTPPass       string `mapstructure:"smtp_pass"`
    SMTPFrom       string `mapstructure:"smtp_from"`
    SMTPTLS        bool   `mapstructure:"smtp_tls"`
    PollInterval   int    `mapstructure:"poll_interval"`    // seconds, default 60
    ProcessedLabel string `mapstructure:"processed_label"`  // IMAP label for processed emails
    Filters        string `mapstructure:"filters"`           // JSON array of filter rules
}
```

Add to ChannelsConfig. Set defaults (poll_interval=60, imap_port=993, smtp_port=587, tls=true). Add validation. Verify build. Commit.

---

## Task 7: Email Adapter (IMAP + SMTP)

**Files:**
- Create: `apps/backend/internal/infrastructure/adapters/messaging/email/adapter.go`
- Modify: `apps/backend/go.mod` (add go-imap dependency)

This is the largest task. The email adapter implements `MessagingPort`:

**Constructor**: `NewAdapter(cfg config.EmailConfig) (*Adapter, error)`

**Start()** (IMAP polling loop):
1. Connect to IMAP server
2. Select INBOX
3. Poll loop (every PollInterval seconds):
   - Search for UNSEEN messages
   - Fetch each: extract From, Subject, Body (text/plain or text/html → strip to plain), In-Reply-To, Message-ID, attachments
   - Convert to `models.Message` with ChannelID = sender email, metadata includes thread headers
   - Call `onMessage(ctx, msg)`
   - Mark as SEEN, optionally move to processed label
4. Respect ctx.Done() for shutdown

**SendMessage()** (SMTP outbound):
1. Build email: To = msg.ChannelID (recipient email), Subject from metadata or generate, Body = msg.Content
2. Thread awareness: set In-Reply-To and References headers from conversation metadata
3. Send via net/smtp with TLS

**Other methods**:
- `SendMedia()`: Send email with attachment
- `SendTyping()`: No-op (email has no typing indicator)
- `HandleWebhook()`: Return nil (we use IMAP polling, not webhooks)
- `GetUserInfo()`: Return email address as display name
- `React()`: No-op
- `GetCapabilities()`: Return capabilities (no voice, no streaming, yes media)
- `ConvertAudioForPlatform()`: No-op

**Dependencies**: Use `github.com/emersion/go-imap/v2` and `github.com/emersion/go-message` for IMAP. Use stdlib `net/smtp` for SMTP.

Verify build. Commit.

---

## Task 8: Email Channel Registration

**Files:**
- Modify: `apps/backend/cmd/mypal/serve/channels.go`
- Modify: `apps/backend/cmd/mypal/serve/lifecycle.go`

Register the email adapter in `initChannels()`:
```go
if cfg.Channels.Email.Enabled && !isPlaceholder(cfg.Channels.Email.IMAPUser) {
    adapter, err := email.NewAdapter(cfg.Channels.Email)
    if err == nil {
        a.MessagingAdapters = append(a.MessagingAdapters, adapter)
    }
}
```

Register in channel registry: `a.ChanReg.Set("email", adapter)`

Add to `startAndWait()` listener loop for email (it's poll-based like Telegram).

Add hot-reload support in `reloadChannel()`.

Verify build. Commit.

---

## Task 9: Email GraphQL Schema

**Files:**
- Create: `schema/email.graphql` (or add to `schema/config.graphql`)

Expose email channel config for the dashboard:

```graphql
type EmailChannelConfig {
  enabled: Boolean!
  imapHost: String!
  imapPort: Int!
  imapUser: String!
  smtpHost: String!
  smtpPort: Int!
  smtpFrom: String!
  pollInterval: Int!
}

extend type Query {
  emailConfig: EmailChannelConfig!
}
```

Read-only for now (config managed via YAML). Regenerate, implement resolver, wire deps. Verify build. Commit.

---

## Task 10: Frontend — Heartbeat Dashboard View

**Files:**
- Create: `apps/frontend/src/views/HeartbeatView/HeartbeatView.tsx`
- Create: `apps/frontend/src/views/HeartbeatView/HeartbeatView.css`
- Modify: `apps/frontend/src/App.tsx` (routing)
- Add GraphQL operations to `packages/ui/`

The heartbeat view shows:
- List of heartbeat items (sortable by priority, status, next_run)
- Create/edit form (title, description, schedule, priority, target user, target channel, context)
- Status badges (active, snoozed, completed, cancelled)
- Actions: snooze, complete, delete
- Execution log viewer (expandable per item, shows recent logs with timestamps and reasons)
- "Bot-created" badge for items created by the bot

Add routing and nav link. Verify frontend builds. Commit.

---

## Task 11: Frontend — Email Channel Settings

**Files:**
- Modify existing settings view or create new email config section
- Add GraphQL query for email config

Simple read-only display of email channel configuration:
- IMAP/SMTP server details
- Connection status indicator
- Poll interval
- Note about config being managed via YAML

Add to settings navigation. Verify frontend builds. Commit.

---

## Summary

| Task | Description | Dependencies |
|------|-------------|-------------|
| 1 | Heartbeat domain models + migration | None |
| 2 | Heartbeat repository | Task 1 |
| 3 | Heartbeat service + evaluation loop | Task 2 |
| 4 | Heartbeat GraphQL + resolvers | Task 3 |
| 5 | Heartbeat bot tools (LLM-callable) | Task 3 |
| 6 | Email channel config | None |
| 7 | Email adapter (IMAP/SMTP) | Task 6 |
| 8 | Email channel registration | Task 7 |
| 9 | Email GraphQL schema | Task 8 |
| 10 | Frontend: Heartbeat view | Task 4 |
| 11 | Frontend: Email settings | Task 9 |
