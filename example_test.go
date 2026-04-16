package velox_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/syssam/velox"
)

// ExampleSchema demonstrates the schema embedding pattern used to
// define entity types. All schema methods have default no-op
// implementations, so you only override what you need.
func ExampleSchema() {
	type User struct {
		velox.Schema
	}
	u := User{}
	// Default implementations return nil/empty.
	fmt.Println("fields:", u.Fields())
	fmt.Println("edges:", u.Edges())
	fmt.Println("hooks:", u.Hooks())
	// Output:
	// fields: []
	// edges: []
	// hooks: []
}

// ExampleView demonstrates defining a read-only view schema.
// Views support queries but not mutations.
func ExampleView() {
	type UserStats struct {
		velox.View
	}
	v := UserStats{}
	// View embeds Schema, so all defaults work.
	fmt.Println("fields:", v.Fields())
	// Output:
	// fields: []
}

// ExampleMutateFunc demonstrates adapting a plain function into a
// Mutator interface. This is the building block for hooks.
func ExampleMutateFunc() {
	mutator := velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
		return fmt.Sprintf("mutated %s", m.Type()), nil
	})
	_ = mutator
	fmt.Println("mutator created")
	// Output:
	// mutator created
}

// ExampleHook demonstrates creating a logging hook that prints
// the entity type and operation before delegating to the next mutator.
func ExampleHook() {
	hook := func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
			fmt.Printf("mutation: type=%s op=%s\n", m.Type(), m.Op())
			return next.Mutate(ctx, m)
		})
	}
	// Hooks are typically registered on a client:
	//   client.Use(hook)
	_ = velox.Hook(hook)
	fmt.Println("hook registered")
	// Output:
	// hook registered
}

// ExampleInterceptFunc demonstrates creating a query interceptor
// that logs query execution.
func ExampleInterceptFunc() {
	interceptor := velox.InterceptFunc(func(next velox.Querier) velox.Querier {
		return velox.QuerierFunc(func(ctx context.Context, q velox.Query) (velox.Value, error) {
			fmt.Println("before query")
			value, err := next.Query(ctx, q)
			fmt.Println("after query")
			return value, err
		})
	})
	// Interceptors are typically registered on a client:
	//   client.Intercept(interceptor)
	_ = velox.Interceptor(interceptor)
	fmt.Println("interceptor registered")
	// Output:
	// interceptor registered
}

// ExampleTraverseFunc demonstrates creating a traverser that applies
// default filters during graph traversals. Traverse functions run at
// each traversal step, making them ideal for soft-delete filtering.
func ExampleTraverseFunc() {
	softDelete := velox.TraverseFunc(func(ctx context.Context, q velox.Query) error {
		fmt.Println("applying soft-delete filter")
		// In real usage, type-assert the query and add a WHERE clause:
		//   if uq, ok := q.(*gen.UserQuery); ok {
		//       uq.Where(user.DeletedAtIsNil())
		//   }
		return nil
	})
	// TraverseFunc also satisfies the Interceptor interface,
	// with a pass-through Intercept method.
	err := softDelete.Traverse(context.Background(), nil)
	fmt.Println("error:", err)
	// Output:
	// applying soft-delete filter
	// error: <nil>
}

// ExampleIsNotFound demonstrates checking whether an error indicates
// that a queried entity was not found.
func ExampleIsNotFound() {
	err := velox.NewNotFoundError("User")
	fmt.Println(velox.IsNotFound(err))
	fmt.Println(velox.IsNotFound(fmt.Errorf("wrapped: %w", err)))
	fmt.Println(velox.IsNotFound(errors.New("other error")))
	// Output:
	// true
	// true
	// false
}

// ExampleIsConstraintError demonstrates checking whether an error
// is a database constraint violation (e.g., unique index, foreign key).
func ExampleIsConstraintError() {
	err := velox.NewConstraintError("unique constraint on email", nil)
	fmt.Println(velox.IsConstraintError(err))
	fmt.Println(velox.IsConstraintError(errors.New("other error")))
	fmt.Println(err)
	// Output:
	// true
	// false
	// velox: constraint failed: unique constraint on email
}

// ExampleIsValidationError demonstrates checking whether an error
// is a field validation failure.
func ExampleIsValidationError() {
	err := velox.NewValidationError("email", errors.New("must be a valid email address"))
	fmt.Println(velox.IsValidationError(err))
	fmt.Println(velox.IsValidationError(errors.New("other error")))
	fmt.Println(err)
	// Output:
	// true
	// false
	// velox: validator failed for field "email": must be a valid email address
}

// ExampleNewAggregateError demonstrates collecting multiple errors
// into a single aggregate error. Nil errors are filtered out, and
// if only one non-nil error remains, it is returned directly.
func ExampleNewAggregateError() {
	// Multiple errors produce an aggregate.
	agg := velox.NewAggregateError(
		velox.NewValidationError("name", errors.New("required")),
		nil, // nil errors are filtered out
		velox.NewValidationError("email", errors.New("invalid format")),
	)
	fmt.Println(agg)

	// A single non-nil error is returned as-is (not wrapped).
	single := velox.NewAggregateError(nil, errors.New("only error"), nil)
	fmt.Println(single)

	// All nil returns nil.
	fmt.Println(velox.NewAggregateError(nil, nil) == nil)
	// Output:
	// velox: multiple errors:
	//   [1] velox: validator failed for field "name": required
	//   [2] velox: validator failed for field "email": invalid format
	// only error
	// true
}

// ExampleOp_Is demonstrates using Op.Is to check whether a mutation
// operation matches a given type. Op values are bitmasks, so Is
// performs a bitwise AND check.
func ExampleOp_Is() {
	op := velox.OpCreate
	fmt.Println("is create:", op.Is(velox.OpCreate))
	fmt.Println("is update:", op.Is(velox.OpUpdate))
	fmt.Println("is delete:", op.Is(velox.OpDelete))

	// Combined ops can be checked too.
	writeOp := velox.OpCreate | velox.OpUpdate
	fmt.Println("combined is create:", writeOp.Is(velox.OpCreate))
	fmt.Println("combined is update:", writeOp.Is(velox.OpUpdate))
	fmt.Println("combined is delete:", writeOp.Is(velox.OpDelete))
	// Output:
	// is create: true
	// is update: false
	// is delete: false
	// combined is create: true
	// combined is update: true
	// combined is delete: false
}
