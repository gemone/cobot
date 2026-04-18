package cobot

// CobotError is a structured error with a machine-readable code.
type CobotError struct {
	Code    string
	Message string
	Cause   error
}

func (e *CobotError) Error() string {
	if e.Cause != nil {
		return e.Code + ": " + e.Message + ": " + e.Cause.Error()
	}
	return e.Code + ": " + e.Message
}

func (e *CobotError) Unwrap() error { return e.Cause }

// Sentinel errors.
var (
	ErrProviderNotConfigured = &CobotError{Code: "PROVIDER_NOT_CONFIGURED", Message: "LLM provider not configured"}
	ErrMaxTurnsExceeded      = &CobotError{Code: "MAX_TURNS_EXCEEDED", Message: "max turns exceeded"}
)
