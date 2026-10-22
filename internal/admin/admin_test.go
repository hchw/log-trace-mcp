package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hchw/log-trace-mcp/internal/crypto"
	"github.com/hchw/log-trace-mcp/internal/manager"
	"github.com/hchw/log-trace-mcp/internal/sdk"
	"github.com/hchw/log-trace-mcp/internal/store"
)

func newAdmin(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "admin.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	key := make([]byte, 32)
	c, _ := crypto.New(key)
	mgr := manager.New(st, c, sdk.NewRegistry(), nil)
	return New(mgr, "secret-token", func() []string { return []string{"search_logs"} }, nil), st
}

func do(t *testing.T, h http.Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAdminPageOffersTLSType(t *testing.T) {
	srv, _ := newAdmin(t)
	rec := do(t, srv.Handler(), "GET", "/", "secret-token", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `value="tls"`) {
		t.Fatal("admin page should offer the tls tenant type")
	}
}

func TestAdminRejectsMissingToken(t *testing.T) {
	srv, _ := newAdmin(t)
	rec := do(t, srv.Handler(), "GET", "/api/tenants", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

func TestAdminCreateAndMask(t *testing.T) {
	srv, st := newAdmin(t)
	h := srv.Handler()

	rec := do(t, h, "POST", "/api/tenants", "secret-token", map[string]any{
		"id":      "prod-sls",
		"type":    "sls",
		"enabled": true,
		"config":  map[string]any{"region": "cn-hangzhou"},
		"creds":   map[string]string{"ak_id": "AK", "ak_secret": "SK"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create failed: %d %s", rec.Code, rec.Body.String())
	}

	e, err := st.GetTenant("prod-sls")
	if err != nil {
		t.Fatal(err)
	}
	if len(e.CredsEnc) == 0 {
		t.Fatal("creds should be encrypted at rest")
	}
	if bytes.Contains(e.CredsEnc, []byte("AK")) || bytes.Contains(e.CredsEnc, []byte("SK")) {
		t.Fatal("plaintext secret found in stored blob")
	}

	rec = do(t, h, "GET", "/api/tenants", "secret-token", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list failed: %d", rec.Code)
	}
	body := rec.Body.String()
	if bytes.Contains([]byte(body), []byte("AK")) || bytes.Contains([]byte(body), []byte("SK")) {
		t.Fatalf("secret leaked in list response: %s", body)
	}
	var views []tenantView
	if err := json.Unmarshal(rec.Body.Bytes(), &views); err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || !views[0].HasCreds {
		t.Fatalf("unexpected views: %+v", views)
	}
}

func TestAdminUpdateKeepsCredsWhenOmitted(t *testing.T) {
	srv, st := newAdmin(t)
	h := srv.Handler()
	_ = do(t, h, "POST", "/api/tenants", "secret-token", map[string]any{
		"id": "e", "type": "sls", "enabled": true, "config": map[string]any{}, "creds": map[string]string{"ak_id": "AK"},
	})
	before, _ := st.GetTenant("e")

	rec := do(t, h, "PUT", "/api/tenants/e", "secret-token", map[string]any{
		"name": "renamed", "type": "sls", "enabled": true, "config": map[string]any{},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("update failed: %d %s", rec.Code, rec.Body.String())
	}
	after, _ := st.GetTenant("e")
	if after.Name != "renamed" {
		t.Fatal("name not updated")
	}
	if string(after.CredsEnc) != string(before.CredsEnc) {
		t.Fatal("creds should be preserved when omitted")
	}
}

func TestAdminDelete(t *testing.T) {
	srv, st := newAdmin(t)
	h := srv.Handler()
	_ = do(t, h, "POST", "/api/tenants", "secret-token", map[string]any{"id": "e", "type": "sls", "enabled": true, "config": map[string]any{}})
	rec := do(t, h, "DELETE", "/api/tenants/e", "secret-token", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete failed: %d", rec.Code)
	}
	if _, err := st.GetTenant("e"); err != store.ErrNotFound {
		t.Fatalf("tenant should be gone, got %v", err)
	}
}

func TestAdminTools(t *testing.T) {
	srv, _ := newAdmin(t)
	rec := do(t, srv.Handler(), "GET", "/api/tools", "secret-token", nil)
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte("search_logs")) {
		t.Fatalf("unexpected tools response: %d %s", rec.Code, rec.Body.String())
	}
}

func loginCookie(t *testing.T, h http.Handler, token string) *http.Cookie {
	t.Helper()
	rec := do(t, h, "POST", "/api/login", "", map[string]string{"token": token})
	if rec.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", rec.Code, rec.Body.String())
	}
	res := rec.Result()
	cookies := res.Cookies()
	for _, c := range cookies {
		if c.Name == sessionCookie && c.HttpOnly {
			return c
		}
	}
	t.Fatalf("no HttpOnly session cookie set: %v", cookies)
	return nil
}

func doWithCookie(t *testing.T, h http.Handler, method, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestPageRedirectsToLoginWhenUnauthenticated(t *testing.T) {
	srv, _ := newAdmin(t)
	rec := doWithCookie(t, srv.Handler(), "GET", "/", nil)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/login" {
		t.Fatalf("want redirect to /login, got %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestLoginWrongToken(t *testing.T) {
	srv, _ := newAdmin(t)
	rec := do(t, srv.Handler(), "POST", "/api/login", "", map[string]string{"token": "wrong"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

func TestLoginCookieGrantsPageAndAPIAccess(t *testing.T) {
	srv, _ := newAdmin(t)
	h := srv.Handler()
	cookie := loginCookie(t, h, "secret-token")

	rec := doWithCookie(t, h, "GET", "/", cookie)
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte("租户列表")) {
		t.Fatalf("page not served with session cookie: %d", rec.Code)
	}
	rec = doWithCookie(t, h, "GET", "/api/tenants", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("api not accessible with session cookie: %d", rec.Code)
	}
}

func TestLogoutInvalidatesSession(t *testing.T) {
	srv, _ := newAdmin(t)
	h := srv.Handler()
	cookie := loginCookie(t, h, "secret-token")

	rec := doWithCookie(t, h, "POST", "/api/logout", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("logout failed: %d", rec.Code)
	}
	rec = doWithCookie(t, h, "GET", "/api/tenants", cookie)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("session should be invalid after logout, got %d", rec.Code)
	}
}

func TestLoginPageShownWhenUnauthenticated(t *testing.T) {
	srv, _ := newAdmin(t)
	rec := doWithCookie(t, srv.Handler(), "GET", "/login", nil)
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte("Admin Token")) {
		t.Fatalf("login page not served: %d", rec.Code)
	}
}

// testAdapter implements sdk.Adapter for connectivity-test coverage.
type testAdapter struct {
	sources sdk.Sources
	err     error
}

func (a *testAdapter) ListSources(ctx context.Context, project string) (sdk.Sources, error) {
	return a.sources, a.err
}
func (a *testAdapter) DescribeSchema(ctx context.Context, project, source string) (sdk.Schema, error) {
	return sdk.Schema{}, nil
}
func (a *testAdapter) Search(ctx context.Context, req sdk.SearchReq) (sdk.Result, error) {
	return sdk.Result{}, nil
}
func (a *testAdapter) Count(ctx context.Context, req sdk.SearchReq) (int64, error) { return 0, nil }
func (a *testAdapter) NativeQuery(ctx context.Context, req sdk.NativeReq) (sdk.Result, error) {
	return sdk.Result{}, nil
}
func (a *testAdapter) Close() error { return nil }

func TestConnTestSuccess(t *testing.T) {
	srv, _ := newAdmin(t)
	srv.mgr.Registry().Replace(map[string]sdk.AdapterInfo{
		"t1": {Adapter: &testAdapter{sources: sdk.Sources{Flat: []sdk.Source{{Name: "a"}, {Name: "b"}}}}, Engine: sdk.EngineSLS},
	})
	rec := do(t, srv.Handler(), "POST", "/api/tenants/t1/test", "secret-token", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["ok"] != true || out["sources"].(float64) != 2 {
		t.Fatalf("unexpected result: %v", out)
	}
	if _, hasDur := out["duration_ms"]; !hasDur {
		t.Fatalf("expected duration_ms in result: %v", out)
	}
}

func TestConnTestFailureIsReported(t *testing.T) {
	srv, _ := newAdmin(t)
	srv.mgr.Registry().Replace(map[string]sdk.AdapterInfo{
		"t1": {Adapter: &testAdapter{err: errors.New("boom")}, Engine: sdk.EngineSLS},
	})
	rec := do(t, srv.Handler(), "POST", "/api/tenants/t1/test", "secret-token", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 with ok:false, got %d", rec.Code)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["ok"] != false || out["error"] != "boom" {
		t.Fatalf("unexpected result: %v", out)
	}
}
