package modeltier

import (
	"context"
	"testing"

	"github.com/BangRocket/MyPal/apps/backend/internal/domain/ports"
	"github.com/BangRocket/MyPal/apps/backend/internal/infrastructure/config"
)

// stubProvider implements ports.AIProviderPort for testing.
type stubProvider struct {
	name string
}

func (s *stubProvider) Chat(_ context.Context, _ ports.ChatRequest) (ports.ChatResponse, error) {
	return ports.ChatResponse{Content: s.name}, nil
}
func (s *stubProvider) ChatWithAudio(_ context.Context, _ ports.ChatRequestWithAudio) (ports.ChatResponse, error) {
	return ports.ChatResponse{}, nil
}
func (s *stubProvider) ChatToAudio(_ context.Context, _ ports.ChatRequest) (ports.ChatResponseWithAudio, error) {
	return ports.ChatResponseWithAudio{}, nil
}
func (s *stubProvider) SupportsAudioInput() bool  { return false }
func (s *stubProvider) SupportsAudioOutput() bool { return false }
func (s *stubProvider) GetMaxTokens() int         { return 4096 }

func newTestRouter() *Router {
	cfg := config.ModelTiersConfig{
		Enabled: true,
		Tiers: []config.ModelTierConfig{
			{Name: "high", Provider: "anthropic", Model: "claude-opus-4", Prefix: "!high", Default: false},
			{Name: "mid", Provider: "openai", Model: "gpt-4o", Prefix: "!mid", Default: true},
			{Name: "low", Provider: "ollama", Model: "llama3", Prefix: "!low", Default: false},
		},
	}
	factory := func(providerName, model string) ports.AIProviderPort {
		return &stubProvider{name: providerName + "/" + model}
	}
	return NewRouter(cfg, factory)
}

func TestRouteHighPrefix(t *testing.T) {
	r := newTestRouter()
	provider, cleaned := r.Route("!high hello")
	if cleaned != "hello" {
		t.Errorf("expected cleaned message %q, got %q", "hello", cleaned)
	}
	stub, ok := provider.(*stubProvider)
	if !ok {
		t.Fatal("expected *stubProvider")
	}
	if stub.name != "anthropic/claude-opus-4" {
		t.Errorf("expected provider %q, got %q", "anthropic/claude-opus-4", stub.name)
	}
}

func TestRouteNoPrefix(t *testing.T) {
	r := newTestRouter()
	provider, cleaned := r.Route("hello")
	if cleaned != "hello" {
		t.Errorf("expected message %q unchanged, got %q", "hello", cleaned)
	}
	stub, ok := provider.(*stubProvider)
	if !ok {
		t.Fatal("expected *stubProvider")
	}
	// Default tier is "mid" -> openai/gpt-4o
	if stub.name != "openai/gpt-4o" {
		t.Errorf("expected default provider %q, got %q", "openai/gpt-4o", stub.name)
	}
}

func TestRouteLowPrefix(t *testing.T) {
	r := newTestRouter()
	provider, cleaned := r.Route("!low how are you")
	if cleaned != "how are you" {
		t.Errorf("expected cleaned message %q, got %q", "how are you", cleaned)
	}
	stub, ok := provider.(*stubProvider)
	if !ok {
		t.Fatal("expected *stubProvider")
	}
	if stub.name != "ollama/llama3" {
		t.Errorf("expected provider %q, got %q", "ollama/llama3", stub.name)
	}
}

func TestRouteUnknownPrefix(t *testing.T) {
	r := newTestRouter()
	provider, cleaned := r.Route("!unknown test message")
	if cleaned != "!unknown test message" {
		t.Errorf("expected message %q unchanged, got %q", "!unknown test message", cleaned)
	}
	stub, ok := provider.(*stubProvider)
	if !ok {
		t.Fatal("expected *stubProvider")
	}
	// Should fall through to default
	if stub.name != "openai/gpt-4o" {
		t.Errorf("expected default provider %q, got %q", "openai/gpt-4o", stub.name)
	}
}

func TestDefaultProvider(t *testing.T) {
	r := newTestRouter()
	provider := r.DefaultProvider()
	stub, ok := provider.(*stubProvider)
	if !ok {
		t.Fatal("expected *stubProvider")
	}
	if stub.name != "openai/gpt-4o" {
		t.Errorf("expected default provider %q, got %q", "openai/gpt-4o", stub.name)
	}
}
