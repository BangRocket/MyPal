package terminal

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFilterMyPalFromEnv_SystemEnv(t *testing.T) {
	// Set some MYPAL_ vars and a normal var
	os.Setenv("MYPAL_SECRET_KEY", "test-key")
	os.Setenv("MYPAL_GRAPHQL_AUTH_TOKEN", "token123")
	os.Setenv("PATH", "/usr/bin")
	os.Setenv("HOME", "/home/user")
	defer func() {
		os.Unsetenv("MYPAL_SECRET_KEY")
		os.Unsetenv("MYPAL_GRAPHQL_AUTH_TOKEN")
	}()

	env := FilterMyPalFromEnv(os.Environ())

	hasPath := false
	hasHome := false
	hasSecretKey := false
	hasToken := false
	for _, e := range env {
		if len(e) >= 4 && e[:4] == "PATH" {
			hasPath = true
		}
		if len(e) >= 4 && e[:4] == "HOME" {
			hasHome = true
		}
		if len(e) >= 20 && e[:20] == "MYPAL_SECRET_K" {
			hasSecretKey = true
		}
		if len(e) >= 27 && e[:27] == "MYPAL_GRAPHQL_AUTH_TO" {
			hasToken = true
		}
	}

	assert.True(t, hasPath, "PATH should be present")
	assert.True(t, hasHome, "HOME should be present")
	assert.False(t, hasSecretKey, "MYPAL_SECRET_KEY must not leak")
	assert.False(t, hasToken, "MYPAL_GRAPHQL_AUTH_TOKEN must not leak")
}

func TestFilterMyPalFromEnv(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"MYPAL_SECRET_KEY=secret123",
		"HOME=/home/user",
		"mypal_graphql_auth_token=leak",
		"MYPAL_CONFIG_PATH=/etc/config",
	}
	filtered := FilterMyPalFromEnv(env)

	hasPath := false
	hasHome := false
	for _, e := range filtered {
		if strings.HasPrefix(e, "PATH=") {
			hasPath = true
		}
		if strings.HasPrefix(e, "HOME=") {
			hasHome = true
		}
		if strings.HasPrefix(e, "MYPAL_") {
			t.Errorf("MYPAL_* must be filtered from user env, got %q", e)
		}
	}
	assert.True(t, hasPath)
	assert.True(t, hasHome)
	assert.Len(t, filtered, 2) // PATH and HOME only
}
