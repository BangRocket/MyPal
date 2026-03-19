package serve

import (
	"context"
	"log"
	"path/filepath"

	"github.com/BangRocket/MyPal/apps/backend/internal/application/graphql/subscriptions"
	appcontext "github.com/BangRocket/MyPal/apps/backend/internal/domain/context"
	domainhandlers "github.com/BangRocket/MyPal/apps/backend/internal/domain/handlers"
	"github.com/BangRocket/MyPal/apps/backend/internal/domain/events"
	"github.com/BangRocket/MyPal/apps/backend/internal/domain/repositories"
	domainservices "github.com/BangRocket/MyPal/apps/backend/internal/domain/services"
	"github.com/BangRocket/MyPal/apps/backend/internal/domain/services/mcp"
	"github.com/BangRocket/MyPal/apps/backend/internal/domain/services/permissions"
	inframc "github.com/BangRocket/MyPal/apps/backend/internal/infrastructure/adapters/mcp"
	browser "github.com/BangRocket/MyPal/apps/backend/internal/infrastructure/adapters/browser/chromedp"
	"github.com/BangRocket/MyPal/apps/backend/internal/infrastructure/adapters/filesystem"
	aifactory "github.com/BangRocket/MyPal/apps/backend/internal/infrastructure/adapters/ai/factory"
	memfile "github.com/BangRocket/MyPal/apps/backend/internal/infrastructure/adapters/memory/file"
	memneo4j "github.com/BangRocket/MyPal/apps/backend/internal/infrastructure/adapters/memory/neo4j"
	"github.com/BangRocket/MyPal/apps/backend/internal/infrastructure/adapters/terminal"

	"github.com/BangRocket/MyPal/apps/backend/internal/domain/models"
	personalitysvc "github.com/BangRocket/MyPal/apps/backend/internal/domain/services/personality"
)

