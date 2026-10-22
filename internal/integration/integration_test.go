// Package integration holds end-to-end tests that require live backends.
// Tests skip unless the corresponding credentials are provided.
package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/hchw/log-trace-mcp/internal/adapter/opensearch"
	"github.com/hchw/log-trace-mcp/internal/adapter/sls"
	tlsadapter "github.com/hchw/log-trace-mcp/internal/adapter/tls"
	"github.com/hchw/log-trace-mcp/internal/sdk"
)

func TestSLSEndToEnd(t *testing.T) {
	endpoint := os.Getenv("LTM_TEST_SLS_ENDPOINT")
	region := os.Getenv("LTM_TEST_SLS_REGION")
	ak := os.Getenv("LTM_TEST_SLS_AK_ID")
	sk := os.Getenv("LTM_TEST_SLS_AK_SECRET")
	project := os.Getenv("LTM_TEST_SLS_PROJECT")
	logstore := os.Getenv("LTM_TEST_SLS_LOGSTORE")
	if (endpoint == "" && region == "") || ak == "" || project == "" || logstore == "" {
		t.Skip("set LTM_TEST_SLS_* to run the SLS integration test")
	}

	a, err := sls.New(sls.Config{Region: region, Endpoint: endpoint},
		sls.Creds{AccessKeyID: ak, AccessKeySecret: sk})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx := context.Background()

	sources, err := a.ListSources(ctx, "")
	if err != nil {
		t.Fatalf("list projects: %v", err)
	}
	if len(sources.Flat) == 0 {
		t.Fatal("expected at least one project")
	}

	if _, err := a.DescribeSchema(ctx, project, logstore); err != nil {
		t.Fatalf("describe schema: %v", err)
	}

	to := time.Now()
	from := to.Add(-time.Hour)
	req := sdk.SearchReq{Tenant: "it", Project: project, Source: logstore, From: from, To: to, Limit: 5}
	res, err := a.Search(ctx, req)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if res.Raw == nil {
		t.Fatal("raw must be preserved")
	}
	if _, err := a.Count(ctx, req); err != nil {
		t.Fatalf("count: %v", err)
	}
	if _, err := a.NativeQuery(ctx, sdk.NativeReq{Tenant: "it", Project: project, Source: logstore,
		Language: "sql", Raw: "SELECT count(*) as c", From: from, To: to, HasTime: true}); err != nil {
		t.Fatalf("native sql: %v", err)
	}
}

func TestOpenSearchEndToEnd(t *testing.T) {
	endpoint := os.Getenv("LTM_TEST_OS_ENDPOINT")
	index := os.Getenv("LTM_TEST_OS_INDEX")
	if endpoint == "" || index == "" {
		t.Skip("set LTM_TEST_OS_ENDPOINT and LTM_TEST_OS_INDEX to run the OpenSearch integration test")
	}
	a, err := opensearch.New(opensearch.Config{
		Endpoint:  endpoint,
		Index:     index,
		TimeField: os.Getenv("LTM_TEST_OS_TIME_FIELD"),
		AuthKind:  "basic",
	}, opensearch.Creds{Username: os.Getenv("LTM_TEST_OS_USER"), Password: os.Getenv("LTM_TEST_OS_PASS")})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx := context.Background()

	sources, err := a.ListSources(ctx, "")
	if err != nil {
		t.Fatalf("list indices: %v", err)
	}
	_ = sources

	to := time.Now()
	from := to.Add(-time.Hour)
	req := sdk.SearchReq{Tenant: "it", Source: index, From: from, To: to, Limit: 5}
	res, err := a.Search(ctx, req)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if res.Raw == nil {
		t.Fatal("raw must be preserved")
	}
	if _, err := a.Count(ctx, req); err != nil {
		t.Fatalf("count: %v", err)
	}
	if _, err := a.NativeQuery(ctx, sdk.NativeReq{Tenant: "it", Source: index,
		Language: "dsl", Raw: map[string]any{"query": map[string]any{"match_all": map[string]any{}}}}); err != nil {
		t.Fatalf("native dsl: %v", err)
	}
}

func TestTLSEndToEnd(t *testing.T) {
	endpoint := os.Getenv("LTM_TEST_TLS_ENDPOINT")
	region := os.Getenv("LTM_TEST_TLS_REGION")
	ak := os.Getenv("LTM_TEST_TLS_AK_ID")
	sk := os.Getenv("LTM_TEST_TLS_AK_SECRET")
	project := os.Getenv("LTM_TEST_TLS_PROJECT")
	topic := os.Getenv("LTM_TEST_TLS_TOPIC")
	if (endpoint == "" && region == "") || ak == "" || sk == "" || project == "" || topic == "" {
		t.Skip("set LTM_TEST_TLS_* to run the TLS integration test")
	}
	a, err := tlsadapter.New(tlsadapter.Config{Endpoint: endpoint, Region: region}, tlsadapter.Creds{
		AccessKeyID:     ak,
		AccessKeySecret: sk,
		SecurityToken:   os.Getenv("LTM_TEST_TLS_SECURITY_TOKEN"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx := context.Background()

	sources, err := a.ListSources(ctx, "")
	if err != nil {
		t.Fatalf("list projects: %v", err)
	}
	if len(sources.Flat) == 0 {
		t.Fatal("expected at least one project")
	}
	if _, err := a.ListSources(ctx, project); err != nil {
		t.Fatalf("list topics: %v", err)
	}
	if _, err := a.DescribeSchema(ctx, project, topic); err != nil {
		t.Fatalf("describe schema: %v", err)
	}

	to := time.Now()
	from := to.Add(-time.Hour)
	req := sdk.SearchReq{Tenant: "it", Project: project, Source: topic, From: from, To: to, Limit: 5}
	res, err := a.Search(ctx, req)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if res.Raw == nil {
		t.Fatal("raw must be preserved")
	}
	if _, err := a.Count(ctx, req); err != nil {
		t.Fatalf("count: %v", err)
	}
	if _, err := a.NativeQuery(ctx, sdk.NativeReq{Tenant: "it", Project: project, Source: topic,
		Language: "sql", Raw: "* | SELECT count(*) AS c", From: from, To: to, HasTime: true}); err != nil {
		t.Fatalf("native sql: %v", err)
	}
}
