package risk

// RuleMetadata identifies the explainable rule set persisted in task snapshots.
type RuleMetadata struct {
	Version string `json:"version"`
}

func CurrentRuleMetadata() RuleMetadata { return RuleMetadata{Version: RuleVersion} }
