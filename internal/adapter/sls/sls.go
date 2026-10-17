// Package sls adapts Aliyun Simple Log Service to the unified SDK.
package sls

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	slssdk "github.com/aliyun/aliyun-log-go-sdk"

	"github.com/hchw/log-trace-mcp/internal/sdk"
)

// Config is the non-secret SLS tenant configuration. The project is supplied
// per call as a tool parameter, not stored here.
type Config struct {
	Region   string `json:"region"`
	Endpoint string `json:"endpoint"`
}

// Creds holds decrypted SLS credentials.
type Creds struct {
	AccessKeyID     string `json:"ak_id"`
	AccessKeySecret string `json:"ak_secret"`
	SecurityToken   string `json:"security_token,omitempty"`
}

// Adapter implements sdk.Adapter for SLS.
type Adapter struct {
	client slssdk.ClientInterface
}

// New builds an SLS adapter.
func New(cfg Config, creds Creds) (*Adapter, error) {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		region := strings.TrimSpace(cfg.Region)
		if region == "" {
			return nil, fmt.Errorf("sls config requires endpoint or region")
		}
		endpoint = region + ".log.aliyuncs.com"
	}
	provider := slssdk.NewStaticCredentialsProvider(creds.AccessKeyID, creds.AccessKeySecret, creds.SecurityToken)
	client := slssdk.CreateNormalInterfaceV2(endpoint, provider)
	return &Adapter{client: client}, nil
}

// Close is a no-op; the SLS client manages pooled connections.
func (a *Adapter) Close() error { return nil }

func (a *Adapter) resolve(project, source string) (string, string, error) {
	return sdk.ResolveSLS(project, source)
}

// ListSources returns projects when project is empty, otherwise the logstores
// of that project.
func (a *Adapter) ListSources(ctx context.Context, project string) (sdk.Sources, error) {
	if strings.TrimSpace(project) == "" {
		projects, err := a.client.ListProject()
		if err != nil {
			return sdk.Sources{}, err
		}
		tree := make([]sdk.Source, 0, len(projects))
		for _, p := range projects {
			tree = append(tree, sdk.Source{Name: p, Kind: "project"})
		}
		return sdk.Sources{Tree: tree, Flat: tree, Raw: projects}, nil
	}
	project = strings.TrimSpace(project)
	logstores, err := a.client.ListLogStore(project)
	if err != nil {
		return sdk.Sources{}, err
	}
	tree := make([]sdk.Source, 0, len(logstores))
	for _, l := range logstores {
		tree = append(tree, sdk.Source{Name: project + "/" + l, Kind: "logstore"})
	}
	return sdk.Sources{Tree: tree, Flat: tree, Raw: logstores}, nil
}

// DescribeSchema returns the index keys of a logstore.
func (a *Adapter) DescribeSchema(ctx context.Context, project, source string) (sdk.Schema, error) {
	project, logstore, err := a.resolve(project, source)
	if err != nil {
		return sdk.Schema{}, err
	}
	idx, err := a.client.GetIndex(project, logstore)
	if err != nil {
		return sdk.Schema{}, err
	}
	var fields []sdk.Field
	for name, key := range idx.Keys {
		fields = append(fields, sdk.Field{Name: name, Type: key.Type})
	}
	return sdk.Schema{Fields: fields, Raw: idx}, nil
}

// Search runs a unified semantic query against SLS.
func (a *Adapter) Search(ctx context.Context, req sdk.SearchReq) (sdk.Result, error) {
	project, logstore, err := a.resolve(req.Project, req.Source)
	if err != nil {
		return sdk.Result{}, err
	}
	query := buildQuery(req)
	limit := req.Limit
	if limit <= 0 {
		limit = 50
	}
	reverse := req.Sort != "asc"
	resp, err := a.client.GetLogs(project, logstore, "", req.From.Unix(), req.To.Unix(), query, int64(limit), int64(req.Offset), reverse)
	if err != nil {
		return sdk.Result{}, err
	}
	return logResult(project+"/"+logstore, resp), nil
}

// Count returns the number of matching records.
func (a *Adapter) Count(ctx context.Context, req sdk.SearchReq) (int64, error) {
	project, logstore, err := a.resolve(req.Project, req.Source)
	if err != nil {
		return 0, err
	}
	query := buildQuery(req)
	// GetLogs returns at most `line` logs and its Count field is the number of
	// logs actually returned, so it cannot report a total. GetHistograms reports
	// the true match count independently of any page size.
	resp, err := a.client.GetHistograms(project, logstore, "", req.From.Unix(), req.To.Unix(), query)
	if err != nil {
		return 0, err
	}
	return resp.Count, nil
}

