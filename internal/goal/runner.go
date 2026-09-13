package goal

import "context"

// Runner is the narrow execution boundary shared with both terminal views.
// Implementations retain ownership of journaling, permissions, tools, and cancellation.
type Runner interface {
	Run(ctx context.Context, text string) error
}

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
