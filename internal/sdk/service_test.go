package sdk

import (
	"context"
	"testing"
)

func TestServiceNativeQueryLanguageValidation(t *testing.T) {
	r := NewRegistry()
	r.Replace(map[string]AdapterInfo{
		"sls-a": {Adapter: &fakeAdapter{engine: EngineSLS}, Engine: EngineSLS},
		"os-a":  {Adapter: &fakeAdapter{engine: EngineOpenSearch}, Engine: EngineOpenSearch},
	})
	svc := NewService(r)
	ctx := context.Background()

	if _, err := svc.NativeQuery(ctx, NativeReq{Tenant: "sls-a", Language: "sql", Raw: "SELECT 1"}); err != nil {
		t.Fatalf("valid sls sql rejected: %v", err)
	}
	if _, err := svc.NativeQuery(ctx, NativeReq{Tenant: "os-a", Language: "dsl", Raw: "{}"}); err != nil {
		t.Fatalf("valid os dsl rejected: %v", err)
	}
	if _, err := svc.NativeQuery(ctx, NativeReq{Tenant: "os-a", Language: "sql", Raw: "SELECT 1"}); err == nil {
		t.Fatal("expected language/tenant mismatch error")
	}
	if _, err := svc.NativeQuery(ctx, NativeReq{Tenant: "sls-a", Language: "ppl", Raw: "x"}); err == nil {
		t.Fatal("expected language/tenant mismatch error")
	}
}

func TestServiceCompare(t *testing.T) {
	r := NewRegistry()
	r.Replace(map[string]AdapterInfo{"a": {Adapter: &fakeAdapter{engine: EngineSLS}, Engine: EngineSLS}})
	svc := NewService(r)
	res, err := svc.Compare(context.Background(),
		SearchReq{Tenant: "a", Source: "p/l"},
		SearchReq{Tenant: "a", Source: "p/l"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Diff == nil {
		t.Fatal("expected diff")
	}
}

func TestServiceUnknownEnv(t *testing.T) {
	svc := NewService(NewRegistry())
	if _, err := svc.Search(context.Background(), SearchReq{Tenant: "nope"}); err == nil {
		t.Fatal("expected error for unknown tenant")
	}
}
