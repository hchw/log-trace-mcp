package sdk

import (
	"reflect"
	"testing"
)

func TestNormalizeAnyRecordsPreservesTypes(t *testing.T) {
	cols, rows := NormalizeAnyRecords([]map[string]any{
		{"level": "ERROR", "status": 500, "ok": false},
		{"level": "INFO", "status": 200, "ok": true},
	})
	wantCols := []string{"level", "ok", "status"}
	if !reflect.DeepEqual(cols, wantCols) {
		t.Fatalf("cols = %v, want %v", cols, wantCols)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if _, ok := rows[0][2].(int); !ok {
		t.Fatalf("status lost its type: %T", rows[0][2])
	}
	if _, ok := rows[0][1].(bool); !ok {
		t.Fatalf("ok lost its type: %T", rows[0][1])
	}
}

func TestNormalizeAnyRecordsDeterministic(t *testing.T) {
	records := []map[string]any{{"b": 2, "a": 1}, {"c": 3}}
	first, _ := NormalizeAnyRecords(records)
	for i := 0; i < 20; i++ {
		again, _ := NormalizeAnyRecords(records)
		if !reflect.DeepEqual(first, again) {
			t.Fatalf("column order is not deterministic: %v vs %v", first, again)
		}
	}
}

func TestNormalizeAnalysisKeepsSchemaOrderAndExtras(t *testing.T) {
	cols, rows := NormalizeAnalysis(
		[]string{"bucket", "c"},
		[]map[string]any{{"c": float64(3), "bucket": "t1", "extra": "x"}},
	)
	wantCols := []string{"bucket", "c", "extra"}
	if !reflect.DeepEqual(cols, wantCols) {
		t.Fatalf("cols = %v, want %v", cols, wantCols)
	}
	if rows[0][1] != float64(3) {
		t.Fatalf("count type/value lost: %#v", rows[0][1])
	}
}

func TestNormalizeRecordsStringMapUnchanged(t *testing.T) {
	cols, rows := NormalizeRecords([]map[string]string{
		{"level": "ERROR", "msg": "boom"},
	})
	if len(cols) != 2 || len(rows) != 1 {
		t.Fatalf("unexpected shape: cols=%v rows=%v", cols, rows)
	}
	if len(rows[0]) != 2 {
		t.Fatalf("unexpected row width: %v", rows[0])
	}
}
