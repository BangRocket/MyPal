package context

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/BangRocket/MyPal/apps/backend/internal/domain/ports"
	"github.com/BangRocket/MyPal/apps/backend/internal/domain/services/mcp"
)

type ContextInjector interface {
	BuildContext(ctx context.Context, userID string, sessionID string) (*AgentLLMContext, error)
	GetUserMemory(ctx context.Context, userID string) (*ports.Graph, error)
	GetGroupMemories(ctx context.Context, userIDs []string) ([]*ports.Graph, error)
	QueryUserMemory(ctx context.Context, requesterID, targetID string) (*ports.Graph, error)
}

type AgentLLMContext struct {
	AgentName     string
	AgentsMD      string
	SoulMD        string
	IdentityMD    string
	BootstrapMD   string
	MemoryMD      string
	MCPs          []MCPResource
	Tools         []Tool
	UserMemory    string
	GroupMemories []string
	// UserDisplayName is the human-readable name of the user the agent is
	// currently talking with. Populated from the user_channels table.
	UserDisplayName string
	// SkillsCatalog holds the lightweight skill catalog (name + description)
	// populated by the message handler before building the system prompt.
	// Each entry is injected into the prompt so the LLM knows which skills are
	// available and can call load_skill on demand.
	SkillsCatalog []mcp.SkillCatalogEntry
}

type MCPResource struct {
	Name  string
	Tools []string
}

type Tool struct {
	Name        string
	Description string
	Category    string
}

// VectorRecaller is the subset of VectorMemory needed for context injection.
type VectorRecaller interface {
	Recall(ctx context.Context, userID, query string, topK int) ([]ports.VectorResult, error)
}

type contextInjector struct {
	agentName      string
	agentsPath     string
	soulPath       string
	identityPath   string
	bootstrapPath  string
	memoryPath     string
	memoryPort     ports.MemoryPort
	graphBackend   ports.GraphBackend // optional enhanced graph (FalkorDB)
	vectorRecaller VectorRecaller     // optional enhanced vector (Qdrant)
	toolRegistry   *mcp.ToolRegistry
}

func NewContextInjector(
	agentName string,
	agentsPath string,
	soulPath string,
	identityPath string,
	bootstrapPath string,
	memoryPath string,
	memoryPort ports.MemoryPort,
	toolRegistry *mcp.ToolRegistry,
) ContextInjector {
	return &contextInjector{
		agentName:     agentName,
		agentsPath:    agentsPath,
		soulPath:      soulPath,
		identityPath:  identityPath,
		bootstrapPath: bootstrapPath,
		memoryPath:    memoryPath,
		memoryPort:    memoryPort,
		toolRegistry:  toolRegistry,
	}
}

// SetGraphBackend attaches an enhanced graph backend (e.g. FalkorDB) to
// the context injector. When set, getMemoryDigest reads from this backend
// instead of the legacy MemoryPort, enabling access to imported memories.
func (c *contextInjector) SetGraphBackend(gb ports.GraphBackend) {
	c.graphBackend = gb
}

// SetVectorRecaller attaches a vector memory service (e.g. Qdrant) to
// the context injector. When set, BuildContext performs a semantic search
// against the user's message to surface relevant memories.
func (c *contextInjector) SetVectorRecaller(vr VectorRecaller) {
	c.vectorRecaller = vr
}

func (c *contextInjector) BuildContext(ctx context.Context, userID string, sessionID string) (*AgentLLMContext, error) {
	log.Printf("context: building context for user %s", userID)
	agentCtx := &AgentLLMContext{AgentName: c.agentName}

	var err error
	agentCtx.AgentsMD, err = c.loadSystemFile(c.agentsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load agents file: %w", err)
	}
	agentCtx.SoulMD, err = c.loadSystemFile(c.soulPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load soul file: %w", err)
	}
	agentCtx.IdentityMD, err = c.loadSystemFile(c.identityPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load identity file: %w", err)
	}
	agentCtx.BootstrapMD, err = c.loadSystemFile(c.bootstrapPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load bootstrap file: %w", err)
	}
	agentCtx.MemoryMD, err = c.loadSystemFile(c.memoryPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load memory file: %w", err)
	}

	promptCount := 0
	for _, s := range []string{agentCtx.AgentsMD, agentCtx.SoulMD, agentCtx.IdentityMD, agentCtx.BootstrapMD, agentCtx.MemoryMD} {
		if s != "" {
			promptCount++
		}
	}
	log.Printf("context: loaded %d system prompt files", promptCount)

	agentCtx.MCPs = c.getMCPs()
	agentCtx.Tools = c.getTools()
	log.Printf("context: %d tools available for agent", len(agentCtx.Tools))

	if userID != "" {
		memoryDigest, err := c.getMemoryDigest(ctx, userID)
		if err == nil && memoryDigest != "" {
			agentCtx.UserMemory = memoryDigest
			log.Printf("context: retrieved memory digest (%d chars)", len(memoryDigest))
		}
	}

	return agentCtx, nil
}

