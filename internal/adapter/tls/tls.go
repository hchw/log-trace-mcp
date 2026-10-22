// Package tls adapts Volcengine Torch Log Service (TLS) to the unified SDK.
package tls

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	tlssdk "github.com/volcengine/volc-sdk-golang/service/tls"

	"github.com/hchw/log-trace-mcp/internal/sdk"
)

const (
	defaultPageSize  = 100
	maxPages         = 100
	topicCacheTTL    = 5 * time.Minute
	defaultWindow    = time.Hour
	defaultSearchRow = 50
)

// Config is the non-secret TLS tenant configuration.
type Config struct {
	Endpoint string `json:"endpoint"`
	Region   string `json:"region"`
}

// Creds holds decrypted TLS credentials.
type Creds struct {
	AccessKeyID     string `json:"ak_id"`
	AccessKeySecret string `json:"ak_secret"`
	SecurityToken   string `json:"security_token,omitempty"`
}

// apiClient is the narrow subset of tlssdk.Client used by the adapter. Keeping
// it small makes the adapter testable without real credentials.
type apiClient interface {
	DescribeProjects(*tlssdk.DescribeProjectsRequest) (*tlssdk.DescribeProjectsResponse, error)
	DescribeTopics(*tlssdk.DescribeTopicsRequest) (*tlssdk.DescribeTopicsResponse, error)
	DescribeIndex(*tlssdk.DescribeIndexRequest) (*tlssdk.DescribeIndexResponse, error)
	SearchLogsV2(*tlssdk.SearchLogsRequest) (*tlssdk.SearchLogsResponse, error)
	DescribeHistogramV1(*tlssdk.DescribeHistogramV1Request) (*tlssdk.DescribeHistogramV1Response, error)
}

// Adapter implements sdk.Adapter for Volcengine TLS.
type Adapter struct {
	client apiClient
	now    func() time.Time

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	topicID string
	expires time.Time
}

// New builds a TLS adapter.
func New(cfg Config, creds Creds) (*Adapter, error) {
	endpoint, err := resolveEndpoint(cfg)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(creds.AccessKeyID) == "" || strings.TrimSpace(creds.AccessKeySecret) == "" {
		return nil, fmt.Errorf("tls credentials require ak_id and ak_secret")
	}
	client := tlssdk.NewClient(
		endpoint,
		strings.TrimSpace(creds.AccessKeyID),
		strings.TrimSpace(creds.AccessKeySecret),
		strings.TrimSpace(creds.SecurityToken),
		strings.TrimSpace(cfg.Region),
	)
	return newWithClient(client), nil
}

func newWithClient(client apiClient) *Adapter {
	return &Adapter{
		client: client,
		now:    time.Now,
		cache:  map[string]cacheEntry{},
	}
}

// Close is a no-op; the TLS SDK client manages pooled connections.
func (a *Adapter) Close() error { return nil }

func resolveEndpoint(cfg Config) (string, error) {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	region := strings.TrimSpace(cfg.Region)
	if endpoint == "" {
		if region == "" {
			return "", fmt.Errorf("tls config requires endpoint or region")
		}
		endpoint = "https://tls-" + region + ".volces.com"
	}
	if !strings.Contains(endpoint, "://") {
		endpoint = "https://" + endpoint
	}
	return endpoint, nil
}

// splitTarget resolves the project and topic from the explicit project
// parameter and the source, mirroring the SLS project/logstore convention.
func splitTarget(project, source string) (string, string, error) {
	project = strings.TrimSpace(project)
	source = strings.TrimSpace(source)
	if source == "" {
		return "", "", fmt.Errorf("source is required")
	}
	if project != "" {
		if strings.Contains(source, "/") {
			return "", "", fmt.Errorf("source %q must be a topic when project is set", source)
		}
		return project, source, nil
	}
	if p, t, ok := strings.Cut(source, "/"); ok {
		p, t = strings.TrimSpace(p), strings.TrimSpace(t)
		if p == "" || t == "" {
			return "", "", fmt.Errorf("invalid source %q: expected project/topic", source)
		}
		return p, t, nil
	}
	return "", "", fmt.Errorf("project is required (pass it as the project tool parameter, or use source=\"project/topic\")")
}

