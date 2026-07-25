package output

import (
	"encoding/json"
	"io"

	"github.com/andreswebs/feedwatch/internal/core"
)

// WriteJSON writes v as compact, newline-terminated JSON to w. It is the single
// stdout writer for every command's result envelope; the trailing newline keeps
// streamed output line-delimited for tools like jq.
func WriteJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	return enc.Encode(v)
}

// errorPayload is the stderr JSON shape of a FeedError. Zero-valued status and
// empty feed URL are omitted so a whole-invocation error stays terse, while the
// category and message are always present.
type errorPayload struct {
	Category core.Category `json:"category"`
	FeedURL  string        `json:"feed_url,omitempty"`
	Status   int           `json:"status,omitempty"`
	Message  string        `json:"message"`
}

// payloadFor derives the stderr shape of a FeedError. The message prefers the
// explicit Message and falls back to the wrapped cause, mirroring FeedError's
// own precedence but without the category/url/status prefix that the structured
// fields already carry.
func payloadFor(e *core.FeedError) errorPayload {
	msg := e.Message
	if msg == "" && e.Err != nil {
		msg = e.Err.Error()
	}
	return errorPayload{
		Category: e.Category,
		FeedURL:  e.FeedURL,
		Status:   e.Status,
		Message:  msg,
	}
}

// WriteError writes a single FeedError to w as {"error":{...}}.
func WriteError(w io.Writer, e *core.FeedError) error {
	return WriteJSON(w, map[string]errorPayload{"error": payloadFor(e)})
}

// WriteErrors writes a batch of per-feed failures to w as {"errors":[...]}.
func WriteErrors(w io.Writer, es []*core.FeedError) error {
	ps := make([]errorPayload, len(es))
	for i, e := range es {
		ps[i] = payloadFor(e)
	}
	return WriteJSON(w, map[string][]errorPayload{"errors": ps})
}
