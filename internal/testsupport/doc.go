// Package testsupport provides the shared test harness used across feedwatch's
// packages: an in-memory Store double, programmable Fetcher and Parser doubles,
// a fixed Clock, an httptest server with conditional-GET behavior and hit
// counters, and a fixture corpus of valid, malformed, and oddly-encoded feeds.
//
// It is imported only by test files and has no production dependents. The
// doubles satisfy the same interfaces as the real implementations, so command
// tests written against them exercise consumer-shaped code paths without SQL or
// network dependencies.
package testsupport
