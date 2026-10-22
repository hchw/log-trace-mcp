// Package manager wires the store, crypto, adapters and registry together and
// keeps the live adapter set in sync with configuration changes.
package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/hchw/log-trace-mcp/internal/adapter/opensearch"
	"github.com/hchw/log-trace-mcp/internal/adapter/sls"
	tlsadapter "github.com/hchw/log-trace-mcp/internal/adapter/tls"
	"github.com/hchw/log-trace-mcp/internal/crypto"
	"github.com/hchw/log-trace-mcp/internal/sdk"
	"github.com/hchw/log-trace-mcp/internal/store"
)

// Decrypted holds a decrypted credential bundle for editing/testing.
type Decrypted struct {
	TenantID string
	Creds    map[string]string
}

// Manager ties persistence to the live adapter registry.
type Manager struct {
	store  *store.Store
	cipher *crypto.Cipher
	reg    *sdk.Registry
	logger *slog.Logger

	mu          sync.Mutex
	lastVersion int64
}

// New builds a Manager.
func New(st *store.Store, c *crypto.Cipher, reg *sdk.Registry, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{store: st, cipher: c, reg: reg, logger: logger}
}

// Registry exposes the live registry.
func (m *Manager) Registry() *sdk.Registry { return m.reg }

// Store exposes the underlying store.
func (m *Manager) Store() *store.Store { return m.store }

// EncryptCreds encrypts a credential map for an tenant.
func (m *Manager) EncryptCreds(tenantID string, creds map[string]string) ([]byte, error) {
	if len(creds) == 0 {
		return nil, nil
	}
	plain, err := json.Marshal(creds)
	if err != nil {
		return nil, err
	}
	return m.cipher.Encrypt(plain, []byte(tenantID))
}

// DecryptCreds decrypts the credential blob for an tenant.
func (m *Manager) DecryptCreds(e store.Tenant) (map[string]string, error) {
	if len(e.CredsEnc) == 0 {
		return map[string]string{}, nil
	}
	plain, err := m.cipher.Decrypt(e.CredsEnc, []byte(e.ID))
	if err != nil {
		return nil, fmt.Errorf("decrypt creds for %q: %w", e.ID, err)
	}
	out := map[string]string{}
	if err := json.Unmarshal(plain, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Reload rebuilds the adapter set from persisted tenants and swaps it in.
func (m *Manager) Reload() error {
	tenants, err := m.store.ListTenants()
	if err != nil {
		return err
	}
	items := map[string]sdk.AdapterInfo{}
	var errs []string
	for _, e := range tenants {
		if !e.Enabled {
			continue
		}
		info, err := m.build(e)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", e.ID, err))
			continue
		}
		items[e.ID] = info
	}
	m.reg.Replace(items)
	m.logger.Info("registry reloaded", "adapters", len(items), "skipped", len(errs))
	if len(errs) > 0 {
		return fmt.Errorf("some tenants failed to load: %s", strings.Join(errs, "; "))
	}
	return nil
}

func (m *Manager) build(e store.Tenant) (sdk.AdapterInfo, error) {
	creds, err := m.DecryptCreds(e)
	if err != nil {
		return sdk.AdapterInfo{}, err
	}
	switch sdk.EngineType(e.Type) {
	case sdk.EngineSLS:
		var cfg sls.Config
		if err := json.Unmarshal(e.Config, &cfg); err != nil {
			return sdk.AdapterInfo{}, fmt.Errorf("parse sls config: %w", err)
		}
		a, err := sls.New(cfg, sls.Creds{
			AccessKeyID:     creds["ak_id"],
			AccessKeySecret: creds["ak_secret"],
			SecurityToken:   creds["security_token"],
		})
		if err != nil {
			return sdk.AdapterInfo{}, err
		}
		return sdk.AdapterInfo{Adapter: a, Engine: sdk.EngineSLS}, nil

	case sdk.EngineOpenSearch:
		var cfg opensearch.Config
		if err := json.Unmarshal(e.Config, &cfg); err != nil {
			return sdk.AdapterInfo{}, fmt.Errorf("parse opensearch config: %w", err)
		}
		a, err := opensearch.New(cfg, opensearch.Creds{
			Username:    creds["username"],
			Password:    creds["password"],
			HeaderValue: creds["header_value"],
		})
		if err != nil {
			return sdk.AdapterInfo{}, err
		}
		return sdk.AdapterInfo{Adapter: a, Engine: sdk.EngineOpenSearch}, nil

	case sdk.EngineTLS:
		var cfg tlsadapter.Config
		if err := json.Unmarshal(e.Config, &cfg); err != nil {
			return sdk.AdapterInfo{}, fmt.Errorf("parse tls config: %w", err)
		}
		a, err := tlsadapter.New(cfg, tlsadapter.Creds{
			AccessKeyID:     creds["ak_id"],
			AccessKeySecret: creds["ak_secret"],
			SecurityToken:   creds["security_token"],
		})
		if err != nil {
			return sdk.AdapterInfo{}, err
		}
		return sdk.AdapterInfo{Adapter: a, Engine: sdk.EngineTLS}, nil

	default:
		return sdk.AdapterInfo{}, fmt.Errorf("unknown tenant type %q", e.Type)
	}
}

// Watch polls SQLite's data_version to catch out-of-process changes and
// triggers reloads. It returns when ctx is cancelled.
func (m *Manager) Watch(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			v, err := m.store.DataVersion()
			if err != nil {
				m.logger.Warn("data_version read failed", "err", err)
				continue
			}
			m.mu.Lock()
			changed := v != m.lastVersion
			if changed {
				m.lastVersion = v
			}
			m.mu.Unlock()
			if changed {
				m.logger.Info("sqlite changed externally, reloading")
				if err := m.Reload(); err != nil {
					m.logger.Warn("reload after external change failed", "err", err)
				}
			}
		}
	}
}

// MarkDataVersion records the current data_version so a subsequent internal
// write does not trigger a redundant external-change reload.
func (m *Manager) MarkDataVersion() {
	if v, err := m.store.DataVersion(); err == nil {
		m.mu.Lock()
		m.lastVersion = v
		m.mu.Unlock()
	}
}

// Rekey re-encrypts all stored credentials under a new master key.
func (m *Manager) Rekey(newKey []byte) error {
	newCipher, err := crypto.New(newKey)
	if err != nil {
		return err
	}
	tenants, err := m.store.ListTenants()
	if err != nil {
		return err
	}
	for _, e := range tenants {
		if len(e.CredsEnc) == 0 {
			continue
		}
		creds, err := m.DecryptCreds(e)
		if err != nil {
			return err
		}
		blob, err := newCipher.Encrypt(mustJSON(creds), []byte(e.ID))
		if err != nil {
			return err
		}
		e.CredsEnc = blob
		if err := m.store.UpdateTenant(e); err != nil {
			return err
		}
	}
	m.cipher = newCipher
	return m.Reload()
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
