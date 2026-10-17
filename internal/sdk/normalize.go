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

// SortedKeys returns the sorted keys of a string map.
func SortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
