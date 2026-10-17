package sdk

import (
	"fmt"
	"strings"
)

// ResolveSLS resolves the SLS project and logstore from the explicit project
// tool parameter and the source (logstore).
//
//   - When project is set, source must be a bare logstore name.
//   - When project is empty, a "project/logstore" source is split.
//
// The project is never taken from tenant configuration; it is always supplied
// per call.
func ResolveSLS(project, source string) (string, string, error) {
	source = strings.TrimSpace(source)
	project = strings.TrimSpace(project)
	if source == "" {
		return "", "", fmt.Errorf("source is required")
	}
	if project != "" {
		if strings.Contains(source, "/") {
			return "", "", fmt.Errorf("source %q must be a logstore when project is set", source)
		}
		return project, source, nil
	}
	if p, l, ok := strings.Cut(source, "/"); ok {
		p, l = strings.TrimSpace(p), strings.TrimSpace(l)
		if p == "" || l == "" {
			return "", "", fmt.Errorf("invalid source %q: expected project/logstore", source)
		}
		return p, l, nil
	}
	return "", "", fmt.Errorf("project is required (pass it as the project tool parameter, or use source=\"project/logstore\")")
}
