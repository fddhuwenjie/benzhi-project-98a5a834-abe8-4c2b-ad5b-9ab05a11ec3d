package task

import "time"

// WindowDuration reports the scheduled duration in a single place.
func WindowDuration(start, end time.Time) time.Duration { return end.Sub(start) }
