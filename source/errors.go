package anvil

import "fmt"

// Error is a startup / transport / config failure before or around a run.
// Distinguish from RunResult.Status == StatusError (run started then failed).
type Error struct {
	Message   string
	Retryable bool
	Cause     error
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
