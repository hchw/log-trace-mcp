package sdk

import (
	"context"
	"sync/atomic"
	"testing"
)

type fakeAdapter struct {
	engine  EngineType
	closed  atomic.Int32
	sources Sources
}

func (f *fakeAdapter) ListSources(ctx context.Context, parent string) (Sources, error) {
	return f.sources, nil
}
func (f *fakeAdapter) DescribeSchema(ctx context.Context, project, source string) (Schema, error) {
	return Schema{}, nil
}
func (f *fakeAdapter) Search(ctx context.Context, req SearchReq) (Result, error) {
	return Result{Engine: string(f.engine), Source: req.Source, Rows: [][]any{{1}}}, nil
}
func (f *fakeAdapter) Count(ctx context.Context, req SearchReq) (int64, error) { return 1, nil }
func (f *fakeAdapter) NativeQuery(ctx context.Context, req NativeReq) (Result, error) {
	return Result{Engine: string(f.engine), Raw: req.Raw}, nil
}
func (f *fakeAdapter) Close() error { f.closed.Add(1); return nil }

func TestRegistryRouting(t *testing.T) {
	r := NewRegistry()
	a := &fakeAdapter{engine: EngineSLS}
	r.Replace(map[string]AdapterInfo{"a": {Adapter: a, Engine: EngineSLS}})

	ad, eng, release, err := r.Acquire("a")
	if err != nil {
		t.Fatal(err)
	}
	if eng != EngineSLS {
		t.Fatalf("wrong engine %q", eng)
	}
	if _, ok := ad.(*fakeAdapter); !ok {
		t.Fatal("wrong adapter type")
	}
	release()

	if _, _, _, err := r.Acquire("missing"); err == nil {
		t.Fatal("expected error for missing tenant")
	}
}

func TestRegistryHotSwapClosesRetiredAfterRelease(t *testing.T) {
	r := NewRegistry()
	oldA := &fakeAdapter{engine: EngineSLS}
	r.Replace(map[string]AdapterInfo{"a": {Adapter: oldA, Engine: EngineSLS}})

	_, _, release, err := r.Acquire("a")
	if err != nil {
		t.Fatal(err)
	}

	newA := &fakeAdapter{engine: EngineSLS}
	r.Replace(map[string]AdapterInfo{"a": {Adapter: newA, Engine: EngineSLS}})

	if oldA.closed.Load() != 0 {
		t.Fatal("old adapter closed while still referenced")
	}
	release()
	if oldA.closed.Load() != 1 {
		t.Fatal("old adapter should be closed after release")
	}
}

func TestRegistryReplacesImmediately(t *testing.T) {
	r := NewRegistry()
	oldA := &fakeAdapter{engine: EngineSLS}
	r.Replace(map[string]AdapterInfo{"a": {Adapter: oldA, Engine: EngineSLS}})
	newA := &fakeAdapter{engine: EngineOpenSearch}
	r.Replace(map[string]AdapterInfo{"a": {Adapter: newA, Engine: EngineOpenSearch}})

	ad, eng, release, _ := r.Acquire("a")
	defer release()
	if eng != EngineOpenSearch {
		t.Fatalf("new requests should use new adapter, got %q", eng)
	}
	if ad != Adapter(newA) {
		t.Fatal("expected new adapter instance")
	}
}
