// Package rule defines reusable privacy rules for the fullgql example.
// These are referenced from schema definitions and demonstrate the Ent-style
// pattern of separating rule logic from schema declarations.
package rule

import (
	"context"
	"fmt"
	"strings"

	"github.com/syssam/velox"
	"github.com/syssam/velox/privacy"
)

// DenyBlockedName denies mutations where the "name" field contains a blocked word.
func DenyBlockedName(blocked string) privacy.MutationRule {
	return privacy.MutationRuleFunc(func(ctx context.Context, m velox.Mutation) error {
		name, ok := m.Field("name")
		if !ok {
			return privacy.Skip
		}
		s, ok := name.(string)
		if !ok {
			return privacy.Skip
		}
		if strings.Contains(strings.ToLower(s), strings.ToLower(blocked)) {
			return fmt.Errorf("name cannot contain %q", blocked)
		}
		return privacy.Skip
	})
}

// DenyEmptyField denies mutations where the given field is an empty string.
func DenyEmptyField(field string) privacy.MutationRule {
	return privacy.MutationRuleFunc(func(ctx context.Context, m velox.Mutation) error {
		val, ok := m.Field(field)
		if !ok {
			return privacy.Skip
		}
		if s, ok := val.(string); ok && s == "" {
			return fmt.Errorf("%s cannot be empty", field)
		}
		return privacy.Skip
	})
}
