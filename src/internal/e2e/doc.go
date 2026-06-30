// Package e2e holds the golden-file end-to-end suite. It builds the real
// feedwatch binary and drives it across command sequences against a local feed
// server and a temporary store, diffing normalized JSON stdout/stderr against
// golden files and asserting exit codes. It is the strongest guard that the
// agent-first output contract and exit-code taxonomy hold end-to-end.
//
// The suite contains no production code; it exists only for its tests.
package e2e
