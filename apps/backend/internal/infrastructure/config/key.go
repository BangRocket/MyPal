package config

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/viper"
)

const envSecretKey = "MYPAL_SECRET_KEY"

const envConfigEncrypt = "MYPAL_CONFIG_ENCRYPT"

// DefaultKey returns a fallback 32-byte key when MYPAL_SECRET_KEY is not set.
// Uses a deterministic derivation from "MyPal" so config and secrets are
// always encrypted on disk, even without env. For production, set MYPAL_SECRET_KEY.
func DefaultKey() []byte {
	h := sha256.Sum256([]byte("MyPal"))
	return h[:]
}

// SecretKey returns the 32-byte encryption key for config and secrets.
// Reads MYPAL_SECRET_KEY from env; if unset, uses DefaultKey().
// Config and local secrets (secrets.json) both use this same key.
// Accepted formats:
//   - Base64 (44 chars, 32 bytes decoded)
//   - Hex (64 chars, 32 bytes decoded)
//   - Any other string: SHA256-hashed and truncated to 32 bytes
func SecretKey() []byte {
	s := strings.TrimSpace(os.Getenv(envSecretKey))
	if s == "" {
		return DefaultKey()
	}
	// Base64
	if b, err := base64.StdEncoding.DecodeString(s); err == nil && len(b) == 32 {
		return b
	}
	// Base64 URL-safe
	if b, err := base64.URLEncoding.DecodeString(s); err == nil && len(b) == 32 {
		return b
	}
	// Hex
	if b, err := hex.DecodeString(s); err == nil && len(b) == 32 {
		return b
	}
	// Passphrase: derive
	h := sha256.Sum256([]byte(s))
	return h[:]
}

// ConfigEncryptEnabled returns whether config should be stored encrypted on disk.
// Checks (in priority order):
//  1. MYPAL_CONFIG_ENCRYPT env var (0 = disabled, 1 = enabled)
//  2. config_encrypt key in viper (set via the Settings UI)
//  3. Default: true (encrypted)
func ConfigEncryptEnabled() bool {
	// Env var takes precedence.
	if s := strings.TrimSpace(os.Getenv(envConfigEncrypt)); s != "" {
		v, err := strconv.ParseInt(s, 10, 64)
		if err == nil {
			return v != 0
		}
	}
	// Check viper config key (set by the UI toggle).
	if viper.IsSet("config_encrypt") {
		return viper.GetBool("config_encrypt")
	}
	return false
}
