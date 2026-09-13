// Package goal builds bounded, auditable task contracts for the agent.
package goal

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxObjectiveBytes = 8 * 1024

var (
	ErrEmptyObjective   = errors.New("goal objective is empty")
	ErrObjectiveTooLong = errors.New("goal objective is too long")
	ErrInvalidObjective = errors.New("goal objective contains unsupported control characters")
)

// Build converts one objective into the model-facing contract used by goal mode.
// The objective is preserved verbatim after outer whitespace is removed.
func Build(objective string) (string, error) {
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return "", ErrEmptyObjective
	}
	if len(objective) > MaxObjectiveBytes {
		return "", ErrObjectiveTooLong
	}
	if !utf8.ValidString(objective) {
		return "", ErrInvalidObjective
	}
	for _, r := range objective {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return "", ErrInvalidObjective
		}
	}

	return fmt.Sprintf(`GOAL MODE IS ACTIVE.
Treat this message as an execution contract, not as a request for a shallow summary.

GOAL:
%s

CONTEXT:
- Read the nearest repository instructions and the minimum relevant source, tests, docs, logs, and configuration before changing or judging anything.
- Separate confirmed facts from assumptions and preserve exact evidence paths.

CONSTRAINTS:
- Keep scope limited to this goal; do not add unrelated cleanup.
- Respect repository instructions and existing patterns.
- Verification integrity: do not weaken or bypass tests, assertions, lint, typecheck, validation, generated-output checks, screenshots, or external blockers to make the goal pass; fix the root cause or report the blocker.
- Ask before secrets, production access, destructive data operations, or unresolved product decisions.

DONE WHEN:
- The requested outcome exists in the current working tree or the exact blocker is documented.
- Every material claim is backed by inspected source or fresh verification.
- Broad reviews cover architecture, correctness, security, performance, tests, CI, and release configuration; counting files or searching TODO/FIXME alone is never completion.

VERIFY:
- Run the repository's relevant fresh checks after changes.
- Record the exact commands run, their results, and any checks that could not run.
- Re-read the final diff and compare the current state with GOAL and DONE WHEN.

OUTPUT:
- Summarize changed files or reviewed areas, key decisions, verification evidence, remaining risks, and the next action.
- Label unverified risks as hypotheses, not findings.

STOP RULES:
- Stop instead of guessing when required context, credentials, owner approval, or a destructive decision is missing.
- After three failed attempts on the same symptom, revisit the root-cause hypothesis and report the blocker.
- Do not claim completion until DONE WHEN and VERIFY are satisfied.

Begin by deriving a concise checklist from this contract, then execute it.`, objective), nil
}
