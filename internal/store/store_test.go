package store

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestEnvCRUD(t *testing.T) {
	s := newStore(t)
	e := Tenant{ID: "prod-sls", Name: "Prod SLS", Type: "sls", Enabled: true,
		Config: json.RawMessage(`{"region":"cn-hangzhou"}`), CredsEnc: []byte("cipher")}
	if err := s.CreateTenant(e); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetTenant("prod-sls")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Prod SLS" || !got.Enabled || string(got.CredsEnc) != "cipher" {
		t.Fatalf("unexpected tenant: %+v", got)
	}

	got.Name = "Updated"
	if err := s.UpdateTenant(got); err != nil {
		t.Fatal(err)
	}
	got2, _ := s.GetTenant("prod-sls")
	if got2.Name != "Updated" {
		t.Fatalf("update not applied: %+v", got2)
	}

	if err := s.DeleteTenant("prod-sls"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetTenant("prod-sls"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestEnvConflict(t *testing.T) {
	s := newStore(t)
	e := Tenant{ID: "dup", Type: "sls", Enabled: true, Config: json.RawMessage(`{}`)}
	if err := s.CreateTenant(e); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateTenant(e); err != ErrConflict {
		t.Fatalf("want ErrConflict, got %v", err)
	}
}

func TestUpdatePersistsCredsWhenProvided(t *testing.T) {
	s := newStore(t)
	_ = s.CreateTenant(Tenant{ID: "e", Type: "sls", Enabled: true, Config: json.RawMessage(`{}`), CredsEnc: []byte("old")})
	got, _ := s.GetTenant("e")
	got.CredsEnc = []byte("new")
	_ = s.UpdateTenant(got)
	got2, _ := s.GetTenant("e")
	if string(got2.CredsEnc) != "new" {
		t.Fatalf("creds not updated: %q", got2.CredsEnc)
	}
	// nil creds keeps existing.
	got2.CredsEnc = nil
	_ = s.UpdateTenant(got2)
	got3, _ := s.GetTenant("e")
	if string(got3.CredsEnc) != "new" {
		t.Fatalf("nil creds should keep existing, got %q", got3.CredsEnc)
	}
}

func TestListTenants(t *testing.T) {
	s := newStore(t)
	for _, id := range []string{"b", "a"} {
		_ = s.CreateTenant(Tenant{ID: id, Type: "sls", Enabled: true, Config: json.RawMessage(`{}`)})
	}
	list, err := s.ListTenants()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != "a" || list[1].ID != "b" {
		t.Fatalf("unexpected list: %+v", list)
	}
}

func TestDataVersion(t *testing.T) {
	s := newStore(t)
	v, err := s.DataVersion()
	if err != nil {
		t.Fatal(err)
	}
	_ = s.CreateTenant(Tenant{ID: "x", Type: "sls", Enabled: true, Config: json.RawMessage(`{}`)})
	// data_version may or may not change within the same connection; just ensure it reads.
	if _, err := s.DataVersion(); err != nil {
		t.Fatal(err)
	}
	_ = v
}
