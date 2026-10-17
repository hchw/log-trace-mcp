// Package admin serves the configuration page and JSON API.
package admin

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hchw/log-trace-mcp/internal/manager"
	"github.com/hchw/log-trace-mcp/internal/sdk"
	"github.com/hchw/log-trace-mcp/internal/store"
)

//go:embed static/*
var staticFS embed.FS

const (
	sessionCookie = "ltm_session"
	sessionTTL    = 12 * time.Hour
)

// Server serves admin HTTP endpoints.
type Server struct {
	mgr      *manager.Manager
	token    string
	toolsFn  func() []string
	onChange func()
	sessions *sessionStore
}

// New builds an admin Server. onChange is invoked after any configuration
// write so engine-exclusive tools can be re-gated.
func New(mgr *manager.Manager, token string, toolsFn func() []string, onChange func()) *Server {
	return &Server{
		mgr:      mgr,
		token:    token,
		toolsFn:  toolsFn,
		onChange: onChange,
		sessions: newSessionStore(sessionTTL),
	}
}

// Handler returns the admin HTTP handler.
//
// Authentication model: browsers log in once at /login with the admin token and
// receive an HttpOnly session cookie; non-browser clients may instead send the
// token on every request as `Authorization: Bearer <token>`.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Public: login page and login submission.
	mux.HandleFunc("GET /login", s.handleLoginPage)
	mux.HandleFunc("POST /api/login", s.handleLogin)

	// Authenticated.
	mux.HandleFunc("POST /api/logout", s.auth(s.handleLogout))
	mux.HandleFunc("GET /api/tenants", s.auth(s.handleList))
	mux.HandleFunc("POST /api/tenants", s.auth(s.handleCreate))
	mux.HandleFunc("GET /api/tenants/{id}", s.auth(s.handleGet))
	mux.HandleFunc("PUT /api/tenants/{id}", s.auth(s.handleUpdate))
	mux.HandleFunc("DELETE /api/tenants/{id}", s.auth(s.handleDelete))
	mux.HandleFunc("POST /api/tenants/{id}/test", s.auth(s.handleTest))
	mux.HandleFunc("GET /api/tools", s.auth(s.handleTools))

	// Gated pages and static assets.
	mux.HandleFunc("GET /", s.handlePage)
	return mux
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authenticated(r) {
			// API callers get a plain 401; the page handler redirects to /login.
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// authenticated accepts either a valid session cookie or a bearer/basic token.
func (s *Server) authenticated(r *http.Request) bool {
	if c, err := r.Cookie(sessionCookie); err == nil && s.sessions.valid(c.Value) {
		return true
	}
	provided := ""
	if h := r.Header.Get("Authorization"); h != "" {
		if strings.HasPrefix(strings.ToLower(h), "bearer ") {
			provided = strings.TrimSpace(h[len("bearer "):])
		} else if strings.HasPrefix(strings.ToLower(h), "basic ") {
			// For Basic, treat the password as the token.
			if _, pw, ok := r.BasicAuth(); ok {
				provided = pw
			}
		}
	}
	if provided == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(s.token)) == 1
}

// handlePage serves the admin page when authenticated, otherwise redirects to
// the login page.
func (s *Server) handlePage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		// Only the single-page app is served; unknown paths go to the root.
		if !s.authenticated(r) {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		http.NotFound(w, r)
		return
	}
	if !s.authenticated(r) {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	s.serveStatic(w, "index.html")
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if s.authenticated(r) {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	s.serveStatic(w, "login.html")
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if subtle.ConstantTimeCompare([]byte(body.Token), []byte(s.token)) != 1 {
		writeErr(w, http.StatusUnauthorized, fmt.Errorf("invalid token"))
		return
	}
	id, err := s.sessions.create()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.sessions.delete(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, MaxAge: -1})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) serveStatic(w http.ResponseWriter, name string) {
	sub, _ := fs.Sub(staticFS, "static")
	data, err := fs.ReadFile(sub, name)
	if err != nil {
		http.NotFound(w, nil)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

// sessionStore is an in-memory set of login sessions with a TTL.
type sessionStore struct {
	mu  sync.Mutex
	ttl time.Duration
	m   map[string]time.Time
}

func newSessionStore(ttl time.Duration) *sessionStore {
	return &sessionStore{ttl: ttl, m: map[string]time.Time{}}
}

func (s *sessionStore) create() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	id := hex.EncodeToString(buf)
	s.mu.Lock()
	s.m[id] = time.Now().Add(s.ttl)
	s.mu.Unlock()
	return id, nil
}

func (s *sessionStore) valid(id string) bool {
	if id == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.m[id]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(s.m, id)
		return false
	}
	return true
}

func (s *sessionStore) delete(id string) {
	s.mu.Lock()
	delete(s.m, id)
	s.mu.Unlock()
}

type tenantView struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Type      string          `json:"type"`
	Enabled   bool            `json:"enabled"`
	Config    json.RawMessage `json:"config"`
	HasCreds  bool            `json:"has_creds"`
	Masked    string          `json:"creds"`
	CreatedAt int64           `json:"created_at"`
	UpdatedAt int64           `json:"updated_at"`
}

