// Package opensearch adapts OpenSearch to the unified SDK.
package opensearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	ossdk "github.com/opensearch-project/opensearch-go/v4"

	"github.com/hchw/log-trace-mcp/internal/sdk"
)

// Config is the non-secret OpenSearch tenant configuration.
type Config struct {
	Endpoint        string `json:"endpoint"`
	Index           string `json:"index"`
	TimeField       string `json:"time_field"`
	AuthKind        string `json:"auth_kind"` // basic | header | none
	HeaderKey       string `json:"header_key"`
	TLSSkipVerify   bool   `json:"tls_skip_verify"`
	DefaultInterval string `json:"default_interval"`
}

// Creds holds decrypted OpenSearch credentials.
type Creds struct {
	Username    string `json:"username,omitempty"`
	Password    string `json:"password,omitempty"`
	HeaderValue string `json:"header_value,omitempty"`
}

// Adapter implements sdk.Adapter for OpenSearch.
type Adapter struct {
	client  *ossdk.Client
	index   string
	timeFld string
}

// New builds an OpenSearch adapter.
func New(cfg Config, creds Creds) (*Adapter, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return nil, fmt.Errorf("opensearch config requires endpoint")
	}
	oc := ossdk.Config{
		Addresses:          []string{strings.TrimSpace(cfg.Endpoint)},
		InsecureSkipVerify: cfg.TLSVerifyDisabled(),
	}
	switch cfg.AuthKind {
	case "", "none":
	case "basic":
		oc.Username = creds.Username
		oc.Password = creds.Password
	case "header":
		if cfg.HeaderKey != "" {
			oc.Header = http.Header{cfg.HeaderKey: []string{creds.HeaderValue}}
		}
	default:
		return nil, fmt.Errorf("unsupported auth_kind %q", cfg.AuthKind)
	}
	client, err := ossdk.NewClient(oc)
	if err != nil {
		return nil, err
	}
	tf := cfg.TimeField
	if tf == "" {
		tf = "@timestamp"
	}
	return &Adapter{client: client, index: cfg.Index, timeFld: tf}, nil
}

// TLSVerifyDisabled reports whether TLS verification should be skipped.
func (c Config) TLSVerifyDisabled() bool { return c.TLSSkipVerify }

// Close closes idle connections.
func (a *Adapter) Close() error { return a.client.Close() }

func (a *Adapter) resolve(source string) string {
	if strings.TrimSpace(source) != "" {
		return strings.TrimSpace(source)
	}
	return a.index
}

// request performs an HTTP call against the cluster and decodes the response.
func (a *Adapter) request(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.client.Stream(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("opensearch %s %s: status %d: %s", method, path, resp.StatusCode, truncate(string(data), 500))
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode opensearch response: %w", err)
		}
	}
	return nil
}

// ListSources lists indices in the cluster.
func (a *Adapter) ListSources(ctx context.Context, parent string) (sdk.Sources, error) {
	var entries []map[string]any
	if err := a.request(ctx, http.MethodGet, "/_cat/indices?format=json", nil, &entries); err != nil {
		return sdk.Sources{}, err
	}
	var tree []sdk.Source
	for _, e := range entries {
		name, _ := e["index"].(string)
		if name == "" {
			continue
		}
		tree = append(tree, sdk.Source{Name: name, Kind: "index"})
	}
	return sdk.Sources{Tree: tree, Flat: tree, Raw: entries}, nil
}

// DescribeSchema returns the mapping fields of an index. project is ignored.
func (a *Adapter) DescribeSchema(ctx context.Context, project, source string) (sdk.Schema, error) {
	index := a.resolve(source)
	if index == "" {
		return sdk.Schema{}, fmt.Errorf("source index is required")
	}
	var resp map[string]any
	if err := a.request(ctx, http.MethodGet, "/"+url.PathEscape(index)+"/_mapping", nil, &resp); err != nil {
		return sdk.Schema{}, err
	}
	var fields []sdk.Field
	for _, idxAny := range resp {
		idx, _ := idxAny.(map[string]any)
		mappings, _ := idx["mappings"].(map[string]any)
		props, _ := mappings["properties"].(map[string]any)
		collectFields("", props, &fields)
	}
	return sdk.Schema{Fields: fields, Raw: resp}, nil
}

func collectFields(prefix string, props map[string]any, out *[]sdk.Field) {
	for name, v := range props {
		m, _ := v.(map[string]any)
		typ, _ := m["type"].(string)
		full := name
		if prefix != "" {
			full = prefix + "." + name
		}
		if sub, ok := m["properties"].(map[string]any); ok {
			collectFields(full, sub, out)
			continue
		}
		*out = append(*out, sdk.Field{Name: full, Type: typ})
	}
}

