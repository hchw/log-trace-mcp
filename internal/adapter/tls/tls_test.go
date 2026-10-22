package tls

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	tlssdk "github.com/volcengine/volc-sdk-golang/service/tls"

	"github.com/hchw/log-trace-mcp/internal/sdk"
)

type fakeClient struct {
	projectsFn  func(*tlssdk.DescribeProjectsRequest) (*tlssdk.DescribeProjectsResponse, error)
	topicsFn    func(*tlssdk.DescribeTopicsRequest) (*tlssdk.DescribeTopicsResponse, error)
	indexFn     func(*tlssdk.DescribeIndexRequest) (*tlssdk.DescribeIndexResponse, error)
	searchFn    func(*tlssdk.SearchLogsRequest) (*tlssdk.SearchLogsResponse, error)
	histogramFn func(*tlssdk.DescribeHistogramV1Request) (*tlssdk.DescribeHistogramV1Response, error)
}

func (f *fakeClient) DescribeProjects(r *tlssdk.DescribeProjectsRequest) (*tlssdk.DescribeProjectsResponse, error) {
	if f.projectsFn == nil {
		return &tlssdk.DescribeProjectsResponse{}, nil
	}
	return f.projectsFn(r)
}

func (f *fakeClient) DescribeTopics(r *tlssdk.DescribeTopicsRequest) (*tlssdk.DescribeTopicsResponse, error) {
	if f.topicsFn == nil {
		return &tlssdk.DescribeTopicsResponse{}, nil
	}
	return f.topicsFn(r)
}

func (f *fakeClient) DescribeIndex(r *tlssdk.DescribeIndexRequest) (*tlssdk.DescribeIndexResponse, error) {
	if f.indexFn == nil {
		return &tlssdk.DescribeIndexResponse{}, nil
	}
	return f.indexFn(r)
}

func (f *fakeClient) SearchLogsV2(r *tlssdk.SearchLogsRequest) (*tlssdk.SearchLogsResponse, error) {
	if f.searchFn == nil {
		return &tlssdk.SearchLogsResponse{}, nil
	}
	return f.searchFn(r)
}

func (f *fakeClient) DescribeHistogramV1(r *tlssdk.DescribeHistogramV1Request) (*tlssdk.DescribeHistogramV1Response, error) {
	if f.histogramFn == nil {
		return &tlssdk.DescribeHistogramV1Response{}, nil
	}
	return f.histogramFn(r)
}

func testAdapter(c *fakeClient) *Adapter {
	a := newWithClient(c)
	a.now = func() time.Time { return time.Unix(1_700_000_000, 0) }
	return a
}

func topicClient(calls *int) *fakeClient {
	return &fakeClient{topicsFn: func(r *tlssdk.DescribeTopicsRequest) (*tlssdk.DescribeTopicsResponse, error) {
		*calls++
		if r.ProjectName == "p" || r.ProjectID == "p" {
			if r.TopicName == "t" || r.TopicID == "t" {
				return &tlssdk.DescribeTopicsResponse{Topics: []*tlssdk.Topic{{TopicID: "t-1", TopicName: "t"}}}, nil
			}
		}
		return &tlssdk.DescribeTopicsResponse{}, nil
	}}
}

