package command

import (
	"fmt"
	"io"
	"runtime"
	"runtime/debug"

	"github.com/andreswebs/feedwatch/internal/output"
)

// --version is a plain flag owned by the tool, not the framework's version flag
// (HideVersion is set on the root), so no framework VersionPrinter global is
// mutated (ADR 0003). The Before hook detects the flag and calls writeVersion,
// which emits the JSON {version, commit, go} contract or a human line under
// --format text.

// VersionResult is the --version stdout envelope: the tool version, the VCS
// commit stamped into the binary at build, and the Go toolchain. It is a JSON
// result on stdout like any other command's, so it opens with the head.
type VersionResult struct {
	output.Head
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Go      string `json:"go"`
}

func writeVersion(w io.Writer, format, version string) error {
	v := VersionResult{
		Head:    output.OKHead(),
		Version: version,
		Commit:  vcsRevision(),
		Go:      runtime.Version(),
	}

	if format == "text" {
		_, err := fmt.Fprintf(w, "feedwatch %s (%s) %s\n", v.Version, v.Commit, v.Go)
		return err
	}
	return output.WriteJSON(w, v)
}

// vcsRevision returns the VCS commit stamped into the binary by the Go
// toolchain, or the empty string when build info is unavailable (for example
// under `go test` or a VCS-less build).
func vcsRevision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			return s.Value
		}
	}
	return ""
}
