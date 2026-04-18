package agent

// Error is a structured error with a machine-readable code.
type Error struct {
	Code    string
	Message string
	Cause   error
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return e.Code + ": " + e.Message + ": " + e.Cause.Error()
	}
	return e.Code + ": " + e.Message
}

func (e *Error) Unwrap() error { return e.Cause }

var (
	ErrProviderNotConfigured = &Error{Code: "PROVIDER_NOT_CONFIGURED", Message: "LLM provider not configured"}
	ErrMaxTurnsExceeded      = &Error{Code: "MAX_TURNS_EXCEEDED", Message: "max turns exceeded"}
)
