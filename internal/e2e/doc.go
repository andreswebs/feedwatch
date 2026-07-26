// Package e2e holds the conditional exec-based end-to-end suite of ADR 0006. It
// builds the real feedwatch binary once and drives it as a process, and exists
// only for process-level behavior the in-process golden-triple harness cannot
// reach: signal handling and the 128+signum exit codes (SIGINT to 130, SIGTERM
// to 143), where the binary must run as its own process to receive a signal.
//
// Every stream-contract scenario (stdout, stderr, and exit-code goldens) lives
// in the in-process golden harness in internal/command, which drives the
// Run(args, deps) boundary with buffer streams and is framework-blind. This
// suite deliberately carries no goldens.
//
// The suite contains no production code; it exists only for its tests.
package e2e
