// Package textutil holds the two string helpers that several capabilities share.
package textutil

import "strings"

// FirstNonEmpty returns the first value that is not blank once trimmed.
func FirstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// Truncate caps a value at limit bytes. Used on upstream error bodies, which can be
// arbitrarily long and end up inside an error message.
func Truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
