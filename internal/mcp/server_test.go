package mcpserver

import (
	"context"
	"testing"

	mcp "github.com/mark3labs/mcp-go/mcp"

	"github.com/hchw/log-trace-mcp/internal/sdk"
)

type fakeAdapter struct {
	engine sdk.EngineType
}

func (f *fakeAdapter) ListSources(ctx context.Context, parent string) (sdk.Sources, error) {
	return sdk.Sources{Flat: []sdk.Source{{Name: "x", Kind: "index"}}}, nil
}
func (f *fakeAdapter) DescribeSchema(ctx context.Context, project, source string) (sdk.Schema, error) {
	return sdk.Schema{Fields: []sdk.Field{{Name: "msg", Type: "text"}}}, nil
}
func (f *fakeAdapter) Search(ctx context.Context, req sdk.SearchReq) (sdk.Result, error) {
	return sdk.Result{Engine: string(f.engine), Source: req.Source, Normalized: true,
		Columns: []string{"msg"}, Rows: [][]any{{"hello"}}}, nil
}
func (f *fakeAdapter) Count(ctx context.Context, req sdk.SearchReq) (int64, error) { return 42, nil }
func (f *fakeAdapter) NativeQuery(ctx context.Context, req sdk.NativeReq) (sdk.Result, error) {
	return sdk.Result{Engine: string(f.engine), Raw: req.Raw, Normalized: false}, nil
}
func (f *fakeAdapter) Close() error { return nil }

func newTestServer(tenants map[string]sdk.EngineType) *Server {
	reg := sdk.NewRegistry()
	items := map[string]sdk.AdapterInfo{}
	for id, eng := range tenants {
		items[id] = sdk.AdapterInfo{Adapter: &fakeAdapter{engine: eng}, Engine: eng}
	}
	reg.Replace(items)
	svc := sdk.NewService(reg)
	s := New(svc, reg, "test", "0")
	s.Register()
	s.Refresh()
	return s
}

func call(t *testing.T, s *Server, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{Params: mcp.CallToolParams{Name: name, Arguments: args}}
	switch name {
	case "list_sources":
		res, err := s.handleListSources(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	case "search_logs":
		res, err := s.handleSearchLogs(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	case "count_logs":
		res, err := s.handleCountLogs(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	case "native_query":
		res, err := s.handleNativeQuery(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	t.Fatalf("unknown tool %s", name)
	return nil
}

func TestSearchLogs(t *testing.T) {
	s := newTestServer(map[string]sdk.EngineType{"a": sdk.EngineSLS})
	res := call(t, s, "search_logs", map[string]any{"tenant": "a", "source": "p/l", "text": "err", "time_range": "last_1h"})
	if res.IsError {
		t.Fatalf("unexpected error: %+v", res)
	}
}

func TestSearchLogsUnknownEnv(t *testing.T) {
	s := newTestServer(map[string]sdk.EngineType{"a": sdk.EngineSLS})
	res := call(t, s, "search_logs", map[string]any{"tenant": "missing"})
	if !res.IsError {
		t.Fatal("expected error result for unknown tenant")
	}
}

func TestNativeQueryLanguageMismatch(t *testing.T) {
	s := newTestServer(map[string]sdk.EngineType{"os": sdk.EngineOpenSearch})
	res := call(t, s, "native_query", map[string]any{"tenant": "os", "source": "idx", "language": "sql", "query": "SELECT 1"})
	if !res.IsError {
		t.Fatal("expected error for sql on opensearch")
	}
}

func TestEngineToolGating(t *testing.T) {
	// No OS tenant: cluster tools absent.
	s := newTestServer(map[string]sdk.EngineType{"sls": sdk.EngineSLS})
	if hasTool(s, "os_cluster_health") {
		t.Fatal("os tools should not be registered without an OS tenant")
	}
	// Add an OS tenant and refresh.
	s.reg.Replace(map[string]sdk.AdapterInfo{
		"sls": {Adapter: &fakeAdapter{engine: sdk.EngineSLS}, Engine: sdk.EngineSLS},
		"os":  {Adapter: &fakeAdapter{engine: sdk.EngineOpenSearch}, Engine: sdk.EngineOpenSearch},
	})
	s.Refresh()
	if !hasTool(s, "os_cluster_health") {
		t.Fatal("os tools should be registered with an OS tenant")
	}
	// Remove OS tenant, refresh again.
	s.reg.Replace(map[string]sdk.AdapterInfo{
		"sls": {Adapter: &fakeAdapter{engine: sdk.EngineSLS}, Engine: sdk.EngineSLS},
	})
	s.Refresh()
	if hasTool(s, "os_cluster_health") {
		t.Fatal("os tools should be removed after OS tenant disappears")
	}
}

func hasTool(s *Server, name string) bool {
	for _, n := range s.RegisteredTools() {
		if n == name {
			return true
		}
	}
	return false
}
