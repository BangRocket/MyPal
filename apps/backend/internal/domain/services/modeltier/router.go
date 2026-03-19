// Package modeltier provides prefix-based routing to different AI model tiers.
// Users can prefix their messages (e.g. "!high", "!mid", "!low") to select a
// specific provider/model combination. When no prefix matches, the default tier
// is used.
package modeltier

import (
	"strings"

	"github.com/BangRocket/MyPal/apps/backend/internal/domain/ports"
	"github.com/BangRocket/MyPal/apps/backend/internal/infrastructure/config"
)

// Router maps message prefixes to AI provider tiers.
type Router struct {
	tiers           map[string]ports.AIProviderPort // tier name -> provider
	prefixes        map[string]string               // prefix -> tier name
	defaultTier     string
	defaultProvider ports.AIProviderPort
}

// NewRouter creates a tier router from config. providerFactory builds a
// provider for a given provider name and model.
func NewRouter(cfg config.ModelTiersConfig, providerFactory func(providerName, model string) ports.AIProviderPort) *Router {
	r := &Router{
		tiers:    make(map[string]ports.AIProviderPort),
		prefixes: make(map[string]string),
	}

	for _, t := range cfg.Tiers {
		provider := providerFactory(t.Provider, t.Model)
		if provider == nil {
			continue
		}
		r.tiers[t.Name] = provider
		r.prefixes[t.Prefix] = t.Name

		if t.Default {
			r.defaultTier = t.Name
			r.defaultProvider = provider
		}
	}

	return r
}

// Route checks if the message starts with a tier prefix, strips it, and
// returns the appropriate provider. If no prefix matches, returns the default
// tier's provider with the original message.
func (r *Router) Route(message string) (provider ports.AIProviderPort, cleanedMessage string) {
	for prefix, tierName := range r.prefixes {
		if strings.HasPrefix(message, prefix) {
			if p, ok := r.tiers[tierName]; ok {
				cleaned := strings.TrimSpace(strings.TrimPrefix(message, prefix))
				return p, cleaned
			}
		}
	}
	return r.defaultProvider, message
}

// DefaultProvider returns the default tier's provider (for non-message
// contexts like tool calls).
func (r *Router) DefaultProvider() ports.AIProviderPort {
	return r.defaultProvider
}
