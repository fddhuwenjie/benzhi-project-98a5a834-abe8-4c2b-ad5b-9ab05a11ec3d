package domain

// IsTerminalStatus reports whether a task can no longer enter another workflow stage.
func IsTerminalStatus(status TaskStatus) bool {
	return status == StatusClosed
}

// IsReviewableStatus reports whether a task has reached the review stage.
func IsReviewableStatus(status TaskStatus) bool {
	return status == StatusPendingReview || status == StatusReviewed
}
