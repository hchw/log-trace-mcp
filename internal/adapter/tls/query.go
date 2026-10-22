package tls

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	tlssdk "github.com/volcengine/volc-sdk-golang/service/tls"

	"github.com/hchw/log-trace-mcp/internal/sdk"
)

// Search runs a unified semantic query against TLS.
func (a *Adapter) Search(ctx context.Context, req sdk.SearchReq) (sdk.Result, error) {
	topicID, err := a.resolve(ctx, req.Project, req.Source)
	if err != nil {
		return sdk.Result{}, err
	}
	from, to := normalizeWindow(req.From, req.To)
	limit := req.Limit
	if limit <= 0 {
		limit = defaultSearchRow
	}
	var offset *int64
	if req.Offset > 0 {
		o := int64(req.Offset)
		offset = &o
	}
	resp, err := a.client.SearchLogsV2(&tlssdk.SearchLogsRequest{
		TopicID:   topicID,
		Query:     buildQuery(req),
		StartTime: from.UnixMilli(),
		EndTime:   to.UnixMilli(),
		Limit:     limit,
		Offset:    offset,
		Sort:      normalizeSort(req.Sort),
	})
	if err != nil {
		return sdk.Result{}, fmt.Errorf("tls search: %w", err)
	}
	return resultFromSearch(req.Source, resp), nil
}

// Count returns the backend's total hit count. SearchLogsV2.HitCount is
// capped by Limit, so the histogram's TotalCount is the reliable total.
func (a *Adapter) Count(ctx context.Context, req sdk.SearchReq) (int64, error) {
	topicID, err := a.resolve(ctx, req.Project, req.Source)
	if err != nil {
		return 0, err
	}
	from, to := normalizeWindow(req.From, req.To)
	resp, err := a.client.DescribeHistogramV1(&tlssdk.DescribeHistogramV1Request{
		TopicID:   topicID,
		Query:     buildQuery(req),
		StartTime: from.UnixMilli(),
		EndTime:   to.UnixMilli(),
		Interval:  histogramInterval(to.Sub(from)),
	})
	if err != nil {
		return 0, fmt.Errorf("tls count: %w", err)
	}
	return resp.TotalCount, nil
}

