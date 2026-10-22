package tls

import (
	"context"
	"fmt"
	"strings"

	tlssdk "github.com/volcengine/volc-sdk-golang/service/tls"

	"github.com/hchw/log-trace-mcp/internal/sdk"
)

// ListSources returns projects when project is empty, otherwise the topics of
// that project. Both listings follow pagination until the reported total is
// reached or a page comes back empty.
func (a *Adapter) ListSources(ctx context.Context, project string) (sdk.Sources, error) {
	if strings.TrimSpace(project) == "" {
		return a.listProjects(ctx)
	}
	return a.listTopics(ctx, strings.TrimSpace(project))
}

func (a *Adapter) listProjects(ctx context.Context) (sdk.Sources, error) {
	var flat []sdk.Source
	var raws []*tlssdk.DescribeProjectsResponse
	for page := 1; page <= maxPages; page++ {
		if err := ctx.Err(); err != nil {
			return sdk.Sources{}, err
		}
		resp, err := a.client.DescribeProjects(&tlssdk.DescribeProjectsRequest{
			PageNumber: page,
			PageSize:   defaultPageSize,
		})
		if err != nil {
			return sdk.Sources{}, fmt.Errorf("tls describe projects: %w", err)
		}
		raws = append(raws, resp)
		for _, p := range resp.Projects {
			name := p.ProjectName
			if name == "" {
				name = p.ProjectID
			}
			flat = append(flat, sdk.Source{Name: name, Kind: "project"})
		}
		if len(resp.Projects) == 0 || (resp.Total > 0 && int64(len(flat)) >= resp.Total) {
			break
		}
	}
	return sdk.Sources{Tree: flat, Flat: flat, Raw: raws}, nil
}

func (a *Adapter) listTopics(ctx context.Context, project string) (sdk.Sources, error) {
	req := &tlssdk.DescribeTopicsRequest{
		ProjectName: project,
		PageNumber:  1,
		PageSize:    defaultPageSize,
	}
	if looksLikeID(project) {
		req.ProjectName = ""
		req.ProjectID = project
	}
	resp, err := a.client.DescribeTopics(req)
	// A project ID is also accepted by the API, so retry with the other
	// identifier when the first lookup failed or found nothing.
	if err != nil || (len(resp.Topics) == 0 && resp.Total == 0) {
		if req.ProjectID == "" {
			req.ProjectName, req.ProjectID = "", project
		} else {
			req.ProjectName, req.ProjectID = project, ""
		}
		if retry, retryErr := a.client.DescribeTopics(req); retryErr == nil {
			resp, err = retry, nil
		} else if err == nil {
			err = retryErr
		}
	}
	if err != nil {
		return sdk.Sources{}, fmt.Errorf("tls describe topics: %w", err)
	}

	var flat []sdk.Source
	raws := []*tlssdk.DescribeTopicsResponse{resp}
	for {
		for _, t := range resp.Topics {
			if t == nil {
				continue
			}
			name := t.TopicName
			if name == "" {
				name = t.TopicID
			}
			flat = append(flat, sdk.Source{Name: project + "/" + name, Kind: "topic"})
		}
		if len(resp.Topics) == 0 || (resp.Total > 0 && len(flat) >= resp.Total) || req.PageNumber >= maxPages {
			break
		}
		req.PageNumber++
		resp, err = a.client.DescribeTopics(req)
		if err != nil {
			return sdk.Sources{}, fmt.Errorf("tls describe topics: %w", err)
		}
		raws = append(raws, resp)
	}
	return sdk.Sources{Tree: flat, Flat: flat, Raw: raws}, nil
}

// DescribeSchema returns the topic's full-text marker and key-value index
// fields. The complete index configuration stays in Raw.
func (a *Adapter) DescribeSchema(ctx context.Context, project, source string) (sdk.Schema, error) {
	topicID, err := a.resolve(ctx, project, source)
	if err != nil {
		return sdk.Schema{}, err
	}
	resp, err := a.client.DescribeIndex(&tlssdk.DescribeIndexRequest{TopicID: topicID})
	if err != nil {
		return sdk.Schema{}, fmt.Errorf("tls describe index: %w", err)
	}
	fields := make([]sdk.Field, 0)
	if resp.FullText != nil {
		fields = append(fields, sdk.Field{Name: "__full_text__", Type: "fulltext"})
	}
	if resp.KeyValue != nil {
		for _, kv := range *resp.KeyValue {
			fields = append(fields, sdk.Field{Name: kv.Key, Type: kv.Value.ValueType})
		}
	}
	return sdk.Schema{Fields: fields, Raw: resp}, nil
}