// resolve returns a TopicID for a project/topic pair, accepting names or IDs
// and caching successful resolutions for a short time.
func (a *Adapter) resolve(ctx context.Context, project, source string) (string, error) {
	project, topic, err := splitTarget(project, source)
	if err != nil {
		return "", err
	}
	key := project + "/" + topic
	if id, ok := a.cached(key); ok {
		return id, nil
	}
	resolved, err := a.lookupTopic(ctx, project, topic)
	if err != nil {
		return "", err
	}
	a.store(key, resolved.TopicID)
	return resolved.TopicID, nil
}

func (a *Adapter) cached(key string) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	entry, ok := a.cache[key]
	if !ok || a.now().After(entry.expires) {
		return "", false
	}
	return entry.topicID, true
}

func (a *Adapter) store(key, topicID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cache[key] = cacheEntry{topicID: topicID, expires: a.now().Add(topicCacheTTL)}
}

func (a *Adapter) lookupTopic(ctx context.Context, project, topic string) (*tlssdk.Topic, error) {
	// A pair can mix a project name with a topic ID (or the reverse), so all
	// four combinations are attempted. UUID-like inputs are tried as IDs first
	// to avoid a needless name lookup.
	projectPairs := [][2]string{{project, ""}, {"", project}}
	if looksLikeID(project) {
		projectPairs[0], projectPairs[1] = projectPairs[1], projectPairs[0]
	}
	topicPairs := [][2]string{{topic, ""}, {"", topic}}
	if looksLikeID(topic) {
		topicPairs[0], topicPairs[1] = topicPairs[1], topicPairs[0]
	}

	var firstErr error
	for _, pp := range projectPairs {
		for _, tp := range topicPairs {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			req := &tlssdk.DescribeTopicsRequest{
				ProjectName: pp[0],
				ProjectID:   pp[1],
				TopicName:   tp[0],
				TopicID:     tp[1],
				PageNumber:  1,
				PageSize:    defaultPageSize,
			}
			resp, err := a.client.DescribeTopics(req)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			for _, item := range resp.Topics {
				if item == nil {
					continue
				}
				if item.TopicID == topic || item.TopicName == topic {
					return item, nil
				}
			}
		}
	}
	if firstErr != nil {
		return nil, fmt.Errorf("tls describe topics: %w", firstErr)
	}
	return nil, fmt.Errorf("tls topic %q not found in project %q", topic, project)
}

func looksLikeID(s string) bool {
	if len(s) < 16 {
		return false
	}
	dashes := 0
	for _, r := range s {
		if r == '-' {
			dashes++
		}
	}
	return dashes >= 2
}

func normalizeWindow(from, to time.Time) (time.Time, time.Time) {
	if from.IsZero() || to.IsZero() {
		now := time.Now()
		return now.Add(-defaultWindow), now
	}
	return from, to
}

func rawToString(raw any) (string, error) {
	switch v := raw.(type) {
	case nil:
		return "", fmt.Errorf("raw query is required")
	case string:
		if strings.TrimSpace(v) == "" {
			return "", fmt.Errorf("raw query is required")
		}
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

func resultFromSearch(source string, resp *tlssdk.SearchLogsResponse) sdk.Result {
	meta := map[string]any{
		"hit_count": resp.HitCount,
		"count":     resp.Count,
		"status":    resp.Status,
	}
	if resp.ElapsedMillisecond > 0 {
		meta["elapsed_ms"] = resp.ElapsedMillisecond
	}
	res := sdk.Result{
		Engine: string(sdk.EngineTLS),
		Source: source,
		Meta:   meta,
		Raw:    resp,
	}
	// TLS returns a non-nil but empty AnalysisResult for plain retrieval
	// queries, so only take the analysis path when the backend says the query
	// was analytical or actually produced a schema/data.
	useAnalysis := resp.AnalysisResult != nil &&
		(resp.Analysis || len(resp.AnalysisResult.Schema) > 0 || len(resp.AnalysisResult.Data) > 0)
	if useAnalysis {
		cols, rows := sdk.NormalizeAnalysis(resp.AnalysisResult.Schema, resp.AnalysisResult.Data)
		res.Columns, res.Rows = cols, rows
		res.Normalized = len(cols) > 0
		if len(resp.AnalysisResult.Type) > 0 {
			meta["column_types"] = resp.AnalysisResult.Type
		}
		return res
	}
	cols, rows := sdk.NormalizeAnyRecords(resp.Logs)
	res.Columns, res.Rows = cols, rows
	res.Normalized = len(cols) > 0
	return res
}
