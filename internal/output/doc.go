// Package output renders a result envelope as JSON or text and owns the stderr
// error and warning envelopes, applying color gating for terminal output. The
// envelope head and the result types themselves belong to the library, so every
// frontend renders the same values.
package output
