package goal

import (
	"context"

	"nabd/internal/agent"
)

// Runner is the narrow execution boundary shared with both terminal views.
// Implementations retain ownership of journaling, permissions, tools, and cancellation.
type Runner interface {
	Run(ctx context.Context, text string) error
}

// Compile-time proof that *agent.Loop satisfies Runner.
// If this assertion breaks, the goal mode integration is broken.
var _ Runner = (*agent.Loop)(nil)

// Run builds a validated goal contract and sends it through the ordinary
// agent runner. Goal mode therefore cannot bypass the permission gate or
// create a second execution path.
func Run(ctx context.Context, runner Runner, objective string) error {
	contract, err := Build(objective)
	if err != nil {
		return err
	}
	if runner == nil {
		return ErrNoRunner
	}
	return runner.Run(ctx, contract)
}
