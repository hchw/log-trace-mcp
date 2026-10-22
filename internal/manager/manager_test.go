package manager

import (
	"encoding/json"
	"testing"

	"github.com/hchw/log-trace-mcp/internal/crypto"
	"github.com/hchw/log-trace-mcp/internal/sdk"
	"github.com/hchw/log-trace-mcp/internal/store"
)

func TestReloadBuildsTLSAdapter(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	cipher, err := crypto.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	reg := sdk.NewRegistry()
	mgr := New(st, cipher, reg, nil)

	creds, err := mgr.EncryptCreds("tls-prod", map[string]string{
		"ak_id":     "ak",
		"ak_secret": "sk",
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := json.Marshal(map[string]string{"region": "cn-beijing"})
	if err := st.CreateTenant(store.Tenant{
		ID:       "tls-prod",
		Name:     "TLS prod",
		Type:     string(sdk.EngineTLS),
		Enabled:  true,
		Config:   cfg,
		CredsEnc: creds,
	}); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	adapter, engine, release, err := reg.Acquire("tls-prod")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer release()
	if adapter == nil || engine != sdk.EngineTLS {
		t.Fatalf("adapter=%v engine=%q", adapter, engine)
	}
}
