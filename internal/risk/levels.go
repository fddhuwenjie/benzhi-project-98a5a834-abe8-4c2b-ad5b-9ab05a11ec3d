package risk

import "heritage-care/internal/domain"

// AtLeast answers threshold comparisons without exposing scoring internals.
func AtLeast(level, minimum domain.RiskLevel) bool {
	return domain.RiskRank(level) >= domain.RiskRank(minimum)
}
