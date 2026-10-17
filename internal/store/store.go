// Package store persists log-center tenant configuration in SQLite.
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned when an tenant does not exist.
var ErrNotFound = errors.New("tenant not found")

// ErrConflict is returned when an tenant id already exists.
var ErrConflict = errors.New("tenant id already exists")

// Tenant is a persisted log-center configuration.
type Tenant struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Type      string          `json:"type"`
	Enabled   bool            `json:"enabled"`
	Config    json.RawMessage `json:"config"`
	CredsEnc  []byte          `json:"-"`
	CreatedAt int64           `json:"created_at"`
	UpdatedAt int64           `json:"updated_at"`
}

// Store wraps the SQLite database.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the SQLite database and applies migrations.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// A single writer keeps things simple and avoids "database is locked"
	// under the concurrent MCP + admin workloads.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable WAL: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS schema_version (
  version INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS tenants (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL,
  type        TEXT NOT NULL,
  enabled     INTEGER NOT NULL DEFAULT 1,
  config      TEXT NOT NULL DEFAULT '{}',
  creds_enc   BLOB,
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);
`
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_version`).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		if _, err := s.db.Exec(`INSERT INTO schema_version(version) VALUES (1)`); err != nil {
			return err
		}
	}
	return nil
}

// DataVersion returns the SQLite data_version pragma, which changes when the
// database file is modified by another connection.
func (s *Store) DataVersion() (int64, error) {
	var v int64
	if err := s.db.QueryRow(`PRAGMA data_version`).Scan(&v); err != nil {
		return 0, err
	}
	return v, nil
}

// CreateTenant inserts a new tenant.
func (s *Store) CreateTenant(e Tenant) error {
	if e.ID == "" {
		return errors.New("tenant id is required")
	}
	now := time.Now().Unix()
	if e.Config == nil {
		e.Config = json.RawMessage(`{}`)
	}
	_, err := s.db.Exec(
		`INSERT INTO tenants(id,name,type,enabled,config,creds_enc,created_at,updated_at)
		 VALUES(?,?,?,?,?,?,?,?)`,
		e.ID, e.Name, e.Type, boolToInt(e.Enabled), string(e.Config), e.CredsEnc, now, now,
	)
	if err != nil {
		if isUnique(err) {
			return ErrConflict
		}
		return err
	}
	return nil
}

// UpdateTenant updates name/type/enabled/config and, when credsEnc is non-nil,
// the encrypted credentials.
func (s *Store) UpdateTenant(e Tenant) error {
	now := time.Now().Unix()
	if e.Config == nil {
		e.Config = json.RawMessage(`{}`)
	}
	var res sql.Result
	var err error
	if e.CredsEnc != nil {
		res, err = s.db.Exec(
			`UPDATE tenants SET name=?,type=?,enabled=?,config=?,creds_enc=?,updated_at=? WHERE id=?`,
			e.Name, e.Type, boolToInt(e.Enabled), string(e.Config), e.CredsEnc, now, e.ID,
		)
	} else {
		res, err = s.db.Exec(
			`UPDATE tenants SET name=?,type=?,enabled=?,config=?,updated_at=? WHERE id=?`,
			e.Name, e.Type, boolToInt(e.Enabled), string(e.Config), now, e.ID,
		)
	}
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteTenant removes an tenant.
func (s *Store) DeleteTenant(id string) error {
	res, err := s.db.Exec(`DELETE FROM tenants WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetTenant loads one tenant.
func (s *Store) GetTenant(id string) (Tenant, error) {
	row := s.db.QueryRow(
		`SELECT id,name,type,enabled,config,creds_enc,created_at,updated_at FROM tenants WHERE id=?`, id)
	return scanTenant(row)
}

// ListTenants loads all tenants ordered by id.
func (s *Store) ListTenants() ([]Tenant, error) {
	rows, err := s.db.Query(
		`SELECT id,name,type,enabled,config,creds_enc,created_at,updated_at FROM tenants ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Tenant
	for rows.Next() {
		e, err := scanTenant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

type scanner interface {
	Scan(dest ...any) error
}

func scanTenant(sc scanner) (Tenant, error) {
	var e Tenant
	var enabled int
	var config string
	var creds []byte
	if err := sc.Scan(&e.ID, &e.Name, &e.Type, &enabled, &config, &creds, &e.CreatedAt, &e.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Tenant{}, ErrNotFound
		}
		return Tenant{}, err
	}
	e.Enabled = enabled != 0
	e.Config = json.RawMessage(config)
	e.CredsEnc = creds
	return e, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