func TestNewValidatesConnectionAndCreds(t *testing.T) {
	if _, err := New(Config{}, Creds{AccessKeyID: "a", AccessKeySecret: "b"}); err == nil {
		t.Fatal("expected error when endpoint and region are missing")
	}
	if _, err := New(Config{Region: "cn-beijing"}, Creds{}); err == nil {
		t.Fatal("expected error when credentials are missing")
	}
	if _, err := New(Config{Region: "cn-beijing"}, Creds{AccessKeyID: "a", AccessKeySecret: "b"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSplitTarget(t *testing.T) {
	cases := []struct {
		project, source, wantProject, wantTopic string
		wantErr                                 bool
	}{
		{"p", "t", "p", "t", false},
		{"", "p/t", "p", "t", false},
		{"", "t", "", "", true},
		{"p", "p/t", "", "", true},
		{"", "/t", "", "", true},
	}
	for _, tc := range cases {
		p, topic, err := splitTarget(tc.project, tc.source)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("%q/%q: expected error", tc.project, tc.source)
			}
			continue
		}
		if err != nil || p != tc.wantProject || topic != tc.wantTopic {
			t.Fatalf("%q/%q = %q/%q, %v", tc.project, tc.source, p, topic, err)
		}
	}
}

func TestResolveTopicCaches(t *testing.T) {
	calls := 0
	a := testAdapter(topicClient(&calls))
	id, err := a.resolve(context.Background(), "p", "t")
	if err != nil {
		t.Fatal(err)
	}
	if id != "t-1" {
		t.Fatalf("id = %q", id)
	}
	if _, err := a.resolve(context.Background(), "p", "t"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("expected one DescribeTopics call, got %d", calls)
	}
}

func TestListProjectsPagination(t *testing.T) {
	c := &fakeClient{projectsFn: func(r *tlssdk.DescribeProjectsRequest) (*tlssdk.DescribeProjectsResponse, error) {
		switch r.PageNumber {
		case 1:
			return &tlssdk.DescribeProjectsResponse{Total: 3, Projects: []tlssdk.ProjectInfo{
				{ProjectID: "p1", ProjectName: "one"},
				{ProjectID: "p2", ProjectName: "two"},
			}}, nil
		case 2:
			return &tlssdk.DescribeProjectsResponse{Total: 3, Projects: []tlssdk.ProjectInfo{
				{ProjectID: "p3", ProjectName: "three"},
			}}, nil
		default:
			return &tlssdk.DescribeProjectsResponse{Total: 3}, nil
		}
	}}
	a := testAdapter(c)
	sources, err := a.ListSources(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(sources.Flat) != 3 {
		t.Fatalf("flat = %v", sources.Flat)
	}
	if raws, ok := sources.Raw.([]*tlssdk.DescribeProjectsResponse); !ok || len(raws) != 2 {
		t.Fatalf("raw pages not preserved: %#v", sources.Raw)
	}
}

func TestListTopicsPagination(t *testing.T) {
	c := &fakeClient{topicsFn: func(r *tlssdk.DescribeTopicsRequest) (*tlssdk.DescribeTopicsResponse, error) {
		switch r.PageNumber {
		case 1:
			return &tlssdk.DescribeTopicsResponse{Total: 2, Topics: []*tlssdk.Topic{{TopicID: "1", TopicName: "t1"}}}, nil
		case 2:
			return &tlssdk.DescribeTopicsResponse{Total: 2, Topics: []*tlssdk.Topic{{TopicID: "2", TopicName: "t2"}}}, nil
		default:
			return &tlssdk.DescribeTopicsResponse{Total: 2}, nil
		}
	}}
	a := testAdapter(c)
	sources, err := a.ListSources(context.Background(), "p")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"p/t1", "p/t2"}
	got := []string{sources.Flat[0].Name, sources.Flat[1].Name}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("names = %v, want %v", got, want)
	}
}

func TestDescribeSchema(t *testing.T) {
	calls := 0
	c := topicClient(&calls)
	c.indexFn = func(r *tlssdk.DescribeIndexRequest) (*tlssdk.DescribeIndexResponse, error) {
		if r.TopicID != "t-1" {
			t.Fatalf("topic id = %q", r.TopicID)
		}
		kv := []tlssdk.KeyValueInfo{{Key: "level", Value: tlssdk.Value{ValueType: "text"}}}
		return &tlssdk.DescribeIndexResponse{FullText: &tlssdk.FullTextInfo{}, KeyValue: &kv}, nil
	}
	a := testAdapter(c)
	schema, err := a.DescribeSchema(context.Background(), "p", "t")
	if err != nil {
		t.Fatal(err)
	}
	if len(schema.Fields) != 2 || schema.Fields[0].Name != "__full_text__" || schema.Fields[1].Name != "level" {
		t.Fatalf("fields = %#v", schema.Fields)
	}
	if schema.Raw == nil {
		t.Fatal("raw must be preserved")
	}
}

