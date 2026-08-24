package domain

// RiskRank provides a stable ordering for risk levels.
func RiskRank(level RiskLevel) int {
	switch level {
	case RiskHigh:
		return 3
	case RiskMedium:
		return 2
	case RiskLow:
		return 1
	default:
		return 0
	}
}
