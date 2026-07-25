package cli

// exitError carries only a process exit code. An action writes its result
// envelope to stdout and then returns exitError to request a specific code
// (2 when all targeted feeds failed, 3 when some did) without emitting anything
// further: its empty message and Exit string keep the boundary from writing a
// stderr error object for what is a normal, reported outcome.
type exitError struct{ code int }

func (e exitError) Error() string { return "" }

// ExitCode reports the requested process exit code, satisfying cli.ExitCoder.
func (e exitError) ExitCode() int { return e.code }

// Exit returns the empty string so the framework prints nothing; the code is
// what matters.
func (e exitError) Exit() string { return "" }
