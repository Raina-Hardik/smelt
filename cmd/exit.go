package cmd

// ExitError carries a specific exit code through cobra's error chain.
// Use exitErr(code, err) to wrap an error with a code; Execute() unwraps it.
//
// Codes:
//
//	0 — success
//	2 — usage / validation error
//	3 — partial failure (some files failed)
//	4 — all failed or cancelled
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string { return e.Err.Error() }
func (e *ExitError) Unwrap() error { return e.Err }

func exitErr(code int, err error) error { return &ExitError{Code: code, Err: err} }