// initServices initialises the AI provider, memory backend, event bus,
// tool registry, message handler and all supporting domain services.
func (a *App) initServices() {
	cfg := a.Cfg

	// AI provider
	a.AIProvider = aifactory.BuildFromConfig(cfg)
	if a.AIProvider == nil {
		log.Println("warn: no AI provider configured — agent will not respond to messages")
	} else {
		log.Printf("ai provider: %s", aifactory.ProviderName(cfg))
	}

	// Memory backend
	switch cfg.Memory.Backend {
	case "neo4j":
		neo4jAdapter, err := memneo4j.NewNeo4jMemoryBackend(
			cfg.Memory.Neo4j.URI,
			cfg.Memory.Neo4j.User,
			cfg.Memory.Neo4j.Password,
		)
		if err != nil {
			log.Fatalf("failed to connect to neo4j memory backend: %v", err)
		}
		a.MemoryAdapter = neo4jAdapter
		log.Println("memory backend: neo4j")
	default:
		gmlBackend := memfile.NewGMLBackend(cfg.Memory.File.Path)
		if err := gmlBackend.Load(); err != nil {
			log.Fatalf("failed to load file memory backend from %s: %v", cfg.Memory.File.Path, err)
		}
		a.MemoryAdapter = gmlBackend
		log.Printf("memory backend: file (%s)", cfg.Memory.File.Path)
	}

	// Event bus + subscription manager
	eventBus := domainservices.NewEventBus()
	a.EventBus = eventBus
	a.SubManager = subscriptions.NewSubscriptionManager(eventBus)

	broadcastToSubs := func(ctx context.Context, e events.Event) error {
		a.SubManager.Broadcast(e)
		return nil
	}
	for _, et := range []string{
		events.EventMessageReceived, events.EventMessageSent, events.EventMessageProcessed,
		events.EventSessionStarted, events.EventSessionEnded,
		events.EventUserPaired, events.EventUserUnpaired,
		events.EventPairingRequested, events.EventPairingApproved, events.EventPairingDenied,
		events.EventTaskAdded, events.EventTaskCompleted, events.EventCronJobExecuted,
		events.EventMCPServerConnected, events.EventMCPServerDisconnected,
		events.EventMemoryUpdated, events.EventCompactionTriggered, events.EventCompactionCompleted,
	} {
		eventBus.Subscribe(et, broadcastToSubs)
	}

	// Pairing service
	a.PairingService = domainservices.NewPairingService(a.PairingRepo)

	// Permission manager (loaded from config + DB below)
	a.PermManager = permissions.Default()
	a.loadPermissions(a.PermManager)

	// Tool registry
	a.ToolRegistry = mcp.NewToolRegistry(true, a.PermManager)

	// Skills adapter
	a.SkillsAdapter = filesystem.NewSkillsAdapter(cfg.Workspace.Path)
	log.Printf("skills: reading from %s/skills", cfg.Workspace.Path)

	// Sub-agent & compaction services
	a.SubAgentSvc = domainservices.NewSubAgentService(
		a.AIProvider,
		cfg.SubAgents.MaxConcurrent,
		cfg.SubAgents.DefaultTimeout,
	)
	a.CompactionSvc = domainservices.NewMessageCompactionService(a.MessageRepo, a.AIProvider)

	// Register all internal tools
	mcp.RegisterAllInternalTools(a.ToolRegistry, mcp.InternalTools{
		Messaging:           &inframc.MessagingAdapter{Port: a.MsgRouter},
		MessageLog:          &inframc.OutboundMessageLogAdapter{MessageRepo: a.MessageRepo, SessionRepo: a.SessionRepo, UserChannelRepo: a.UserChannelRepo},
		LastChannelResolver: a.UserChannelRepo,
		Memory:              &inframc.MemoryAdapter{Port: a.MemoryAdapter},
		Tasks: &inframc.TaskAdapter{Repo: a.TaskRepo, Notify: func() {
			if a.SchedulerNotify != nil {
				a.SchedulerNotify()
			}
		}},
		SubAgents: a.SubAgentSvc,
		Terminal:  terminal.NewHostAdapter(),
		Browser: &inframc.BrowserAdapter{
			Port: browser.NewChromeDPAdapter(browser.ChromeDPConfig{Headless: true}),
		},
		Cron: &inframc.CronAdapter{Repo: a.TaskRepo, Notify: func() {
			if a.SchedulerNotify != nil {
				a.SchedulerNotify()
			}
		}},
		Filesystem:    filesystem.NewAdapter(a.CfgPath),
		Conversations: &inframc.ConversationAdapter{ConvRepo: a.ConvRepo, MsgRepo: a.MessageRepo},
		Skills:        a.SkillsAdapter,
		ConfigPath:    a.CfgPath,
		SchedulerNotify: func() {
			if a.SchedulerNotify != nil {
				a.SchedulerNotify()
			}
		},
	})
	log.Printf("tools: registered %d internal tools", len(a.ToolRegistry.AllTools()))

	// Wire tool registry into subagents so they can perform tool_use loops.
	a.SubAgentSvc.SetToolRegistry(a.ToolRegistry)
	a.SubAgentSvc.SetPermissionManager(a.PermManager)

	// Context injector
	a.CtxInjector = appcontext.NewContextInjector(
		cfg.Agent.Name,
		filepath.Join(cfg.Workspace.Path, "AGENTS.md"),
		filepath.Join(cfg.Workspace.Path, "SOUL.md"),
		filepath.Join(cfg.Workspace.Path, "IDENTITY.md"),
		filepath.Join(cfg.Workspace.Path, "BOOTSTRAP.md"),
		filepath.Join(cfg.Workspace.Path, "MEMORY.md"),
		a.MemoryAdapter,
		a.ToolRegistry,
	)

	// Message handler
	gormDB := a.db.GormDB()
	a.MsgHandler = domainhandlers.NewMessageHandler(
		a.AIProvider,
		a.MsgRouter,
		a.MemoryAdapter,
		a.ToolRegistry,
		a.PermManager,
		a.SessionRepo,
		a.MessageRepo,
		a.UserRepo,
		eventBus,
		a.CtxInjector,
		a.CompactionSvc,
		a.UserChannelRepo,
		a.PairingService,
	)
	a.MsgHandler.SetGroupRegistrar(repositories.NewGroupRepository(gormDB))
	a.MsgHandler.SetPlatformEnsurer(repositories.NewChannelRepository(gormDB))
	a.MsgHandler.SetSkillsProvider(a.SkillsAdapter)
	a.MsgHandler.SetPermissionLoader(func(ctx context.Context, userID string) map[string]string {
		records, err := a.ToolPermRepo.ListByUser(ctx, userID)
		if err != nil {
			return nil
		}
		m := make(map[string]string, len(records))
		for _, r := range records {
			m[r.ToolName] = r.Mode
		}
		return m
	})

	// Personality service — used for seeding and message pipeline integration.
	pSvc := personalitysvc.NewService(a.PersonalityRepo, a.RelationshipRepo)
	a.MsgHandler.SetPersonalityService(pSvc)

	// Seed the default personality if none exist yet.
	seedDefaultPersonality(context.Background(), pSvc)
}

// seedDefaultPersonality creates the built-in "MyPal" personality when the
// personalities table is empty. This ensures there is always a usable default
// personality on first boot.
func seedDefaultPersonality(ctx context.Context, svc *personalitysvc.Service) {
	existing, err := svc.List(ctx)
	if err != nil {
		log.Printf("warn: could not check existing personalities: %v", err)
		return
	}
	if len(existing) > 0 {
		return // already have personalities, don't seed
	}

	if err := svc.Create(ctx, &models.PersonalityModel{
		Name:       "MyPal",
		BasePrompt: "You are MyPal, a friendly and capable personal AI assistant. You're warm, thoughtful, and genuinely interested in helping. You remember things about the people you talk to and build real relationships over time.",
		Traits:     `["helpful","curious","warm","thoughtful","reliable"]`,
		Tone:       "friendly",
		Boundaries: `["Do not provide medical, legal, or financial advice","Do not pretend to be human","Be honest about uncertainty"]`,
		Quirks:     `[]`,
		Adaptations: `{"sms":"Be brief and direct. Short sentences.","email":"Use proper greeting and sign-off. Be more formal.","discord":"Be casual and expressive. Use shorter messages.","telegram":"Be concise but friendly.","slack":"Be professional but warm."}`,
		IsDefault:  true,
	}); err != nil {
		log.Printf("warn: failed to seed default personality: %v", err)
		return
	}
	log.Println("personality: seeded default 'MyPal' personality")
}
