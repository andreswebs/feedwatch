// Package version is the single source of truth for the feedwatch build
// version, resolving a link-time override, the embedded build info, and a
// development fallback in that order.
package version

import "runtime/debug"

// Override is set at link time via
// -ldflags="-X github.com/andreswebs/feedwatch/internal/version.Override=...".
// It is empty for bare `go build` and `go test`.
var Override = ""

// Current returns the build version using a three-tier fallback: the link-time
// Override, then debug.ReadBuildInfo().Main.Version (populated by
// `go install ...@vX.Y.Z`), then "dev".
func Current() string {
	if Override != "" {
		return Override
	}
	// BuildInfo.Main.Version is populated by `go install ...@vX.Y.Z` but
	// reports "(devel)" for bare `go build`.
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}
