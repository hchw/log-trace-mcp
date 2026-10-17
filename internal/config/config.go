// Package config loads runtime configuration for log-trace-mcp.
package config

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Config holds process-level runtime configuration.
type Config struct {
	// MasterKey is the 32-byte AES-256 key used to encrypt credentials at rest.
	MasterKey []byte
	// AdminToken guards the admin HTTP endpoints.
	AdminToken string
	// DBPath is the SQLite database file path.
	DBPath string
	// AdminAddr is the listen address for the admin page.
	AdminAddr string
	// MCPHTTPAddr is the listen address for the streamable HTTP MCP transport.
	MCPHTTPAddr string
}

// fileConfig mirrors the optional JSON configuration file.
type fileConfig struct {
	MasterKey   string `json:"master_key"`
	AdminToken  string `json:"admin_token"`
	DBPath      string `json:"db_path"`
	AdminAddr   string `json:"admin_addr"`
	MCPHTTPAddr string `json:"mcp_http_addr"`
}

const (
	envMasterKey   = "LOG_TRACE_MASTER_KEY"
	envConfigPath  = "LOG_TRACE_CONFIG"
	envAdminToken  = "LOG_TRACE_ADMIN_TOKEN"
	envDBPath      = "LOG_TRACE_DB"
	envAdminAddr   = "LOG_TRACE_ADMIN_ADDR"
	envMCPHTTPAddr = "LOG_TRACE_MCP_HTTP_ADDR"

	defaultDBPath      = "log-trace-mcp.db"
	defaultAdminAddr   = "127.0.0.1:8710"
	defaultMCPHTTPAddr = "127.0.0.1:8711"

	// defaultConfigPath is read when LOG_TRACE_CONFIG is unset and the file exists.
	defaultConfigPath = "config.json"
)

// Load reads configuration from an optional JSON file (LOG_TRACE_CONFIG) and
// overlays environment variables. The master key must resolve to 32 bytes and
// the admin token must be non-empty.
func Load() (Config, error) {
	var fc fileConfig
	if path := resolveConfigPath(); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("read config file %q: %w", path, err)
		}
		if err := json.Unmarshal(data, &fc); err != nil {
			return Config{}, fmt.Errorf("parse config file %q: %w", path, err)
		}
	}

	keyStr := strings.TrimSpace(os.Getenv(envMasterKey))
	if keyStr == "" {
		keyStr = strings.TrimSpace(fc.MasterKey)
	}
	key, err := decodeMasterKey(keyStr)
	if err != nil {
		return Config{}, err
	}

	adminToken := firstNonEmpty(os.Getenv(envAdminToken), fc.AdminToken)
	if adminToken == "" {
		return Config{}, fmt.Errorf("%s is required (or admin_token in config file)", envAdminToken)
	}

	return Config{
		MasterKey:   key,
		AdminToken:  adminToken,
		DBPath:      firstNonEmpty(os.Getenv(envDBPath), fc.DBPath, defaultDBPath),
		AdminAddr:   firstNonEmpty(os.Getenv(envAdminAddr), fc.AdminAddr, defaultAdminAddr),
		MCPHTTPAddr: firstNonEmpty(os.Getenv(envMCPHTTPAddr), fc.MCPHTTPAddr, defaultMCPHTTPAddr),
	}, nil
}

// resolveConfigPath returns the explicit config path from LOG_TRACE_CONFIG, or
// the default ./config.json when it exists.
func resolveConfigPath() string {
	if p := strings.TrimSpace(os.Getenv(envConfigPath)); p != "" {
		return p
	}
	if _, err := os.Stat(defaultConfigPath); err == nil {
		return defaultConfigPath
	}
	return ""
}

func decodeMasterKey(s string) ([]byte, error) {
	if s == "" {
		return nil, fmt.Errorf("%s is required (or master_key in config file)", envMasterKey)
	}
	key, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		// Accept raw (non-base64) keys of exactly 32 bytes as a convenience.
		if len(s) == 32 {
			return []byte(s), nil
		}
		return nil, fmt.Errorf("%s must be base64-encoded 32 bytes: %w", envMasterKey, err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("%s must decode to 32 bytes, got %d", envMasterKey, len(key))
	}
	return key, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// GenerateMasterKey returns a fresh base64-encoded 32-byte master key.
func GenerateMasterKey() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf), nil
}