type tenantRequest struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Type    string            `json:"type"`
	Enabled *bool             `json:"enabled"`
	Config  json.RawMessage   `json:"config"`
	Creds   map[string]string `json:"creds"`
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	tenants, err := s.mgr.Store().ListTenants()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	views := make([]tenantView, 0, len(tenants))
	for _, e := range tenants {
		views = append(views, toView(e))
	}
	writeJSON(w, http.StatusOK, views)
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	e, err := s.mgr.Store().GetTenant(r.PathValue("id"))
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, toView(e))
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req tenantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(req.ID) == "" || strings.TrimSpace(req.Type) == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("id and type are required"))
		return
	}
	creds, err := s.mgr.EncryptCreds(req.ID, req.Creds)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	tenant := store.Tenant{
		ID:       req.ID,
		Name:     req.Name,
		Type:     req.Type,
		Enabled:  enabled,
		Config:   normalizeConfig(req.Config),
		CredsEnc: creds,
	}
	if tenant.Name == "" {
		tenant.Name = req.ID
	}
	if err := s.mgr.Store().CreateTenant(tenant); err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	s.afterWrite(w)
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req tenantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	existing, err := s.mgr.Store().GetTenant(id)
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	existing.Name = firstNonEmpty(req.Name, existing.Name)
	if req.Type != "" {
		existing.Type = req.Type
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}
	if req.Config != nil {
		existing.Config = req.Config
	}
	if req.Creds != nil {
		blob, err := s.mgr.EncryptCreds(id, req.Creds)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		existing.CredsEnc = blob
	}
	if err := s.mgr.Store().UpdateTenant(existing); err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	s.afterWrite(w)
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.mgr.Store().DeleteTenant(r.PathValue("id")); err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	s.afterWrite(w)
}

func (s *Server) handleTest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	adapter, engine, release, err := s.mgr.Registry().Acquire(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	defer release()

	const timeout = 10 * time.Second
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	logger := slog.Default().With("component", "admin", "tenant", id, "engine", engine)
	logger.Info("connectivity test started")
	start := time.Now()

	// Run in a goroutine so a backend that ignores context cancellation still
	// yields a bounded response to the UI.
	type outcome struct {
		sources sdk.Sources
		err     error
	}
	ch := make(chan outcome, 1)
	go func() {
		res, err := adapter.ListSources(ctx, "")
		ch <- outcome{res, err}
	}()

	select {
	case o := <-ch:
		dur := time.Since(start).Milliseconds()
		if o.err != nil {
			logger.Warn("connectivity test failed", "duration_ms", dur, "err", o.err)
			writeJSON(w, http.StatusOK, map[string]any{
				"ok": false, "error": o.err.Error(), "duration_ms": dur,
				"tenant": id, "engine": engine,
			})
			return
		}
		sample := make([]string, 0, 5)
		for i, src := range o.sources.Flat {
			if i >= 5 {
				break
			}
			sample = append(sample, src.Name)
		}
		logger.Info("connectivity test ok", "duration_ms", dur, "sources", len(o.sources.Flat))
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "sources": len(o.sources.Flat), "sample": sample,
			"duration_ms": dur, "tenant": id, "engine": engine,
		})
	case <-ctx.Done():
		dur := time.Since(start).Milliseconds()
		logger.Warn("connectivity test timed out", "duration_ms", dur)
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": false, "error": "timeout after " + timeout.String(), "duration_ms": dur,
			"tenant": id, "engine": engine,
		})
	}
}

func (s *Server) handleTools(w http.ResponseWriter, r *http.Request) {
	tools := []string{}
	if s.toolsFn != nil {
		tools = s.toolsFn()
	}
	writeJSON(w, http.StatusOK, map[string]any{"tools": tools})
}

func (s *Server) afterWrite(w http.ResponseWriter) {
	if err := s.mgr.Reload(); err != nil {
		// Reload errors for individual tenants are non-fatal; report them.
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "warning": err.Error()})
		return
	}
	s.mgr.MarkDataVersion()
	if s.onChange != nil {
		s.onChange()
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func toView(e store.Tenant) tenantView {
	return tenantView{
		ID:        e.ID,
		Name:      e.Name,
		Type:      e.Type,
		Enabled:   e.Enabled,
		Config:    e.Config,
		HasCreds:  len(e.CredsEnc) > 0,
		Masked:    maskCreds(len(e.CredsEnc) > 0),
		CreatedAt: e.CreatedAt,
		UpdatedAt: e.UpdatedAt,
	}
}

func maskCreds(has bool) string {
	if has {
		return "****"
	}
	return ""
}

func normalizeConfig(c json.RawMessage) json.RawMessage {
	if len(c) == 0 {
		return json.RawMessage(`{}`)
	}
	return c
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]any{"error": err.Error()})
}

func statusFor(err error) int {
	switch {
	case err == store.ErrNotFound:
		return http.StatusNotFound
	case err == store.ErrConflict:
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
