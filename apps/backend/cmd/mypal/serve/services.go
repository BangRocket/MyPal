package serve

import (
	"context"
	"log"
	"path/filepath"
	"time"

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
	dockerbackend "github.com/BangRocket/MyPal/apps/backend/internal/infrastructure/adapters/sandbox/docker"
	incusbackend "github.com/BangRocket/MyPal/apps/backend/internal/infrastructure/adapters/sandbox/incus"
	aifactory "github.com/BangRocket/MyPal/apps/backend/internal/infrastructure/adapters/ai/factory"
	embeddingollama "github.com/BangRocket/MyPal/apps/backend/internal/infrastructure/adapters/embedding/ollama"
	embeddingopenai "github.com/BangRocket/MyPal/apps/backend/internal/infrastructure/adapters/embedding/openai"
	memfalkordb "github.com/BangRocket/MyPal/apps/backend/internal/infrastructure/adapters/memory/falkordb"
	memfile "github.com/BangRocket/MyPal/apps/backend/internal/infrastructure/adapters/memory/file"
	memfilegraph "github.com/BangRocket/MyPal/apps/backend/internal/infrastructure/adapters/memory/filegraph"
	memneo4j "github.com/BangRocket/MyPal/apps/backend/internal/infrastructure/adapters/memory/neo4j"
	mempgvector "github.com/BangRocket/MyPal/apps/backend/internal/infrastructure/adapters/memory/pgvector"
	memqdrant "github.com/BangRocket/MyPal/apps/backend/internal/infrastructure/adapters/memory/qdrant"
	"github.com/BangRocket/MyPal/apps/backend/internal/infrastructure/adapters/terminal"

	"github.com/BangRocket/MyPal/apps/backend/internal/domain/models"
	"github.com/BangRocket/MyPal/apps/backend/internal/domain/ports"
	memorysvc "github.com/BangRocket/MyPal/apps/backend/internal/domain/services/memory"
	heartbeatsvc "github.com/BangRocket/MyPal/apps/backend/internal/domain/services/heartbeat"
	sandboxsvc "github.com/BangRocket/MyPal/apps/backend/internal/domain/services/sandbox"
	"github.com/BangRocket/MyPal/apps/backend/internal/domain/services/modeltier"
	organicsvc "github.com/BangRocket/MyPal/apps/backend/internal/domain/services/organic"
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

	// Enhanced memory system (vector + graph)
	a.MemorySys = a.initEnhancedMemory()

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

	// Heartbeat service — note: the dispatcher is wired in startAndWait after
	// the message handler is ready, so we pass nil here and set it later.
	a.HeartbeatSvc = heartbeatsvc.NewService(a.HeartbeatRepo, nil, 0)

	// Sandbox manager (optional, behind cfg.Sandbox.Enabled)
	var sandboxAdapter *inframc.SandboxAdapter
	if cfg.Sandbox.Enabled {
		var backend ports.SandboxBackend
		switch cfg.Sandbox.Backend {
		case "incus":
			backend = incusbackend.NewBackend(cfg.Sandbox.IncusSocket)
			log.Println("sandbox backend: incus")
		default: // "docker" or unset
			backend = dockerbackend.NewBackend(cfg.Sandbox.DockerHost)
			log.Println("sandbox backend: docker")
		}

		timeout := time.Duration(cfg.Sandbox.Timeout) * time.Second
		if timeout == 0 {
			timeout = 300 * time.Second
		}
		memDefault := cfg.Sandbox.MemDefault
		if memDefault == 0 {
			memDefault = 268435456 // 256 MB
		}
		cpuDefault := cfg.Sandbox.CPUDefault
		if cpuDefault == 0 {
			cpuDefault = 1.0
		}
		netDefault := cfg.Sandbox.NetDefault
		if netDefault == "" {
			netDefault = "none"
		}

		a.SandboxMgr = sandboxsvc.NewManager(backend, timeout, memDefault, cpuDefault, netDefault, cfg.Sandbox.PoolSize)
		sandboxAdapter = &inframc.SandboxAdapter{Mgr: a.SandboxMgr}
		log.Printf("sandbox: enabled (pool_size=%d, timeout=%s)", cfg.Sandbox.PoolSize, timeout)

		if cfg.Sandbox.PoolSize > 0 {
			poolImage := cfg.Sandbox.PoolImage
			if poolImage == "" {
				poolImage = "python:3.12-slim"
			}
			if err := a.SandboxMgr.WarmPool(context.Background(), poolImage, cfg.Sandbox.PoolSize); err != nil {
				log.Printf("sandbox: warm pool failed: %v", err)
			} else {
				log.Printf("sandbox: warmed %d containers (image=%s)", cfg.Sandbox.PoolSize, poolImage)
			}
		}
	}

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
		Heartbeat:     &inframc.HeartbeatAdapter{Svc: a.HeartbeatSvc},
		Sandbox:       sandboxAdapter,
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
	// When the enhanced memory system has a graph backend (FalkorDB), attach
	// it to the context injector so imported memories are visible in prompts.
	if a.MemorySys != nil && a.MemorySys.Graph != nil {
		type graphSetter interface {
			SetGraphBackend(ports.GraphBackend)
		}
		if ci, ok := a.CtxInjector.(graphSetter); ok {
			ci.SetGraphBackend(a.MemorySys.Graph)
			log.Println("context injector: enhanced graph backend attached")
		}
	}

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

	// Organic response service — decides when to respond to un-mentioned group messages.
	organicRepo := repositories.NewOrganicResponseConfigRepository(gormDB)
	a.OrganicConfigRepo = organicRepo
	agentName := cfg.Agent.Name
	if agentName == "" {
		agentName = "MyPal"
	}
	oSvc := organicsvc.NewService(organicRepo, agentName)
	a.MsgHandler.SetOrganicService(oSvc)

	// Model tier router — optional prefix-based routing to different providers.
	if cfg.ModelTiers.Enabled {
		tierRouter := modeltier.NewRouter(cfg.ModelTiers, func(providerName, model string) ports.AIProviderPort {
			return aifactory.BuildProvider(cfg, providerName, model)
		})
		a.MsgHandler.SetTierRouter(tierRouter)
		log.Printf("model tiers: enabled with %d tier(s)", len(cfg.ModelTiers.Tiers))
	}

	// Seed the default personality if none exist yet.
	seedDefaultPersonality(context.Background(), pSvc)
}

