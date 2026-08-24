package domain

import "time"

// ValidWindow centralizes the basic invariant used by scheduled work.
func ValidWindow(start, end time.Time) bool {
	return !start.IsZero() && !end.IsZero() && end.After(start)
}
