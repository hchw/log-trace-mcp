package sls

import (
	"context"
	"testing"
	"time"

	slssdk "github.com/aliyun/aliyun-log-go-sdk"

	"github.com/hchw/log-trace-mcp/internal/sdk"
)

// histClient embeds the big SLS interface so only the method under test needs
// an implementation.
type histClient struct {
	slssdk.ClientInterface
	resp  *slssdk.GetHistogramsResponse
	query string
}

func (h *histClient) GetHistograms(project, logstore, topic string, from, to int64, q string) (*slssdk.GetHistogramsResponse, error) {
	h.query = q
	return h.resp, nil
}

func TestCountUsesHistogramTotal(t *testing.T) {
	fc := &histClient{resp: &slssdk.GetHistogramsResponse{Count: 42, Progress: "Complete"}}
	a := &Adapter{client: fc}

	n, err := a.Count(context.Background(), sdk.SearchReq{
		Project: "p", Source: "s", Text: "level:ERROR",
		From: time.Unix(0, 0), To: time.Unix(60, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 42 {
		t.Fatalf("Count = %d, want 42", n)
	}
	if fc.query != "level:ERROR" {
		t.Fatalf("query = %q", fc.query)
	}
}
