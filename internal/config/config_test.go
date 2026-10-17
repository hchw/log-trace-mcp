package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRequiresMasterKey(t *testing.T) {
	t.Setenv(envMasterKey, "")
	t.Setenv(envConfigPath, "")
	t.Setenv(envAdminToken, "tok")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for missing master key")
	}
}

func TestLoadRequiresAdminToken(t *testing.T) {
	t.Setenv(envMasterKey, mustKey(t))
	t.Setenv(envConfigPath, "")
	t.Setenv(envAdminToken, "")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for missing admin token")
	}
}

func TestLoadFromEnv(t *testing.T) {
	t.Setenv(envMasterKey, mustKey(t))
	t.Setenv(envAdminToken, "tok")
	t.Setenv(envConfigPath, "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.MasterKey) != 32 || cfg.AdminToken != "tok" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if cfg.DBPath != defaultDBPath {
		t.Fatalf("want default db path, got %q", cfg.DBPath)
	}
}

func TestLoadFromFileWithEnvOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{"master_key":"` + mustKey(t) + `","admin_token":"filetok","db_path":"file.db"}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envConfigPath, path)
	t.Setenv(envMasterKey, "")
	t.Setenv(envAdminToken, "envtok")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AdminToken != "envtok" {
		t.Fatalf("tenant token should override file, got %q", cfg.AdminToken)
	}
	if cfg.DBPath != "file.db" {
		t.Fatalf("want file.db, got %q", cfg.DBPath)
	}
}

func TestDecodeMasterKeyWrongLength(t *testing.T) {
	t.Setenv(envMasterKey, "c2hvcnQ=") // "short"
	t.Setenv(envAdminToken, "tok")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for wrong key length")
	}
}

func TestLoadDefaultConfigFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	key := mustKey(t)
	content := `{"master_key":"` + key + `","admin_token":"fromdefault","db_path":"d.db"}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	// No LOG_TRACE_CONFIG, no env overrides: the default ./config.json is used.
	t.Setenv(envConfigPath, "")
	t.Setenv(envMasterKey, "")
	t.Setenv(envAdminToken, "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AdminToken != "fromdefault" || cfg.DBPath != "d.db" {
		t.Fatalf("default config not loaded: %+v", cfg)
	}
}

func mustKey(t *testing.T) string {
	t.Helper()
	k, err := GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	return k
}