func (c *contextInjector) GetUserMemory(ctx context.Context, userID string) (*ports.Graph, error) {
	if c.memoryPort == nil {
		return &ports.Graph{}, nil
	}
	graph, err := c.memoryPort.GetUserGraph(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &graph, nil
}

func (c *contextInjector) GetGroupMemories(ctx context.Context, userIDs []string) ([]*ports.Graph, error) {
	if c.memoryPort == nil {
		return make([]*ports.Graph, 0), nil
	}

	graphs := make([]*ports.Graph, 0, len(userIDs))
	for _, uid := range userIDs {
		graph, err := c.memoryPort.GetUserGraph(ctx, uid)
		if err == nil {
			graphs = append(graphs, &graph)
		}
	}
	return graphs, nil
}

func (c *contextInjector) QueryUserMemory(ctx context.Context, requesterID, targetID string) (*ports.Graph, error) {
	return c.GetUserMemory(ctx, targetID)
}

func (c *contextInjector) loadSystemFile(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		// Return empty string when the file is not found; callers treat "" as no content.
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

func (c *contextInjector) getMCPs() []MCPResource {
	if c.toolRegistry == nil {
		return []MCPResource{}
	}

	mcps := make(map[string]MCPResource)
	allTools := c.toolRegistry.AllTools()

	for _, tool := range allTools {
		parts := splitToolName(tool.Name)
		if len(parts) >= 2 {
			serverName := parts[0]
			mcpRes, exists := mcps[serverName]
			if !exists {
				mcpRes = MCPResource{Name: serverName, Tools: []string{}}
			}
			mcpRes.Tools = append(mcpRes.Tools, tool.Name)
			mcps[serverName] = mcpRes
		}
	}

	result := make([]MCPResource, 0, len(mcps))
	for _, m := range mcps {
		result = append(result, m)
	}
	return result
}

func (c *contextInjector) getTools() []Tool {
	if c.toolRegistry == nil {
		return []Tool{}
	}

	allTools := c.toolRegistry.AllTools()
	tools := make([]Tool, 0, len(allTools))

	for _, t := range allTools {
		parts := splitToolName(t.Name)
		toolName := t.Name
		category := "internal"
		if len(parts) >= 2 {
			category = parts[0]
			toolName = parts[1]
		}
		tools = append(tools, Tool{
			Name:        toolName,
			Description: t.Description,
			Category:    category,
		})
	}
	return tools
}

func (c *contextInjector) getMemoryDigest(ctx context.Context, userID string) (string, error) {
	var parts []string

	// 1. Vector recall — semantic memories with actual content.
	if c.vectorRecaller != nil {
		results, err := c.vectorRecaller.Recall(ctx, userID, "user background and preferences", 20)
		if err == nil && len(results) > 0 {
			var b strings.Builder
			b.WriteString("## Memories about this user\n")
			for _, r := range results {
				if r.Content != "" {
					b.WriteString("- ")
					b.WriteString(r.Content)
					b.WriteString("\n")
				}
			}
			parts = append(parts, b.String())
		}
	}

	// 2. Graph entities — known facts and relationships.
	if c.graphBackend != nil {
		entities, relations, err := c.graphBackend.UserGraph(ctx, userID)
		if err == nil && len(entities) > 0 {
			var b strings.Builder
			b.WriteString("## Known entities related to this user\n")
			// Cap at 50 entities to avoid bloating the prompt.
			limit := len(entities)
			if limit > 50 {
				limit = 50
			}
			for _, e := range entities[:limit] {
				if e.Name != "" {
					name := strings.ReplaceAll(e.Name, "_", " ")
					b.WriteString("- ")
					b.WriteString(name)
					b.WriteString("\n")
				}
			}
			if len(relations) > 0 {
				b.WriteString("\nRelationships:\n")
				relLimit := len(relations)
				if relLimit > 30 {
					relLimit = 30
				}
				for _, r := range relations[:relLimit] {
					fromName := strings.ReplaceAll(r.FromID, "_", " ")
					toName := strings.ReplaceAll(r.ToID, "_", " ")
					b.WriteString("- ")
					b.WriteString(fromName)
					b.WriteString(" -[")
					b.WriteString(r.Type)
					b.WriteString("]-> ")
					b.WriteString(toName)
					b.WriteString("\n")
				}
			}
			parts = append(parts, b.String())
		}
	}

	if len(parts) > 0 {
		return strings.Join(parts, "\n"), nil
	}

	// 3. Fall back to legacy MemoryPort (GML/Neo4j).
	if c.memoryPort == nil {
		return "", nil
	}

	graph, err := c.memoryPort.GetUserGraph(ctx, userID)
	if err != nil {
		return "", err
	}

	if len(graph.Nodes) == 0 {
		return "", nil
	}

	return formatGraphAsText(&graph), nil
}

func splitToolName(name string) []string {
	var parts []string
	current := ""
	for _, ch := range name {
		if ch == ':' {
			parts = append(parts, current)
			current = ""
		} else {
			current += string(ch)
		}
	}
	parts = append(parts, current)
	return parts
}

func formatGraphAsText(graph *ports.Graph) string {
	if graph == nil || len(graph.Nodes) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("User memory:\n")

	nodeMap := make(map[string]ports.GraphNode)
	for _, node := range graph.Nodes {
		nodeMap[node.ID] = node
	}

	// Emit user node properties (key/value pairs set via set_user_property).
	for _, node := range graph.Nodes {
		if node.Type == "user" && len(node.Properties) > 0 {
			b.WriteString("\nUser profile properties:\n")
			for k, v := range node.Properties {
				b.WriteString("  " + k + ": " + v + "\n")
			}
		}
	}

	// Emit free-text facts linked to the user node.
	for _, edge := range graph.Edges {
		source, ok := nodeMap[edge.Source]
		if !ok {
			continue
		}
		target, ok := nodeMap[edge.Target]
		if !ok {
			continue
		}

		if source.Type == "user" && target.Type == "fact" {
			fmt.Fprintf(&b, "- [node_id:%s] %s\n", target.ID, target.Value)
		}
	}

	return b.String()
}