// NativeQuery passes a TLS query string through to SearchLogsV2. Both "sql"
// and "search" map to the same native query field.
func (a *Adapter) NativeQuery(ctx context.Context, req sdk.NativeReq) (sdk.Result, error) {
	language := strings.ToLower(strings.TrimSpace(req.Language))
	if !sdk.ValidLanguage(sdk.EngineTLS, language) {
		return sdk.Result{}, fmt.Errorf("language %q is not valid for tls; valid: %v", req.Language, sdk.LanguagesFor(sdk.EngineTLS))
	}
	topicID, err := a.resolve(ctx, req.Project, req.Source)
	if err != nil {
		return sdk.Result{}, err
	}
	query, err := rawToString(req.Raw)
	if err != nil {
		return sdk.Result{}, err
	}
	from, to := normalizeWindow(req.From, req.To)
	limit := req.Limit
	if limit <= 0 {
		limit = defaultPageSize
	}
	var offset *int64
	if req.Offset > 0 {
		o := int64(req.Offset)
		offset = &o
	}
	resp, err := a.client.SearchLogsV2(&tlssdk.SearchLogsRequest{
		TopicID:   topicID,
		Query:     query,
		StartTime: from.UnixMilli(),
		EndTime:   to.UnixMilli(),
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		return sdk.Result{}, fmt.Errorf("tls native query: %w", err)
	}
	return resultFromSearch(req.Source, resp), nil
}

// Analyze returns a native time histogram. group_by is a best-effort SQL path;
// when it fails the error points callers at native_query.
func (a *Adapter) Analyze(ctx context.Context, req sdk.AnalyzeReq) (sdk.AnalyzeResult, error) {
	topicID, err := a.resolve(ctx, req.Project, req.Source)
	if err != nil {
		return sdk.AnalyzeResult{}, err
	}
	from, to := normalizeWindow(req.From, req.To)
	query := buildQuery(req.SearchReq)
	resp, err := a.client.DescribeHistogramV1(&tlssdk.DescribeHistogramV1Request{
		TopicID:   topicID,
		Query:     query,
		StartTime: from.UnixMilli(),
		EndTime:   to.UnixMilli(),
		Interval:  histogramInterval(to.Sub(from)),
	})
	if err != nil {
		return sdk.AnalyzeResult{}, fmt.Errorf("tls histogram: %w", err)
	}
	infos := append([]tlssdk.HistogramInfoV1(nil), resp.HistogramInfos...)
	sort.Slice(infos, func(i, j int) bool { return infos[i].StartTime < infos[j].StartTime })
	out := sdk.AnalyzeResult{Histogram: make([]sdk.Bucket, 0, len(infos))}
	for _, h := range infos {
		out.Histogram = append(out.Histogram, sdk.Bucket{
			Key:   time.UnixMilli(h.StartTime).In(sdk.DefaultLocation).Format("2006-01-02 15:04"),
			Count: h.Count,
		})
	}
	raw := map[string]any{"histogram": resp}
	if strings.TrimSpace(req.GroupBy) != "" {
		top, topRaw, err := a.groupBy(ctx, topicID, query, req.GroupBy, from, to)
		if err != nil {
			return sdk.AnalyzeResult{}, err
		}
		out.Top = top
		raw["top"] = topRaw
	}
	out.Raw = raw
	return out, nil
}

func (a *Adapter) groupBy(ctx context.Context, topicID, baseQuery, groupBy string, from, to time.Time) ([]sdk.Bucket, any, error) {
	query := fmt.Sprintf("%s | SELECT %s AS bucket, count(*) AS c GROUP BY %s ORDER BY c DESC LIMIT 100", baseQuery, groupBy, groupBy)
	resp, err := a.client.SearchLogsV2(&tlssdk.SearchLogsRequest{
		TopicID:   topicID,
		Query:     query,
		StartTime: from.UnixMilli(),
		EndTime:   to.UnixMilli(),
		Limit:     100,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("tls group_by failed: %w; use native_query with language=sql", err)
	}
	if resp.AnalysisResult == nil {
		return nil, nil, fmt.Errorf("tls group_by returned no analysis result; use native_query with language=sql")
	}
	buckets := make([]sdk.Bucket, 0, len(resp.AnalysisResult.Data))
	for _, row := range resp.AnalysisResult.Data {
		buckets = append(buckets, sdk.Bucket{
			Key:   fmt.Sprint(row["bucket"]),
			Count: toInt64(row["c"]),
		})
	}
	return buckets, resp, nil
}

// ContextLogs implements the v1 time-window fallback. TLS native context
// anchors are intentionally deferred until the anchor fields are verified.
func (a *Adapter) ContextLogs(ctx context.Context, req sdk.ContextReq) (sdk.ContextResult, error) {
	timeExpr := strings.TrimSpace(req.Anchor["time"])
	if timeExpr == "" {
		return sdk.ContextResult{}, fmt.Errorf("tls context requires a time anchor; native context anchors are not supported in v1")
	}
	anchor, err := sdk.ParseTimePoint(timeExpr)
	if err != nil {
		return sdk.ContextResult{}, err
	}
	window := 5 * time.Minute
	before := req.Before
	if before <= 0 {
		before = 10
	}
	after := req.After
	if after <= 0 {
		after = 10
	}
	res, err := a.Search(ctx, sdk.SearchReq{
		Tenant:  req.Tenant,
		Project: req.Project,
		Source:  req.Source,
		From:    anchor.Add(-window),
		To:      anchor.Add(window),
		Limit:   before + after + 1,
		Sort:    "asc",
	})
	if err != nil {
		return sdk.ContextResult{}, err
	}
	return sdk.ContextResult{
		Columns: res.Columns,
		Rows:    res.Rows,
		Raw: map[string]any{
			"mode":        "time_window",
			"anchor_time": anchor,
			"window":      window.String(),
			"result":      res,
		},
	}, nil
}

func buildQuery(req sdk.SearchReq) string {
	var parts []string
	if text := strings.TrimSpace(req.Text); text != "" {
		parts = append(parts, text)
	}
	for _, k := range sdk.SortedKeys(req.Filter) {
		parts = append(parts, fmt.Sprintf("%s:%q", k, req.Filter[k]))
	}
	if len(parts) == 0 {
		return "*"
	}
	return strings.Join(parts, " and ")
}

func normalizeSort(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "asc":
		return "asc"
	case "desc":
		return "desc"
	default:
		return ""
	}
}

func histogramInterval(d time.Duration) int64 {
	seconds := int64(d.Seconds())
	if seconds <= 0 {
		return int64(time.Minute / time.Millisecond)
	}
	// Aim for roughly 60 buckets across the window.
	interval := seconds / 60
	if interval < 1 {
		interval = 1
	}
	return interval * int64(time.Second/time.Millisecond)
}

func toInt64(v any) int64 {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int32:
		return int64(n)
	case int64:
		return n
	case float32:
		return int64(n)
	case float64:
		return int64(n)
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(n), 10, 64)
		return parsed
	default:
		return 0
	}
}