func TestSearchUsesMillisecondsAndKeepsTypes(t *testing.T) {
	calls := 0
	c := topicClient(&calls)
	from := time.Unix(1_700_000_000, 0)
	to := from.Add(time.Hour)
	var gotReq *tlssdk.SearchLogsRequest
	c.searchFn = func(r *tlssdk.SearchLogsRequest) (*tlssdk.SearchLogsResponse, error) {
		gotReq = r
		return &tlssdk.SearchLogsResponse{
			Status:             "OK",
			HitCount:           7,
			Count:              1,
			ElapsedMillisecond: 12,
			Logs:               []map[string]any{{"level": "ERROR", "status": 500, "ok": false}},
		}, nil
	}
	a := testAdapter(c)
	res, err := a.Search(context.Background(), sdk.SearchReq{
		Project: "p", Source: "t", Text: "boom", Filter: map[string]string{"level": "ERROR"},
		From: from, To: to, Limit: 5, Offset: 2, Sort: "asc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotReq.StartTime != from.UnixMilli() || gotReq.EndTime != to.UnixMilli() {
		t.Fatalf("times = %d/%d, want %d/%d", gotReq.StartTime, gotReq.EndTime, from.UnixMilli(), to.UnixMilli())
	}
	if gotReq.Limit != 5 || gotReq.Offset == nil || *gotReq.Offset != 2 || gotReq.Sort != "asc" {
		t.Fatalf("request = %#v", gotReq)
	}
	if !strings.Contains(gotReq.Query, "boom") || !strings.Contains(gotReq.Query, `level:"ERROR"`) {
		t.Fatalf("query = %q", gotReq.Query)
	}
	if res.Engine != "tls" || !res.Normalized || res.Raw == nil {
		t.Fatalf("result = %#v", res)
	}
	idx := indexOf(res.Columns, "status")
	if idx < 0 {
		t.Fatalf("columns = %v", res.Columns)
	}
	if _, ok := res.Rows[0][idx].(int); !ok {
		t.Fatalf("status type lost: %T", res.Rows[0][idx])
	}
	if res.Meta["hit_count"] != 7 {
		t.Fatalf("meta = %#v", res.Meta)
	}
}

func indexOf(items []string, want string) int {
	for i, item := range items {
		if item == want {
			return i
		}
	}
	return -1
}

func TestCountUsesHistogramTotal(t *testing.T) {
	calls := 0
	c := topicClient(&calls)
	searched := false
	c.searchFn = func(r *tlssdk.SearchLogsRequest) (*tlssdk.SearchLogsResponse, error) {
		searched = true
		return &tlssdk.SearchLogsResponse{HitCount: 1}, nil
	}
	c.histogramFn = func(r *tlssdk.DescribeHistogramV1Request) (*tlssdk.DescribeHistogramV1Response, error) {
		if r.Interval <= 0 {
			t.Fatalf("interval = %d", r.Interval)
		}
		return &tlssdk.DescribeHistogramV1Response{TotalCount: 42}, nil
	}
	a := testAdapter(c)
	n, err := a.Count(context.Background(), sdk.SearchReq{Project: "p", Source: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if n != 42 {
		t.Fatalf("count = %d, want 42", n)
	}
	if searched {
		t.Fatal("Count must not rely on the page-capped SearchLogsV2.HitCount")
	}
}

func TestResultFromSearchIgnoresEmptyAnalysisResult(t *testing.T) {
	res := resultFromSearch("p/t", &tlssdk.SearchLogsResponse{
		Analysis:       false,
		AnalysisResult: &tlssdk.AnalysisResult{},
		Logs:           []map[string]any{{"level": "ERROR"}},
		HitCount:       1,
	})
	if !res.Normalized || len(res.Rows) != 1 {
		t.Fatalf("plain retrieval must not be shadowed by an empty AnalysisResult: %#v", res)
	}
}

func TestResultFromSearchUsesPopulatedAnalysisResult(t *testing.T) {
	res := resultFromSearch("p/t", &tlssdk.SearchLogsResponse{
		Analysis: true,
		AnalysisResult: &tlssdk.AnalysisResult{
			Schema: []string{"level", "c"},
			Type:   map[string]string{"c": "long"},
			Data:   []map[string]any{{"level": "ERROR", "c": 3}},
		},
	})
	if !res.Normalized || len(res.Rows) != 1 || res.Columns[0] != "level" {
		t.Fatalf("analysis result not normalized: %#v", res)
	}
}

func TestNativeQueryLanguages(t *testing.T) {
	calls := 0
	c := topicClient(&calls)
	c.searchFn = func(r *tlssdk.SearchLogsRequest) (*tlssdk.SearchLogsResponse, error) {
		return &tlssdk.SearchLogsResponse{Logs: []map[string]any{{"x": 1}}}, nil
	}
	a := testAdapter(c)
	if _, err := a.NativeQuery(context.Background(), sdk.NativeReq{Project: "p", Source: "t", Language: "spl", Raw: "*"}); err == nil || !strings.Contains(err.Error(), "valid") {
		t.Fatalf("expected language error, got %v", err)
	}
	if _, err := a.NativeQuery(context.Background(), sdk.NativeReq{Project: "p", Source: "t", Language: "sql", Raw: "* | SELECT count(*) AS c"}); err != nil {
		t.Fatal(err)
	}
}

func TestAnalyzeHistogramSorted(t *testing.T) {
	calls := 0
	c := topicClient(&calls)
	c.histogramFn = func(r *tlssdk.DescribeHistogramV1Request) (*tlssdk.DescribeHistogramV1Response, error) {
		if r.Interval <= 0 {
			t.Fatalf("interval = %d", r.Interval)
		}
		return &tlssdk.DescribeHistogramV1Response{HistogramInfos: []tlssdk.HistogramInfoV1{
			{StartTime: 2_000, Count: 2},
			{StartTime: 1_000, Count: 1},
		}}, nil
	}
	a := testAdapter(c)
	res, err := a.Analyze(context.Background(), sdk.AnalyzeReq{SearchReq: sdk.SearchReq{Project: "p", Source: "t"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Histogram) != 2 || res.Histogram[0].Count != 1 || res.Histogram[1].Count != 2 {
		t.Fatalf("histogram = %#v", res.Histogram)
	}
	if res.Raw == nil {
		t.Fatal("raw must be preserved")
	}
}

func TestAnalyzeGroupByFailurePointsToNativeQuery(t *testing.T) {
	calls := 0
	c := topicClient(&calls)
	c.searchFn = func(r *tlssdk.SearchLogsRequest) (*tlssdk.SearchLogsResponse, error) {
		return nil, errors.New("sql dialect unsupported")
	}
	a := testAdapter(c)
	_, err := a.Analyze(context.Background(), sdk.AnalyzeReq{
		SearchReq: sdk.SearchReq{Project: "p", Source: "t"},
		GroupBy:   "level",
	})
	if err == nil || !strings.Contains(err.Error(), "native_query") {
		t.Fatalf("expected native_query guidance, got %v", err)
	}
}

func TestContextLogsTimeFallback(t *testing.T) {
	calls := 0
	c := topicClient(&calls)
	searched := false
	c.searchFn = func(r *tlssdk.SearchLogsRequest) (*tlssdk.SearchLogsResponse, error) {
		searched = true
		return &tlssdk.SearchLogsResponse{Logs: []map[string]any{{"msg": "hi"}}}, nil
	}
	a := testAdapter(c)
	res, err := a.ContextLogs(context.Background(), sdk.ContextReq{
		Project: "p", Source: "t", Anchor: map[string]string{"time": "2026-10-17 12:00:00"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !searched {
		t.Fatal("expected a search for the time window")
	}
	raw, ok := res.Raw.(map[string]any)
	if !ok || raw["mode"] != "time_window" {
		t.Fatalf("raw = %#v", res.Raw)
	}
}

func TestContextLogsMissingTime(t *testing.T) {
	a := testAdapter(&fakeClient{})
	if _, err := a.ContextLogs(context.Background(), sdk.ContextReq{Project: "p", Source: "t"}); err == nil {
		t.Fatal("expected an error without a time anchor")
	}
}

func TestResultFromSearchEmptyIsNotNormalized(t *testing.T) {
	res := resultFromSearch("p/t", &tlssdk.SearchLogsResponse{})
	if res.Normalized {
		t.Fatalf("expected normalized=false for empty result: %#v", res)
	}
}
