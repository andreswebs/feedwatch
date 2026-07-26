package output

import (
	"encoding/json"
	"errors"
	"io"

	"github.com/andreswebs/feedwatch/internal/terr"
)

// ExitCodeFor maps a whole-invocation error to a process exit code per
// docs/adr/0001-exit-code-taxonomy.md. It discovers the coded error at the
// boundary with errors.As against terr.Coded and returns its exit code; usage
// 64, schema-too-new 65, store-unavailable 69, config 78, and internal 70.
// Anything unclassified falls back to 70 (EX_SOFTWARE), so a missing
// classification surfaces loudly as an internal error rather than a silent 0.
// The boundary checks for a nil error first, so this is never called with nil.
func ExitCodeFor(err error) int {
	var coded terr.Coded
	if errors.As(err, &coded) {
		return coded.ExitCode()
	}
	return 70
}

// SchemaVersion is the version of the output contract, bumped on breaking
// shape changes to any result envelope (docs/adr/0005-output-contract.md).
const SchemaVersion = 1

// Head opens every result envelope: schema_version identifies the output
// contract version and ok reports whether the invocation succeeded. Embed it as
// the first field of each command's envelope struct so the two keys lead every
// result on stdout.
type Head struct {
	SchemaVersion int  `json:"schema_version"`
	OK            bool `json:"ok"`
}

// OKHead returns the head for a successful result: the current schema version
// and ok true.
func OKHead() Head { return Head{SchemaVersion: SchemaVersion, OK: true} }

// WriteJSON writes v as compact, newline-terminated JSON to w. It is the single
// stdout writer for every command's result envelope; the trailing newline keeps
// streamed output line-delimited for tools like jq.
func WriteJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	return enc.Encode(v)
}

// errorEnvelope is the ADR 0005 stderr error envelope: the schema head, ok
// always false, and the structured error object. It is the sole stderr shape
// for a whole-invocation failure.
type errorEnvelope struct {
	SchemaVersion int         `json:"schema_version"`
	OK            bool        `json:"ok"`
	Error         errorDetail `json:"error"`
}

// errorDetail is the ADR 0005 error object: a stable machine code, a human
// message, and an optional remediation hint and per-instance details. hint and
// details are omitted when empty so a terse failure stays terse.
type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
	Details any    `json:"details,omitempty"`
}

// EmitError writes a whole-invocation failure to w as a single
// newline-terminated ADR 0005 error envelope. The code and hint come from the
// error's terr.Coded classification, the details from terr.Detailed; an
// unclassified error renders as internal_error so a missing classification
// stays visible instead of being silently mislabeled. The write is best-effort:
// a failure to report an error on stderr is unrecoverable and is never
// escalated over the error it describes.
func EmitError(w io.Writer, err error) {
	env := errorEnvelope{
		SchemaVersion: SchemaVersion,
		Error:         errorDetail{Code: "internal_error", Message: errorMessage(err)},
	}

	var coded terr.Coded
	if errors.As(err, &coded) {
		env.Error.Code = coded.Code()
		env.Error.Hint = coded.Hint()
	}
	var detailed terr.Detailed
	if errors.As(err, &detailed) {
		env.Error.Details = detailed.ErrorDetails()
	}

	data, merr := json.Marshal(env)
	if merr != nil {
		// Unmarshalable details: degrade to the envelope without them, which
		// cannot fail to marshal.
		env.Error.Details = nil
		data, _ = json.Marshal(env)
	}
	_, _ = w.Write(append(data, '\n'))
}

// warningEnvelope is a non-fatal, machine-readable advisory written to stderr.
// It carries level "warning" instead of an ok field, so a consumer can tell it
// apart from the error envelope unambiguously, and it never changes the exit
// code. Successive envelopes form a valid NDJSON stream, one object per line.
type warningEnvelope struct {
	SchemaVersion int    `json:"schema_version"`
	Level         string `json:"level"`
	Code          string `json:"code"`
	Message       string `json:"message"`
	Hint          string `json:"hint,omitempty"`
	Details       any    `json:"details,omitempty"`
}

// EmitWarning writes a single newline-terminated ADR 0005 warning envelope to w.
// It is the stderr NDJSON warning channel: a non-fatal advisory distinct from a
// log record and from the error envelope, marked level "warning" so a consumer
// can tell it apart. hint and details are omitted when empty. The write is
// best-effort: a warning that fails to marshal is dropped rather than escalated,
// since it never changes the outcome it advises about.
func EmitWarning(w io.Writer, code, message, hint string, details any) {
	env := warningEnvelope{
		SchemaVersion: SchemaVersion,
		Level:         "warning",
		Code:          code,
		Message:       message,
		Hint:          hint,
		Details:       details,
	}
	data, err := json.Marshal(env)
	if err != nil {
		return
	}
	_, _ = w.Write(append(data, '\n'))
}

// detailer is an optional interface for errors that expose a bare human message
// distinct from their prefixed Error() string. FeedError implements it via
// Detail(), so the envelope message stays free of the category/url/status
// prefix that Error() prepends for text output; the structured code and details
// already carry that classification.
type detailer interface{ Detail() string }

// errorMessage resolves the envelope message: the bare detail when the error
// exposes one, otherwise its Error() string.
func errorMessage(err error) string {
	var d detailer
	if errors.As(err, &d) {
		if msg := d.Detail(); msg != "" {
			return msg
		}
	}
	return err.Error()
}
