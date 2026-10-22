// Package sdk defines the backend-agnostic log query model shared by adapters
// and the tool layer.
package sdk

import (
	"context"
	"time"
)

// EngineType identifies a log backend implementation.
type EngineType string

const (
	EngineSLS        EngineType = "sls"
	EngineOpenSearch EngineType = "opensearch"
	EngineTLS        EngineType = "tls"
)

// Field describes one field of a data set schema.
type Field struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// Schema is the described structure of a data set.
type Schema struct {
	Fields []Field `json:"fields"`
	Raw    any     `json:"raw,omitempty"`
}

// Source identifies a data set within a log center.
type Source struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// Sources is the result of a data source discovery call.
type Sources struct {
	Tree []Source `json:"tree"`
	Flat []Source `json:"flat"`
	Raw  any      `json:"raw,omitempty"`
}

// SearchReq is the unified semantic query request (Tier 1).
type SearchReq struct {
	Tenant  string            `json:"tenant"`
	Project string            `json:"project,omitempty"` // SLS project; ignored by OpenSearch
	Source  string            `json:"source"`            // logstore (SLS) or index (OpenSearch)
	Text    string            `json:"text,omitempty"`
	Filter  map[string]string `json:"filter,omitempty"`
	From    time.Time         `json:"from"`
	To      time.Time         `json:"to"`
	Limit   int               `json:"limit,omitempty"`
	Offset  int               `json:"offset,omitempty"`
	Sort    string            `json:"sort,omitempty"`
}

// Result is the unified query result envelope. Raw always carries the
// untouched backend response so no information is lost.
type Result struct {
	Engine     string         `json:"engine"`
	Source     string         `json:"source"`
	Normalized bool           `json:"normalized"`
	Columns    []string       `json:"columns,omitempty"`
	Rows       [][]any        `json:"rows,omitempty"`
	Meta       map[string]any `json:"meta,omitempty"`
	Raw        any            `json:"raw,omitempty"`
}

// NativeReq is a Tier 2 passthrough request.
type NativeReq struct {
	Tenant   string    `json:"tenant"`
	Project  string    `json:"project,omitempty"` // SLS project; ignored by OpenSearch
	Source   string    `json:"source"`
	Language string    `json:"language"`
	Raw      any       `json:"raw"`
	From     time.Time `json:"from,omitempty"`
	To       time.Time `json:"to,omitempty"`
	HasTime  bool      `json:"-"`
	Limit    int       `json:"limit,omitempty"`
	Offset   int       `json:"offset,omitempty"`
}

// AnalyzeReq extends SearchReq with an optional grouping field.
type AnalyzeReq struct {
	SearchReq
	GroupBy string `json:"group_by,omitempty"`
}

// Bucket is one histogram or group bucket.
type Bucket struct {
	Key   string `json:"key"`
	Count int64  `json:"count"`
}

// AnalyzeResult is the output of an analyze call.
type AnalyzeResult struct {
	Histogram []Bucket `json:"histogram,omitempty"`
	Top       []Bucket `json:"top,omitempty"`
	Raw       any      `json:"raw,omitempty"`
}

// ContextReq asks for logs surrounding an anchor.
type ContextReq struct {
	Tenant  string            `json:"tenant"`
	Project string            `json:"project,omitempty"` // SLS project
	Source  string            `json:"source"`
	Anchor  map[string]string `json:"anchor"`
	Before  int               `json:"before,omitempty"`
	After   int               `json:"after,omitempty"`
}

// ContextResult is the output of a context query.
type ContextResult struct {
	Columns []string `json:"columns,omitempty"`
	Rows    [][]any  `json:"rows,omitempty"`
	Raw     any      `json:"raw,omitempty"`
}

// CompareResult holds two time-window results and their difference.
type CompareResult struct {
	A    Result         `json:"a"`
	B    Result         `json:"b"`
	Diff map[string]any `json:"diff"`
}

// Adapter is implemented by each backend.
type Adapter interface {
	// ListSources lists data sets. For SLS, project selects the project to list
	// logstores from (empty lists projects); OpenSearch ignores project.
	ListSources(ctx context.Context, project string) (Sources, error)
	DescribeSchema(ctx context.Context, project, source string) (Schema, error)
	Search(ctx context.Context, req SearchReq) (Result, error)
	Count(ctx context.Context, req SearchReq) (int64, error)
	NativeQuery(ctx context.Context, req NativeReq) (Result, error)
	Close() error
}

// AnalyzeCapable is implemented by adapters that can aggregate.
type AnalyzeCapable interface {
	Analyze(ctx context.Context, req AnalyzeReq) (AnalyzeResult, error)
}

// ContextCapable is implemented by adapters that support context logs.
type ContextCapable interface {
	ContextLogs(ctx context.Context, req ContextReq) (ContextResult, error)
}

// RawCapable is implemented by adapters that can issue engine-specific raw
// requests, backing Tier 3 tools.
type RawCapable interface {
	RawRequest(ctx context.Context, method, path string, body any) (any, error)
}

// LanguagesFor returns the native query languages valid for an engine.
func LanguagesFor(t EngineType) []string {
	switch t {
	case EngineSLS:
		return []string{"sql", "spl"}
	case EngineOpenSearch:
		return []string{"dsl", "ppl"}
	case EngineTLS:
		return []string{"sql", "search"}
	default:
		return nil
	}
}

// ValidLanguage reports whether lang is accepted by the engine.
func ValidLanguage(t EngineType, lang string) bool {
	for _, l := range LanguagesFor(t) {
		if l == lang {
			return true
		}
	}
	return false
}