// initEnhancedMemory creates the enhanced MemorySystem (vector + graph)
// based on configuration. Returns nil when both subsystems are disabled.
func (a *App) initEnhancedMemory() *memorysvc.MemorySystem {
	cfg := a.Cfg

	// Neither enabled → nothing to do.
	if !cfg.Memory.Vector.Enabled && !cfg.Memory.Graph.Enabled {
		log.Println("enhanced memory: disabled")
		return nil
	}

	// Build embedding provider (needed by vector memory).
	var embedder ports.EmbeddingProvider
	if cfg.Memory.Vector.Enabled {
		switch cfg.Embedding.Provider {
		case "openai":
			embedder = embeddingopenai.NewProvider(
				cfg.Embedding.OpenAI.APIKey,
				cfg.Embedding.OpenAI.Model,
			)
			log.Printf("embedding provider: openai (model=%s)", cfg.Embedding.OpenAI.Model)
		case "ollama":
			embedder = embeddingollama.NewProvider(
				cfg.Embedding.Ollama.Endpoint,
				cfg.Embedding.Ollama.Model,
			)
			log.Printf("embedding provider: ollama (model=%s, endpoint=%s)", cfg.Embedding.Ollama.Model, cfg.Embedding.Ollama.Endpoint)
		default:
			log.Fatalf("enhanced memory: unknown embedding.provider %q", cfg.Embedding.Provider)
		}
	}

	// Build vector memory subsystem.
	var vectorMem *memorysvc.VectorMemory
	if cfg.Memory.Vector.Enabled && embedder != nil {
		switch cfg.Memory.Vector.Backend {
		case "pgvector":
			gormDB := a.db.GormDB()
			store := mempgvector.NewStore(gormDB)
			vectorMem = memorysvc.NewVectorMemory(store, embedder, cfg.Memory.Vector.TopK)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := vectorMem.Init(ctx); err != nil {
				log.Fatalf("enhanced memory: vector init failed: %v", err)
			}
			log.Println("enhanced memory: vector (pgvector) ready")
		case "qdrant":
			store := memqdrant.NewStore(
				cfg.Memory.Vector.Qdrant.Endpoint,
				cfg.Memory.Vector.Qdrant.Collection,
				cfg.Memory.Vector.Qdrant.APIKey,
			)
			vectorMem = memorysvc.NewVectorMemory(store, embedder, cfg.Memory.Vector.TopK)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := vectorMem.Init(ctx); err != nil {
				log.Fatalf("enhanced memory: vector init failed: %v", err)
			}
			log.Printf("enhanced memory: vector (qdrant @ %s) ready", cfg.Memory.Vector.Qdrant.Endpoint)
		default:
			log.Fatalf("enhanced memory: unknown memory.vector.backend %q", cfg.Memory.Vector.Backend)
		}
	}

	// Build graph memory subsystem.
	var graphBackend ports.GraphBackend
	if cfg.Memory.Graph.Enabled {
		switch cfg.Memory.Graph.Backend {
		case "falkordb":
			store, err := memfalkordb.NewStore(
				cfg.Memory.Graph.FalkorDB.Addr,
				cfg.Memory.Graph.FalkorDB.Password,
				cfg.Memory.Graph.FalkorDB.Graph,
			)
			if err != nil {
				log.Fatalf("enhanced memory: falkordb connect failed: %v", err)
			}
			graphBackend = store
			log.Printf("enhanced memory: graph (falkordb @ %s) ready", cfg.Memory.Graph.FalkorDB.Addr)
		case "file":
			filePath := cfg.Memory.Graph.FilePath
			store, err := memfilegraph.NewStore(filePath)
			if err != nil {
				log.Fatalf("enhanced memory: file graph load failed: %v", err)
			}
			graphBackend = store
			log.Printf("enhanced memory: graph (file @ %s) ready", filePath)
		default:
			log.Fatalf("enhanced memory: unknown memory.graph.backend %q", cfg.Memory.Graph.Backend)
		}
	}

	sys := memorysvc.NewMemorySystem(vectorMem, graphBackend)
	log.Printf("enhanced memory: system ready (vector=%v, graph=%v)", sys.Vector != nil, sys.Graph != nil)
	return sys
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
