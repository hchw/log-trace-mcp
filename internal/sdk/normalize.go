package sdk

import "sort"

// NormalizeRecords converts a list of string maps into stable columns and rows.
// Column order follows first appearance, with any extra keys appended sorted.
func NormalizeRecords(records []map[string]string) ([]string, [][]any) {
	var cols []string
	seen := map[string]bool{}
	for _, r := range records {
		for k := range r {
			if !seen[k] {
				seen[k] = true
				cols = append(cols, k)
			}
		}
	}
	rows := make([][]any, 0, len(records))
	for _, r := range records {
		row := make([]any, len(cols))
		for i, c := range cols {
			row[i] = r[c]
		}
		rows = append(rows, row)
	}
	return cols, rows
}

// NormalizeAnyRecords converts typed records into stable columns and rows.
// Values keep their original types. Columns are the sorted union of all keys,
// which makes the output deterministic across records.
func NormalizeAnyRecords(records []map[string]any) ([]string, [][]any) {
	seen := map[string]bool{}
	for _, r := range records {
		for k := range r {
			seen[k] = true
		}
	}
	cols := make([]string, 0, len(seen))
	for k := range seen {
		cols = append(cols, k)
	}
	sort.Strings(cols)
	rows := make([][]any, 0, len(records))
	for _, r := range records {
		row := make([]any, len(cols))
		for i, c := range cols {
			row[i] = r[c]
		}
		rows = append(rows, row)
	}
	return cols, rows
}

// NormalizeAnalysis converts an analysis result with an explicit schema into
// columns and rows. Schema order is preserved; any extra keys are appended
// sorted so no field is dropped.
func NormalizeAnalysis(schema []string, data []map[string]any) ([]string, [][]any) {
	cols := make([]string, 0, len(schema))
	seen := map[string]bool{}
	for _, c := range schema {
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		cols = append(cols, c)
	}
	extra := map[string]bool{}
	for _, r := range data {
		for k := range r {
			if !seen[k] {
				extra[k] = true
			}
		}
	}
	extraKeys := make([]string, 0, len(extra))
	for k := range extra {
		extraKeys = append(extraKeys, k)
	}
	sort.Strings(extraKeys)
	cols = append(cols, extraKeys...)
	rows := make([][]any, 0, len(data))
	for _, r := range data {
		row := make([]any, len(cols))
		for i, c := range cols {
			row[i] = r[c]
		}
		rows = append(rows, row)
	}
	return cols, rows
}

// SortedKeys returns the sorted keys of a string map.
func SortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
