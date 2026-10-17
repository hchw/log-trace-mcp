// Package mcpserver registers the unified log query tools on an MCP server.
package mcpserver

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	mcp "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/hchw/log-trace-mcp/internal/sdk"
)

const (
	// instructions are advertised to MCP clients on initialize and frame how the
	// model should reason about tenants before calling any tool.
	instructions = `Query logs across Aliyun SLS and OpenSearch through a single unified interface.

A "tenant" is a configured log-center connection: one credential set with its own permission scope. The same system commonly has separate tenants for different environments (for example "prod" and "test"), which may expose different data and allow different operations. Every tool requires a tenant, and results depend on it. If the user has not named a tenant, ask which one to use before querying - do not guess.

Inside a tenant, a "project" is an SLS project (ignored by OpenSearch) and a "source" is a logstore (SLS) or an index (OpenSearch). Use list_sources to discover a tenant's projects, then list_sources again with project to discover its logstores.

The convert_time tool converts a clock time between time zones (no tenant needed). Use it whenever a time is stated in one zone and the result is needed in another, and ask the user for the source zone when it is ambiguous.`

	tenantDesc    = "Tenant id: a configured log-center connection, i.e. one credential set with its own permission scope. Different tenants usually map to different environments (such as prod or test) and may see different data or allow different operations. Required for every tool. If the user did not specify a tenant, ask which one (prod or test) before querying - do not guess."
	projectDesc   = "SLS project to query. Optional when source is given as \"project/logstore\". Ignored by OpenSearch. Discover values with list_sources."
	sourceDesc    = "Logstore (SLS) or index (OpenSearch) to query. Discover values with list_sources."
	sourceSLSDesc = "Logstore (SLS only). Discover values with list_sources."

	timeRangeDesc = "Time window as one expression: last_1h, today, now-15m~now-5m, unix seconds, or a datetime such as 2026-10-17 12:26:00. Its start/end are overridden by from/to when those are given."
	fromDesc      = "Explicit window start. Overrides time_range's start. Accepts unix seconds/milliseconds or a datetime (2026-10-17 12:26:00, 2026-10-17 12:26, 2026-10-17)."
	toDesc        = "Explicit window end. Overrides time_range's end. Accepts the same forms as from."
)

// Server owns the MCP server and tool registration.
type Server struct {
	svc *sdk.Service
	reg *sdk.Registry
	m   *mcpserver.MCPServer

	mu     sync.Mutex
	engine []string // names of currently registered Tier 3 tools
}

// New builds a Server.
func New(svc *sdk.Service, reg *sdk.Registry, name, version string) *Server {
	m := mcpserver.NewMCPServer(name, version,
		mcpserver.WithToolCapabilities(true),
		mcpserver.WithInstructions(instructions),
		mcpserver.WithRecovery(),
	)
	return &Server{svc: svc, reg: reg, m: m}
}

// MCPServer exposes the underlying MCP server for transports.
func (s *Server) MCPServer() *mcpserver.MCPServer { return s.m }