// Search runs a unified semantic query.
func (a *Adapter) Search(ctx context.Context, req sdk.SearchReq) (sdk.Result, error) {
	index := a.resolve(req.Source)
	if index == "" {
		return sdk.Result{}, fmt.Errorf("source index is required")
	}
	body := a.searchBody(req)
	var resp map[string]any
	if err := a.request(ctx, http.MethodPost, "/"+url.PathEscape(index)+"/_search", body, &resp); err != nil {
		return sdk.Result{}, err
	}
	return a.searchResult(index, resp), nil
}

// Count returns the number of matching documents.
func (a *Adapter) Count(ctx context.Context, req sdk.SearchReq) (int64, error) {
	index := a.resolve(req.Source)
	if index == "" {
		return 0, fmt.Errorf("source index is required")
	}
	body := map[string]any{"query": a.queryClause(req)}
	var resp map[string]any
	if err := a.request(ctx, http.MethodPost, "/"+url.PathEscape(index)+"/_count", body, &resp); err != nil {
		return 0, err
	}
	return toInt64(resp["count"]), nil
}

// Analyze runs histogram and terms aggregations.
func (a *Adapter) Analyze(ctx context.Context, req sdk.AnalyzeReq) (sdk.AnalyzeResult, error) {
	index := a.resolve(req.Source)
	if index == "" {
		return sdk.AnalyzeResult{}, fmt.Errorf("source index is required")
	}
	out := sdk.AnalyzeResult{}
	raw := map[string]any{}

	histBody := map[string]any{
		"size":  0,
		"query": a.queryClause(req.SearchReq),
		"aggs": map[string]any{
			"hist": map[string]any{
				"date_histogram": map[string]any{"field": a.timeFld, "fixed_interval": "1m"},
			},
		},
	}
	var histResp map[string]any
	if err := a.request(ctx, http.MethodPost, "/"+url.PathEscape(index)+"/_search", histBody, &histResp); err != nil {
		return sdk.AnalyzeResult{}, err
	}
	out.Histogram = extractBuckets(histResp, "hist", true)
	raw["histogram"] = histResp

	if strings.TrimSpace(req.GroupBy) != "" {
		topBody := map[string]any{
			"size":  0,
			"query": a.queryClause(req.SearchReq),
			"aggs": map[string]any{
				"top": map[string]any{"terms": map[string]any{"field": req.GroupBy, "size": 100}},
			},
		}
		var topResp map[string]any
		if err := a.request(ctx, http.MethodPost, "/"+url.PathEscape(index)+"/_search", topBody, &topResp); err != nil {
			return sdk.AnalyzeResult{}, err
		}
		out.Top = extractBuckets(topResp, "top", false)
		raw["top"] = topResp
	}
	out.Raw = raw
	return out, nil
}

