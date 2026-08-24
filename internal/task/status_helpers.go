package task

import "heritage-care/internal/domain"

// CanClose is a small, side-effect-free status guard for callers and diagnostics.
func CanClose(status domain.TaskStatus) bool { return status == domain.StatusReviewed }