// Register installs the always-on Tier 1 and Tier 2 tools.
func (s *Server) Register() {
	s.m.AddTool(mcp.NewTool("list_sources",
		mcp.WithDescription("List the data sources (SLS projects / logstores, OpenSearch indices) available to a tenant. Start here to discover what a tenant can access."),
		mcp.WithString("tenant", mcp.Required(), mcp.Description(tenantDesc)),
		mcp.WithString("project", mcp.Description(projectDesc)),
	), s.handleListSources)

	s.m.AddTool(mcp.NewTool("describe_schema",
		mcp.WithDescription("Describe the field structure of a data set within a tenant (SLS index keys, OpenSearch mapping)."),
		mcp.WithString("tenant", mcp.Required(), mcp.Description(tenantDesc)),
		mcp.WithString("project", mcp.Description(projectDesc)),
		mcp.WithString("source", mcp.Required(), mcp.Description(sourceDesc)),
	), s.handleDescribeSchema)

	s.m.AddTool(mcp.NewTool("search_logs",
		mcp.WithDescription("Search logs within one tenant using a keyword, field filters and a unified time range."),
		mcp.WithString("tenant", mcp.Required(), mcp.Description(tenantDesc)),
		mcp.WithString("project", mcp.Description(projectDesc)),
		mcp.WithString("source", mcp.Description(sourceDesc)),
		mcp.WithString("text", mcp.Description("Free-text keyword")),
		mcp.WithObject("filter", mcp.Description("Field equality/contains filters, e.g. {\"level\":\"ERROR\"}")),
		mcp.WithString("time_range", mcp.Description(timeRangeDesc)),
		mcp.WithString("from", mcp.Description(fromDesc)),
		mcp.WithString("to", mcp.Description(toDesc)),
		mcp.WithNumber("limit", mcp.Description("Max rows (default 50)")),
		mcp.WithNumber("offset", mcp.Description("Row offset")),
		mcp.WithString("sort", mcp.Description("desc (default) or asc")),
	), s.handleSearchLogs)

	s.m.AddTool(mcp.NewTool("count_logs",
		mcp.WithDescription("Count logs within one tenant matching a keyword, filters and time range."),
		mcp.WithString("tenant", mcp.Required(), mcp.Description(tenantDesc)),
		mcp.WithString("project", mcp.Description(projectDesc)),
		mcp.WithString("source", mcp.Description(sourceDesc)),
		mcp.WithString("text", mcp.Description("Free-text keyword")),
		mcp.WithObject("filter", mcp.Description("Field filters")),
		mcp.WithString("time_range", mcp.Description(timeRangeDesc)),
		mcp.WithString("from", mcp.Description(fromDesc)),
		mcp.WithString("to", mcp.Description(toDesc)),
	), s.handleCountLogs)

	s.m.AddTool(mcp.NewTool("compare_logs",
		mcp.WithDescription("Compare two time windows within one tenant and report the difference."),
		mcp.WithString("tenant", mcp.Required(), mcp.Description(tenantDesc)),
		mcp.WithString("project", mcp.Description(projectDesc)),
		mcp.WithString("source", mcp.Description(sourceDesc)),
		mcp.WithString("text", mcp.Description("Free-text keyword")),
		mcp.WithObject("filter", mcp.Description("Field filters")),
		mcp.WithString("range_a", mcp.Required(), mcp.Description("First time range")),
		mcp.WithString("range_b", mcp.Required(), mcp.Description("Second time range")),
		mcp.WithNumber("limit", mcp.Description("Max rows per window")),
	), s.handleCompareLogs)

	s.m.AddTool(mcp.NewTool("analyze",
		mcp.WithDescription("Aggregate logs within one tenant: time histogram and optional top values of a field."),
		mcp.WithString("tenant", mcp.Required(), mcp.Description(tenantDesc)),
		mcp.WithString("project", mcp.Description(projectDesc)),
		mcp.WithString("source", mcp.Description(sourceDesc)),
		mcp.WithString("text", mcp.Description("Free-text keyword")),
		mcp.WithObject("filter", mcp.Description("Field filters")),
		mcp.WithString("time_range", mcp.Description(timeRangeDesc)),
		mcp.WithString("from", mcp.Description(fromDesc)),
		mcp.WithString("to", mcp.Description(toDesc)),
		mcp.WithString("group_by", mcp.Description("Field to group by for top values")),
	), s.handleAnalyze)

	s.m.AddTool(mcp.NewTool("get_context",
		mcp.WithDescription("Return logs surrounding a point in an SLS logstore. Give an anchor from a search hit (pack_id + pack_meta) for exact native context, or a time to get a window around that instant."),
		mcp.WithString("tenant", mcp.Required(), mcp.Description(tenantDesc)),
		mcp.WithString("project", mcp.Description(projectDesc)),
		mcp.WithString("source", mcp.Description(sourceSLSDesc)),
		mcp.WithObject("anchor", mcp.Description("Native anchor from a search hit, e.g. {\"pack_id\":\"...\",\"pack_meta\":\"...\"}")),
		mcp.WithString("time", mcp.Description("Fallback anchor time; returns a window around this instant when pack tags are unavailable")),
		mcp.WithString("window", mcp.Description("Window radius for time mode (default 5m)")),
		mcp.WithNumber("before", mcp.Description("Lines before (native mode) / part of the limit (time mode)")),
		mcp.WithNumber("after", mcp.Description("Lines after (native mode) / part of the limit (time mode)")),
	), s.handleGetContext)

	s.m.AddTool(mcp.NewTool("native_query",
		mcp.WithDescription("Pass a native query through to one tenant's backend. Valid languages: SLS {sql,spl}, OpenSearch {dsl,ppl}."),
		mcp.WithString("tenant", mcp.Required(), mcp.Description(tenantDesc)),
		mcp.WithString("project", mcp.Description(projectDesc)),
		mcp.WithString("source", mcp.Description(sourceDesc)),
		mcp.WithString("language", mcp.Required(), mcp.Description("sql | spl | dsl | ppl")),
		mcp.WithString("query", mcp.Description("Query text for sql/spl/ppl")),
		mcp.WithObject("body", mcp.Description("Query body object for dsl")),
		mcp.WithString("time_range", mcp.Description(timeRangeDesc)),
		mcp.WithString("from", mcp.Description(fromDesc)),
		mcp.WithString("to", mcp.Description(toDesc)),
		mcp.WithNumber("limit", mcp.Description("Max rows")),
		mcp.WithNumber("offset", mcp.Description("Row offset")),
	), s.handleNativeQuery)

	s.m.AddTool(mcp.NewTool("convert_time",
		mcp.WithDescription("Convert a clock time from one time zone to another, with no tenant or data access. Use when a time is stated in one zone and the user needs the corresponding time in another zone."),
		mcp.WithString("time", mcp.Required(), mcp.Description("Time to convert: a wall-clock datetime (2026-10-17 12:26:00, 2026-10-17 12:26, 2026-10-17), an RFC3339 value with offset, or unix seconds/milliseconds.")),
		mcp.WithString("from_tz", mcp.Required(), mcp.Description("Zone the time is expressed in: IANA name such as Asia/Shanghai or UTC, or a fixed offset such as +08:00.")),
		mcp.WithString("to_tz", mcp.Required(), mcp.Description("Target zone: IANA name such as Asia/Shanghai or UTC, or a fixed offset such as -05:00.")),
	), s.handleConvertTime)
}