// RawRequest issues an arbitrary engine-specific request. It backs the
// OpenSearch cluster operations tools.
func (a *Adapter) RawRequest(ctx context.Context, method, path string, body any) (any, error) {
	var resp any
	if err := a.request(ctx, method, path, body, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// NativeQuery passes a DSL or PPL query through to OpenSearch.
func (a *Adapter) NativeQuery(ctx context.Context, req sdk.NativeReq) (sdk.Result, error) {
	index := a.resolve(req.Source)
	switch req.Language {
	case "dsl":
		if index == "" {
			return sdk.Result{}, fmt.Errorf("source index is required for dsl")
		}
		var body any
		if err := decodeRaw(req.Raw, &body); err != nil {
			return sdk.Result{}, err
		}
		var resp map[string]any
		if err := a.request(ctx, http.MethodPost, "/"+url.PathEscape(index)+"/_search", body, &resp); err != nil {
			return sdk.Result{}, err
		}
		return a.searchResult(index, resp), nil

	case "ppl":
		query, ok := req.Raw.(string)
		if !ok {
			return sdk.Result{}, fmt.Errorf("ppl raw must be a string")
		}
		var resp map[string]any
		if err := a.request(ctx, http.MethodPost, "/_plugins/_ppl", map[string]any{"query": query}, &resp); err != nil {
			return sdk.Result{}, err
		}
		return pplResult(index, resp), nil

	default:
		return sdk.Result{}, fmt.Errorf("unsupported opensearch language %q", req.Language)
	}
}

func (a *Adapter) searchBody(req sdk.SearchReq) map[string]any {
	limit := req.Limit
	if limit <= 0 {
		limit = 50
	}
	order := "desc"
	if req.Sort == "asc" {
		order = "asc"
	}
	return map[string]any{
		"query": a.queryClause(req),
		"sort":  []any{map[string]any{a.timeFld: map[string]any{"order": order, "unmapped_type": "date"}}},
		"from":  req.Offset,
		"size":  limit,
	}
}

func (a *Adapter) queryClause(req sdk.SearchReq) map[string]any {
	var must []any
	if strings.TrimSpace(req.Text) != "" {
		must = append(must, map[string]any{
			"multi_match": map[string]any{"query": req.Text, "fields": []string{"*"}},
		})
	}
	var filter []any
	filter = append(filter, map[string]any{
		"range": map[string]any{
			a.timeFld: map[string]any{
				"gte": req.From.Format(time.RFC3339),
				"lt":  req.To.Format(time.RFC3339),
			},
		},
	})
	for _, k := range sdk.SortedKeys(req.Filter) {
		filter = append(filter, map[string]any{"term": map[string]any{k: req.Filter[k]}})
	}
	boolQ := map[string]any{"filter": filter}
	if len(must) > 0 {
		boolQ["must"] = must
	}
	return map[string]any{"bool": boolQ}
}

func (a *Adapter) searchResult(source string, resp map[string]any) sdk.Result {
	hits, _ := resp["hits"].(map[string]any)
	total := int64(0)
	if t, ok := hits["total"].(map[string]any); ok {
		total = toInt64(t["value"])
	}
	hitList, _ := hits["hits"].([]any)
	records := make([]map[string]string, 0, len(hitList))
	for _, h := range hitList {
		hm, _ := h.(map[string]any)
		src, _ := hm["_source"].(map[string]any)
		records = append(records, flatten(src))
	}
	cols, rows := sdk.NormalizeRecords(records)
	meta := map[string]any{"count": total}
	if took, ok := resp["took"]; ok {
		meta["took"] = took
	}
	return sdk.Result{
		Engine:     string(sdk.EngineOpenSearch),
		Source:     source,
		Normalized: true,
		Columns:    cols,
		Rows:       rows,
		Meta:       meta,
		Raw:        resp,
	}
}

func pplResult(source string, resp map[string]any) sdk.Result {
	colsAny, _ := resp["schema"].([]any)
	var cols []string
	for _, c := range colsAny {
		cm, _ := c.(map[string]any)
		if n, ok := cm["name"].(string); ok {
			cols = append(cols, n)
		}
	}
	rowsAny, _ := resp["datarows"].([]any)
	rows := make([][]any, 0, len(rowsAny))
	for _, r := range rowsAny {
		if arr, ok := r.([]any); ok {
			rows = append(rows, arr)
		}
	}
	return sdk.Result{
		Engine:     string(sdk.EngineOpenSearch),
		Source:     source,
		Normalized: true,
		Columns:    cols,
		Rows:       rows,
		Meta:       map[string]any{"count": len(rows)},
		Raw:        resp,
	}
}

func extractBuckets(resp map[string]any, agg string, ts bool) []sdk.Bucket {
	aggs, _ := resp["aggregations"].(map[string]any)
	a, _ := aggs[agg].(map[string]any)
	list, _ := a["buckets"].([]any)
	out := make([]sdk.Bucket, 0, len(list))
	for _, b := range list {
		bm, _ := b.(map[string]any)
		key := ""
		if ts {
			if s, ok := bm["key_as_string"].(string); ok {
				key = s
			} else {
				key = fmt.Sprint(bm["key"])
			}
		} else {
			key = fmt.Sprint(bm["key"])
		}
		out = append(out, sdk.Bucket{Key: key, Count: toInt64(bm["doc_count"])})
	}
	return out
}

func flatten(src map[string]any) map[string]string {
	out := make(map[string]string, len(src))
	for k, v := range src {
		switch t := v.(type) {
		case string:
			out[k] = t
		case nil:
			out[k] = ""
		default:
			b, err := json.Marshal(t)
			if err != nil {
				out[k] = fmt.Sprint(t)
			} else {
				out[k] = string(b)
			}
		}
	}
	return out
}

func decodeRaw(raw any, out *any) error {
	switch v := raw.(type) {
	case nil:
		return fmt.Errorf("raw query is required")
	case string:
		return json.Unmarshal([]byte(v), out)
	case json.RawMessage:
		return json.Unmarshal(v, out)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		return json.Unmarshal(b, out)
	}
}

func toInt64(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	default:
		return 0
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