// Analyze runs SQL aggregations for histogram and grouping.
func (a *Adapter) Analyze(ctx context.Context, req sdk.AnalyzeReq) (sdk.AnalyzeResult, error) {
	project, logstore, err := a.resolve(req.Project, req.Source)
	if err != nil {
		return sdk.AnalyzeResult{}, err
	}
	base := buildQuery(req.SearchReq)
	out := sdk.AnalyzeResult{}
	raw := map[string]any{}

	histSQL := fmt.Sprintf("%s | select date_format(date_trunc('minute', __time__), '%%Y-%%m-%%d %%H:%%i') as bucket, count(*) as c group by bucket order by bucket limit 1000", base)
	if b, r, err := a.runSQL(project, logstore, req.From, req.To, histSQL, "bucket", "c"); err == nil {
		out.Histogram = b
		raw["histogram"] = r
	} else {
		return sdk.AnalyzeResult{}, fmt.Errorf("histogram: %w", err)
	}

	if strings.TrimSpace(req.GroupBy) != "" {
		topSQL := fmt.Sprintf("%s | select %s as bucket, count(*) as c group by bucket order by c desc limit 100", base, req.GroupBy)
		if b, r, err := a.runSQL(project, logstore, req.From, req.To, topSQL, "bucket", "c"); err == nil {
			out.Top = b
			raw["top"] = r
		} else {
			return sdk.AnalyzeResult{}, fmt.Errorf("group by: %w", err)
		}
	}
	out.Raw = raw
	return out, nil
}

func (a *Adapter) runSQL(project, logstore string, from, to time.Time, query, keyCol, cntCol string) ([]sdk.Bucket, any, error) {
	f := from.Unix()
	t := to.Unix()
	resp, err := a.client.GetLogs(project, logstore, "", f, t, query, 1000, 0, false)
	if err != nil {
		return nil, nil, err
	}
	_ = keyCol
	_ = cntCol
	buckets := make([]sdk.Bucket, 0, len(resp.Logs))
	for _, row := range resp.Logs {
		buckets = append(buckets, sdk.Bucket{
			Key:   row["bucket"],
			Count: atoi64(row["c"]),
		})
	}
	return buckets, resp.Logs, nil
}

// ContextLogs returns logs surrounding an anchor for SLS.
func (a *Adapter) ContextLogs(ctx context.Context, req sdk.ContextReq) (sdk.ContextResult, error) {
	project, logstore, err := a.resolve(req.Project, req.Source)
	if err != nil {
		return sdk.ContextResult{}, err
	}
	packID := firstNonEmpty(req.Anchor["pack_id"], req.Anchor["__tag__:__pack_id__"])
	packMeta := firstNonEmpty(req.Anchor["pack_meta"], req.Anchor["__pack_meta__"])
	if packID == "" || packMeta == "" {
		return sdk.ContextResult{}, fmt.Errorf("anchor requires pack_id and pack_meta")
	}
	store, err := a.client.GetLogStore(project, logstore)
	if err != nil {
		return sdk.ContextResult{}, err
	}
	before := int32(req.Before)
	if before <= 0 {
		before = 10
	}
	after := int32(req.After)
	if after <= 0 {
		after = 10
	}
	resp, err := store.GetContextLogs(before, after, packID, packMeta)
	if err != nil {
		return sdk.ContextResult{}, err
	}
	cols, rows := sdk.NormalizeRecords(resp.Logs)
	return sdk.ContextResult{Columns: cols, Rows: rows, Raw: resp}, nil
}

// NativeQuery passes a SQL or SPL query through to SLS.
func (a *Adapter) NativeQuery(ctx context.Context, req sdk.NativeReq) (sdk.Result, error) {
	project, logstore, err := a.resolve(req.Project, req.Source)
	if err != nil {
		return sdk.Result{}, err
	}
	query, err := rawToString(req.Raw)
	if err != nil {
		return sdk.Result{}, err
	}
	from, to := req.From, req.To
	if !req.HasTime || from.IsZero() || to.IsZero() {
		now := time.Now()
		from, to = now.Add(-time.Hour), now
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 100
	}
	resp, err := a.client.GetLogs(project, logstore, "", from.Unix(), to.Unix(), query, int64(limit), int64(req.Offset), false)
	if err != nil {
		return sdk.Result{}, err
	}
	return logResult(project+"/"+logstore, resp), nil
}

func logResult(source string, resp *slssdk.GetLogsResponse) sdk.Result {
	cols, rows := sdk.NormalizeRecords(resp.Logs)
	return sdk.Result{
		Engine:     string(sdk.EngineSLS),
		Source:     source,
		Normalized: true,
		Columns:    cols,
		Rows:       rows,
		Meta:       map[string]any{"count": resp.Count, "progress": resp.Progress},
		Raw:        resp,
	}
}

func buildQuery(req sdk.SearchReq) string {
	var parts []string
	if strings.TrimSpace(req.Text) != "" {
		parts = append(parts, strings.TrimSpace(req.Text))
	}
	for _, k := range keysSorted(req.Filter) {
		parts = append(parts, fmt.Sprintf("%s:%q", k, req.Filter[k]))
	}
	if len(parts) == 0 {
		return "*"
	}
	return strings.Join(parts, " and ")
}

func keysSorted(m map[string]string) []string {
	return sdk.SortedKeys(m)
}

func rawToString(raw any) (string, error) {
	switch v := raw.(type) {
	case nil:
		return "", fmt.Errorf("raw query is required")
	case string:
		return v, nil
	case json.RawMessage:
		return string(v), nil
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
}

func atoi64(s string) int64 {
	var n int64
	fmt.Sscanf(s, "%d", &n)
	return n
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
