package domain

import "strings"

// HasIdentity is shared by request validators that require an actor identifier.
func HasIdentity(value string) bool { return strings.TrimSpace(value) != "" }
