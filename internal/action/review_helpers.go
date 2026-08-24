package action

// ReviewDecisionValid keeps HTTP and application validation terminology aligned.
func ReviewDecisionValid(decision string) bool {
	return normalizeDecision(decision) != ""
}
