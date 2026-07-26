package command

// exitError carries only a process exit code. An action writes its result
// envelope to stdout and then returns exitError to request a specific code
// (2 when all targeted feeds failed, 3 when some did) without emitting anything
// further. Run recognizes it with errors.As and translates it to the requested
// code, emitting nothing: its empty message keeps stderr clean for what is a
// normal, already-reported outcome. It is a plain type, not a framework
// ExitCoder, so the framework never sees it (ADR 0003).
type exitError struct{ code int }

func (e exitError) Error() string { return "" }
