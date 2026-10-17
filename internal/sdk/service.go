package sdk

import (
	"context"
	"fmt"
)

// Service exposes unified operations over the registry.
type Service struct {
	reg *Registry
}

// NewService builds a Service.
func NewService(reg *Registry) *Service { return &Service{reg: reg} }

// Registry returns the underlying registry.
func (s *Service) Registry() *Registry { return s.reg }

func (s *Service) acquire(tenant string) (Adapter, EngineType, func(), error) {
	return s.reg.Acquire(tenant)
}

// ListSources discovers data sources for a tenant. project selects the SLS
// project to drill into; OpenSearch ignores it.
func (s *Service) ListSources(ctx context.Context, tenant, project string) (Sources, error) {
	a, _, release, err := s.acquire(tenant)
	if err != nil {
		return Sources{}, err
	}
	defer release()
	return a.ListSources(ctx, project)
}

// DescribeSchema describes the schema of a data set.
func (s *Service) DescribeSchema(ctx context.Context, tenant, project, source string) (Schema, error) {
	a, _, release, err := s.acquire(tenant)
	if err != nil {
		return Schema{}, err
	}
	defer release()
	return a.DescribeSchema(ctx, project, source)
}

// Search runs a unified semantic query.
func (s *Service) Search(ctx context.Context, req SearchReq) (Result, error) {
	a, _, release, err := s.acquire(req.Tenant)
	if err != nil {
		return Result{}, err
	}
	defer release()
	return a.Search(ctx, req)
}

// Count returns the number of matching records.
func (s *Service) Count(ctx context.Context, req SearchReq) (int64, error) {
	a, _, release, err := s.acquire(req.Tenant)
	if err != nil {
		return 0, err
	}
	defer release()
	return a.Count(ctx, req)
}

// Compare runs two searches over different windows and reports the difference.
func (s *Service) Compare(ctx context.Context, a, b SearchReq) (CompareResult, error) {
	ra, err := s.Search(ctx, a)
	if err != nil {
		return CompareResult{}, err
	}
	rb, err := s.Search(ctx, b)
	if err != nil {
		return CompareResult{}, err
	}
	ca, cb := resultCount(ra), resultCount(rb)
	return CompareResult{
		A: ra,
		B: rb,
		Diff: map[string]any{
			"a_count": ca,
			"b_count": cb,
			"delta":   cb - ca,
		},
	}, nil
}

func resultCount(r Result) int64 {
	if r.Meta != nil {
		if v, ok := r.Meta["count"]; ok {
			switch n := v.(type) {
			case int64:
				return n
			case int:
				return int64(n)
			case float64:
				return int64(n)
			}
		}
	}
	return int64(len(r.Rows))
}

// Analyze aggregates a data set, delegating to adapters that support it.
func (s *Service) Analyze(ctx context.Context, req AnalyzeReq) (AnalyzeResult, error) {
	a, _, release, err := s.acquire(req.Tenant)
	if err != nil {
		return AnalyzeResult{}, err
	}
	defer release()
	ac, ok := a.(AnalyzeCapable)
	if !ok {
		return AnalyzeResult{}, fmt.Errorf("analyze is not supported for tenant %q", req.Tenant)
	}
	return ac.Analyze(ctx, req)
}

// ContextLogs returns logs surrounding an anchor.
func (s *Service) ContextLogs(ctx context.Context, req ContextReq) (ContextResult, error) {
	a, _, release, err := s.acquire(req.Tenant)
	if err != nil {
		return ContextResult{}, err
	}
	defer release()
	cc, ok := a.(ContextCapable)
	if !ok {
		return ContextResult{}, fmt.Errorf("get_context is not supported for tenant %q", req.Tenant)
	}
	return cc.ContextLogs(ctx, req)
}

// NativeQuery passes a native query through to the backend, validating the
// language against the tenant's engine type.
func (s *Service) NativeQuery(ctx context.Context, req NativeReq) (Result, error) {
	a, engine, release, err := s.acquire(req.Tenant)
	if err != nil {
		return Result{}, err
	}
	defer release()
	if !ValidLanguage(engine, req.Language) {
		return Result{}, fmt.Errorf("language %q is not valid for tenant %q (%s); valid: %v",
			req.Language, req.Tenant, engine, LanguagesFor(engine))
	}
	return a.NativeQuery(ctx, req)
}

// RawRequest issues an engine-specific raw request via adapters that support it.
func (s *Service) RawRequest(ctx context.Context, tenant, method, path string, body any) (any, error) {
	a, _, release, err := s.acquire(tenant)
	if err != nil {
		return nil, err
	}
	defer release()
	rc, ok := a.(RawCapable)
	if !ok {
		return nil, fmt.Errorf("raw requests are not supported for tenant %q", tenant)
	}
	return rc.RawRequest(ctx, method, path, body)
}
