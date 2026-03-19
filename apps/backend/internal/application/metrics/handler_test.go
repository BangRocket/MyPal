package metrics

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BangRocket/MyPal/apps/backend/internal/application/graphql/resolvers"
	"github.com/BangRocket/MyPal/apps/backend/internal/application/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandler_ServeHTTP(t *testing.T) {
	deps := &resolvers.Deps{AgentRegistry: registry.NewAgentRegistry()}
	h := NewHandler(deps)
	req := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, 200, rec.Code)
	body := rec.Body.String()
	assert.True(t, strings.Contains(body, "mypal_uptime_seconds") ||
		strings.Contains(body, "# HELP mypal"), "must include Prometheus metrics")
}
