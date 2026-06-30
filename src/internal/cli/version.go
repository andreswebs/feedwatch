package cli

import (
	"fmt"
	"io"
	"runtime"
	"runtime/debug"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch/internal/output"
)

// installVersionPrinter overrides the framework's global VersionPrinter so
// --version emits the JSON {version, commit, go} contract (or a human line under
// --format text). VersionPrinter is package-global framework state, in the same
// controlled category as OsExiter and ErrWriter; it is set from the single
// NewRootCommand construction point. The version string is captured here while
// commit and the Go toolchain are read from the embedded build info at print
// time.
func installVersionPrinter(version string) {
	cliv3.VersionPrinter = func(cmd *cliv3.Command) {
		_ = writeVersion(cmd.Root().Writer, cmd.String("format"), version)
	}
}

func writeVersion(w io.Writer, format, version string) error {
	v := struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
		Go      string `json:"go"`
	}{
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
