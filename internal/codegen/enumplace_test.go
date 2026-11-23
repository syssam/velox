package codegen

import (
	"strings"
	"testing"

	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
)

// EpLevel is an enum type of the application.
type EpLevel string

// Values lists EpLevel's values.
func (EpLevel) Values() []string { return []string{"low", "high"} }

// EpTier is an enum type of the application that one entity uses.
type EpTier string

// Values lists EpTier's values.
func (EpTier) Values() []string { return []string{"basic", "pro"} }

// EpTask and EpBug share EpLevel; EpTask alone has two fields of EpTier,
// and a generated enum.
type EpTask struct{ schema.Schema }

func (EpTask) Fields() []schema.Field {
	return []schema.Field{
		field.Enum("state").Values("open", "done"),
		field.EnumOf[EpLevel]("level"),
		field.EnumOf[EpTier]("tier"),
		field.EnumOf[EpTier]("old_tier").Optional().Nillable(),
	}
}

type EpBug struct{ schema.Schema }

func (EpBug) Fields() []schema.Field {
	return []schema.Field{field.EnumOf[EpLevel]("level")}
}

// An enum type one entity uses is declared in its file, and so is the
// validator of an application's enum type only its Check calls; velox.go
// declares the shared ones (decision 0086), and each Check calls the one
// validator of its type.
func TestEnumPlacement(t *testing.T) {
	src := rendered(t, EpTask{}, EpBug{}, Budget{}, BudgetPeriod{})
	in := func(file, decl string) {
		t.Helper()
		if n := strings.Count(src[file], decl); n != 1 {
			t.Errorf("%s declares %q %d times, want once", file, decl, n)
		}
		for name, s := range src {
			if name != file && strings.Contains(s, decl) {
				t.Errorf("%s declares %q too", name, decl)
			}
		}
	}
	in("ep_task.go", "type EpTaskState string")
	in("ep_task.go", "func veloxCheckEpTier(x codegen.EpTier) error {")
	in("velox.go", "func veloxCheckEpLevel(x codegen.EpLevel) error {")
	in("velox.go", "type BudgetPeriodType string")
	if n := strings.Count(src["ep_task.go"], "if err := veloxCheckEpTier(x); err != nil {"); n != 2 {
		t.Errorf("EpTask's Check calls veloxCheckEpTier %d times, want 2", n)
	}
	if !strings.Contains(src["ep_bug.go"], "if err := veloxCheckEpLevel(x); err != nil {") {
		t.Error("EpBug's Check does not call veloxCheckEpLevel")
	}
	if strings.Contains(src["ep_bug.go"], `"errors"`) {
		t.Error("ep_bug.go imports errors, which only velox.go's validator uses")
	}
	if !strings.Contains(src["velox.go"], "\t\"errors\"\n\n\t\"github.com/syssam/velox\"\n\t\"github.com/syssam/velox/internal/codegen\"\n") {
		t.Errorf("velox.go's imports:\n%s", src["velox.go"])
	}
	if !strings.Contains(src["velox.go"], `return errors.New("is not one of low, high")`) {
		t.Error("velox.go's validator lost the message")
	}
}