// Refresh registers or removes Tier 3 engine-exclusive tools based on which
// tenant types are currently present.
func (s *Server) Refresh() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.engine) > 0 {
		s.m.DeleteTools(s.engine...)
		s.engine = nil
	}

	hasOS := false
	for _, id := range s.reg.IDs() {
		if t, _ := s.reg.TypeOf(id); t == sdk.EngineOpenSearch {
			hasOS = true
		}
	}

	if hasOS {
		s.addOSClusterTools()
	}
}

// RegisteredTools returns the names of currently registered tools.
func (s *Server) RegisteredTools() []string {
	names := make([]string, 0, len(s.m.ListTools()))
	for name := range s.m.ListTools() {
		names = append(names, name)
	}
	return names
}

func (s *Server) addOSClusterTools() {
	s.m.AddTool(mcp.NewTool("os_cluster_health",
		mcp.WithDescription("OpenSearch only: returns cluster health for a tenant."),
		mcp.WithString("tenant", mcp.Required(), mcp.Description(tenantDesc)),
	), s.handleOSClusterHealth)
	s.m.AddTool(mcp.NewTool("os_shards",
		mcp.WithDescription("OpenSearch only: returns shard information for a tenant."),
		mcp.WithString("tenant", mcp.Required(), mcp.Description(tenantDesc)),
	), s.handleOSShards)
	s.m.AddTool(mcp.NewTool("os_explain",
		mcp.WithDescription("OpenSearch only: explains why a document matches, within one tenant."),
		mcp.WithString("tenant", mcp.Required(), mcp.Description(tenantDesc)),
		mcp.WithString("index", mcp.Required(), mcp.Description("Index name")),
		mcp.WithString("id", mcp.Required(), mcp.Description("Document id")),
	), s.handleOSExplain)
	s.engine = append(s.engine, "os_cluster_health", "os_shards", "os_explain")
}

// --- handlers ---

func (s *Server) handleListSources(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tenant, err := request.RequireString("tenant")
	if err != nil {
		return errResult(err), nil
	}
	res, err := s.svc.ListSources(ctx, tenant, request.GetString("project", ""))
	if err != nil {
		return errResult(err), nil
	}
	return jsonResult(res)
}

func (s *Server) handleDescribeSchema(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tenant, err := request.RequireString("tenant")
	if err != nil {
		return errResult(err), nil
	}
	source, err := request.RequireString("source")
	if err != nil {
		return errResult(err), nil
	}
	res, err := s.svc.DescribeSchema(ctx, tenant, request.GetString("project", ""), source)
	if err != nil {
		return errResult(err), nil
	}
	return jsonResult(res)
}

