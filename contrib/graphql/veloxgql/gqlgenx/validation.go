package gqlgenx

import (
	"maps"
	"slices"
	"sync"

	"github.com/vektah/gqlparser/v2/validator/core"
	"github.com/vektah/gqlparser/v2/validator/rules"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
)

// QuietValidation returns the rules function for gqlgen's
// SetValidationRulesFn that validates as gqlgen does, with no "Did you
// mean" suggestions and at most the MaxErrors of db's limits
// (veloxgql.LimitsOf; veloxgql.DefaultLimits() for a nil db). It is opt-in,
// for a server open to clients, with SetDisableSuggestion:
//
//	srv.SetDisableSuggestion(true)
//	srv.SetValidationRulesFn(gqlgenx.QuietValidation(db))
//
// gqlgen's SetDisableSuggestion alone quiets one rule, an unknown field's.
// QuietValidation also quiets those of an unknown argument, type, enum or
// input value, and a scalar's subfield: each suggestion names the schema's
// fields and takes about 25 KB. An error over MaxErrors is left out, with
// one error that says so.
//
// The rules run as one, so a request gqlgen has not cached makes about 70
// allocations fewer than under gqlgen's 28 rules. That one rule has the
// name gqlgen gives the field rule under SetDisableSuggestion, which keeps
// it; without SetDisableSuggestion gqlgen removes it, and validation runs
// only gqlgen's field rule.
func QuietValidation(db *velox.DB) func() *rules.Rules {
	most := veloxgql.LimitsOf(db).MaxErrors
	return func() *rules.Rules { return quietValidation(most) }
}

var (
	quietOnce sync.Once
	quiet     []core.RuleFunc
)

// quietRules are gqlparser's default rules with no suggestions, in the
// order of their names.
func quietRules() []core.RuleFunc {
	quietOnce.Do(func() {
		d := rules.NewDefaultRules()
		for _, r := range []struct {
			name string
			rule core.Rule
		}{
			{"FieldsOnCorrectType", rules.FieldsOnCorrectTypeRuleWithoutSuggestions},
			{"KnownArgumentNames", rules.KnownArgumentNamesRuleWithoutSuggestions},
			{"KnownTypeNames", rules.KnownTypeNamesRuleWithoutSuggestions},
			{"ScalarLeafs", rules.ScalarLeafsRuleWithoutSuggestions},
			{"ValuesOfCorrectType", rules.ValuesOfCorrectTypeRuleWithoutSuggestions},
		} {
			d.RemoveRule(r.name)
			d.AddRule(r.rule.Name, r.rule.RuleFunc)
		}
		inner := d.GetInner()
		for _, name := range slices.Sorted(maps.Keys(inner)) {
			quiet = append(quiet, inner[name])
		}
	})
	return quiet
}

// quietValidation is quietRules as one rule, holding them to at most most
// errors, for one validation. Its rules have the names of those gqlgen
// adds when they are absent under SetDisableSuggestion (one at v0.17.86,
// two at v0.17.98), so that it adds none.
func quietValidation(most int) *rules.Rules {
	return rules.NewRules(core.Rule{
		Name:     rules.ScalarLeafsRuleWithoutSuggestions.Name,
		RuleFunc: func(*core.Events, core.AddErrFunc) {},
	}, core.Rule{
		Name: rules.FieldsOnCorrectTypeRuleWithoutSuggestions.Name,
		RuleFunc: func(obs *core.Events, addError core.AddErrFunc) {
			n := 0
			limited := func(opts ...core.ErrorOption) {
				switch n++; {
				case n <= most:
					addError(opts...)
				case n == most+1:
					addError(core.Message("over %d validation errors: the rest are left out", most))
				}
			}
			for _, rule := range quietRules() {
				rule(obs, limited)
			}
		},
	})
}
