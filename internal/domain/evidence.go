package domain

import "strings"

// CleanEvidenceRefs removes surrounding whitespace while preserving order.
func CleanEvidenceRefs(refs []string) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if cleaned := strings.TrimSpace(ref); cleaned != "" {
			out = append(out, cleaned)
		}
	}
	return out
}
