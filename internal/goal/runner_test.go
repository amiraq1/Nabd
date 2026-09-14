package goal

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type recordingRunner struct {
	calls int
	text  string
	err   error
}

func (r *recordingRunner) Run(_ context.Context, text string) error {
	r.calls++
	r.text = text
	return r.err
}

func TestRunBuildsContractAndUsesRunnerOnce(t *testing.T) {
	r := &recordingRunner{}
	if err := Run(context.Background(), r, "راجع المستودع"); err != nil {
		t.Fatal(err)
	}
	if r.calls != 1 {
		t.Fatalf("calls = %d, want 1", r.calls)
	}
	if !strings.Contains(r.text, "GOAL:\nراجع المستودع") {
		t.Fatalf("runner received non-contract text: %q", r.text)
	}
}

func TestRunRejectsBeforeCallingRunner(t *testing.T) {
	r := &recordingRunner{}
	if err := Run(context.Background(), r, " "); !errors.Is(err, ErrEmptyObjective) {
		t.Fatalf("error = %v", err)
	}
	if r.calls != 0 {
		t.Fatalf("invalid objective invoked runner %d times", r.calls)
	}
}

func TestRunRejectsNilRunner(t *testing.T) {
	if err := Run(context.Background(), nil, "valid objective"); !errors.Is(err, ErrNoRunner) {
		t.Fatalf("error = %v", err)
	}
}

func TestRunPropagatesRunnerError(t *testing.T) {
	want := errors.New("runner failed")
	r := &recordingRunner{err: want}
	if err := Run(context.Background(), r, "valid objective"); !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}