func (s *Server) handleSearchLogs(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	req, err := s.searchReq(request)
	if err != nil {
		return errResult(err), nil
	}
	res, err := s.svc.Search(ctx, req)
	if err != nil {
		return errResult(err), nil
	}
	return jsonResult(res)
}

func (s *Server) handleCountLogs(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	req, err := s.searchReq(request)
	if err != nil {
		return errResult(err), nil
	}
	n, err := s.svc.Count(ctx, req)
	if err != nil {
		return errResult(err), nil
	}
	return jsonResult(map[string]any{"count": n, "tenant": req.Tenant, "project": req.Project, "source": req.Source})
}

func (s *Server) handleCompareLogs(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	req, err := s.searchReq(request)
	if err != nil {
		return errResult(err), nil
	}
	rangeA, err := request.RequireString("range_a")
	if err != nil {
		return errResult(err), nil
	}
	rangeB, err := request.RequireString("range_b")
	if err != nil {
		return errResult(err), nil
	}
	fromA, toA, err := sdk.ParseTimeRange(rangeA)
	if err != nil {
		return errResult(err), nil
	}
	fromB, toB, err := sdk.ParseTimeRange(rangeB)
	if err != nil {
		return errResult(err), nil
	}
	reqA, reqB := req, req
	reqA.From, reqA.To = fromA, toA
	reqB.From, reqB.To = fromB, toB
	res, err := s.svc.Compare(ctx, reqA, reqB)
	if err != nil {
		return errResult(err), nil
	}
	return jsonResult(res)
}

func (s *Server) handleAnalyze(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	req, err := s.searchReq(request)
	if err != nil {
		return errResult(err), nil
	}
	res, err := s.svc.Analyze(ctx, sdk.AnalyzeReq{SearchReq: req, GroupBy: request.GetString("group_by", "")})
	if err != nil {
		return errResult(err), nil
	}
	return jsonResult(res)
}

