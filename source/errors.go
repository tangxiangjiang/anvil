package anvil

import (
	"fmt"

	"github.com/anvil-dev/anvil/internal/runtime"
)

// Error is a startup / transport / config failure before or around a run.
// Distinguish from RunResult.Status == StatusError (run started then failed).
type Error struct {
	Message   string
	Retryable bool
	Cause     error
}

// IsMaxStepsExceeded reports whether a run failed because MaxSteps was hit.
// Pass RunResult.Err or an error string from the stream error event.
func IsMaxStepsExceeded(errText string) bool {
	return runtime.IsMaxStepsExceeded(errText)
}

// IsMaxStepsExceededResult reports whether r ended due to the step limit.
func IsMaxStepsExceededResult(r *RunResult) bool {
	return r != nil && r.Status == StatusError && IsMaxStepsExceeded(r.Err)
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Cause)
	}
	return e.Message
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func newError(message string, retryable bool, cause error) *Error {
	return &Error{Message: message, Retryable: retryable, Cause: cause}
}