func (s *Server) handleGetContext(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tenant, err := request.RequireString("tenant")
	if err != nil {
		return errResult(err), nil
	}
	args := request.GetArguments()
	anchor := toMapStringString(args["anchor"])

	packID := firstNonEmpty(anchor["pack_id"], anchor["__tag__:__pack_id__"])
	packMeta := firstNonEmpty(anchor["pack_meta"], anchor["__tag__:__pack_meta__"])
	if packID != "" && packMeta != "" {
		res, err := s.svc.ContextLogs(ctx, sdk.ContextReq{
			Tenant:  tenant,
			Project: request.GetString("project", ""),
			Source:  request.GetString("source", ""),
			Anchor:  anchor,
			Before:  request.GetInt("before", 10),
			After:   request.GetInt("after", 10),
		})
		if err != nil {
			return errResult(err), nil
		}
		return jsonResult(map[string]any{"mode": "native", "result": res})
	}

	// Fallback: a time-window context around an instant. SLS only supports native
	// context for logs that carry pack tags, which PutLogs-written logs do not.
	timeExpr := request.GetString("time", "")
	if timeExpr == "" {
		timeExpr = anchor["time"]
	}
	if timeExpr == "" {
		return errResult(fmt.Errorf("provide anchor {pack_id,pack_meta} for native context, or time for a time-window context")), nil
	}
	anchorTime, err := sdk.ParseTimePoint(timeExpr)
	if err != nil {
		return errResult(err), nil
	}
	window := 5 * time.Minute
	if w := request.GetString("window", ""); w != "" {
		if d, err := time.ParseDuration(w); err == nil && d > 0 {
			window = d
		} else if err != nil {
			return errResult(fmt.Errorf("invalid window %q: %w", w, err)), nil
		}
	}
	req := sdk.SearchReq{
		Tenant:  tenant,
		Project: request.GetString("project", ""),
		Source:  request.GetString("source", ""),
		From:    anchorTime.Add(-window),
		To:      anchorTime.Add(window),
		Limit:   request.GetInt("before", 10) + request.GetInt("after", 10) + 1,
		Sort:    "asc",
	}
	res, err := s.svc.Search(ctx, req)
	if err != nil {
		return errResult(err), nil
	}
	return jsonResult(map[string]any{
		"mode": "time_window", "anchor_time": anchorTime, "window": window.String(), "result": res,
	})
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func (s *Server) handleNativeQuery(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tenant, err := request.RequireString("tenant")
	if err != nil {
		return errResult(err), nil
	}
	language, err := request.RequireString("language")
	if err != nil {
		return errResult(err), nil
	}
	args := request.GetArguments()
	req := sdk.NativeReq{
		Tenant:   tenant,
		Project:  request.GetString("project", ""),
		Source:   request.GetString("source", ""),
		Language: strings.ToLower(language),
		Limit:    request.GetInt("limit", 100),
		Offset:   request.GetInt("offset", 0),
	}
	if body, ok := args["body"]; ok && body != nil {
		req.Raw = body
	} else {
		req.Raw = request.GetString("query", "")
	}
	if tr, fe, te := request.GetString("time_range", ""), request.GetString("from", ""), request.GetString("to", ""); tr != "" || fe != "" || te != "" {
		f, t, err := sdk.ResolveWindow(tr, fe, te)
		if err != nil {
			return errResult(err), nil
		}
		req.From, req.To, req.HasTime = f, t, true
	}
	res, err := s.svc.NativeQuery(ctx, req)
	if err != nil {
		return errResult(err), nil
	}
	return jsonResult(res)
}

func (s *Server) handleConvertTime(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	timeExpr, err := request.RequireString("time")
	if err != nil {
		return errResult(err), nil
	}
	fromTZ, err := request.RequireString("from_tz")
	if err != nil {
		return errResult(err), nil
	}
	toTZ, err := request.RequireString("to_tz")
	if err != nil {
		return errResult(err), nil
	}
	res, err := sdk.ConvertTime(timeExpr, fromTZ, toTZ)
	if err != nil {
		return errResult(err), nil
	}
	return jsonResult(res)
}

func (s *Server) handleOSClusterHealth(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	raw, err := s.svc.RawRequest(ctx, request.GetString("tenant", ""), http.MethodGet, "/_cluster/health", nil)
	if err != nil {
		return errResult(err), nil
	}
	return jsonResult(raw)
}

func (s *Server) handleOSShards(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	raw, err := s.svc.RawRequest(ctx, request.GetString("tenant", ""), http.MethodGet, "/_cat/shards?format=json", nil)
	if err != nil {
		return errResult(err), nil
	}
	return jsonResult(raw)
}

func (s *Server) handleOSExplain(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tenant, err := request.RequireString("tenant")
	if err != nil {
		return errResult(err), nil
	}
	index, err := request.RequireString("index")
	if err != nil {
		return errResult(err), nil
	}
	id, err := request.RequireString("id")
	if err != nil {
		return errResult(err), nil
	}
	path := fmt.Sprintf("/%s/_explain/%s", index, id)
	raw, err := s.svc.RawRequest(ctx, tenant, http.MethodGet, path, nil)
	if err != nil {
		return errResult(err), nil
	}
	return jsonResult(raw)
}

// --- helpers ---

func (s *Server) searchReq(request mcp.CallToolRequest) (sdk.SearchReq, error) {
	var req sdk.SearchReq
	tenant, err := request.RequireString("tenant")
	if err != nil {
		return req, err
	}
	req.Tenant = tenant
	req.Project = request.GetString("project", "")
	req.Source = request.GetString("source", "")
	req.Text = request.GetString("text", "")
	req.Filter = toMapStringString(request.GetArguments()["filter"])
	req.Limit = request.GetInt("limit", 50)
	req.Offset = request.GetInt("offset", 0)
	req.Sort = strings.ToLower(request.GetString("sort", "desc"))
	from, to, err := sdk.ResolveWindow(request.GetString("time_range", ""), request.GetString("from", ""), request.GetString("to", ""))
	if err != nil {
		return req, err
	}
	req.From, req.To = from, to
	return req, nil
}

func toMapStringString(v any) map[string]string {
	out := map[string]string{}
	m, ok := v.(map[string]any)
	if !ok {
		return out
	}
	for k, val := range m {
		switch t := val.(type) {
		case string:
			out[k] = t
		default:
			out[k] = fmt.Sprint(t)
		}
	}
	return out
}

func jsonResult(v any) (*mcp.CallToolResult, error) {
	res, err := mcp.NewToolResultJSON(v)
	if err != nil {
		return errResult(err), nil
	}
	return res, nil
}

func errResult(err error) *mcp.CallToolResult {
	return mcp.NewToolResultError(err.Error())
}
