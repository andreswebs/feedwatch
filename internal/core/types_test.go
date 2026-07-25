package core_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/andreswebs/feedwatch/internal/core"
)

// Behavior 1: Item marshals with the documented field names; a nil
// PublishedAt serializes as "published_at": null rather than being omitted.
func TestItemMarshalsDocumentedShape(t *testing.T) {
	pub := time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC)
	item := core.Item{
		FeedURL:         "https://blog.example/feed.xml",
		DedupKey:        "internal-key",
		GUID:            "guid-123",
		Title:           "Hello",
		Link:            "https://blog.example/hello",
		Summary:         "short description",
		ContentHTML:     "<p>full</p>",
		ContentText:     "full",
		ContentMIMEType: "text/html",
		BaseURL:         "https://blog.example/",
		Author:          "Jane",
		Categories:      []string{"go"},
		Enclosures:      []core.Enclosure{{URL: "https://x/a.mp3", Type: "audio/mpeg", Length: 100}},
		PublishedAt:     &pub,
		UpdatedAt:       &pub,
		FetchedAt:       pub,
		Seen:            true,
	}

	out := marshalToMap(t, item)

	want := map[string]any{
		"feed_url":          "https://blog.example/feed.xml",
		"id":                "guid-123",
		"title":             "Hello",
		"link":              "https://blog.example/hello",
		"summary":           "short description",
		"content_html":      "<p>full</p>",
		"content_text":      "full",
		"content_mime_type": "text/html",
		"base_url":          "https://blog.example/",
		"author":            "Jane",
		"published_at":      "2026-06-27T10:00:00Z",
		"updated_at":        "2026-06-27T10:00:00Z",
		"fetched_at":        "2026-06-27T10:00:00Z",
	}
	for k, v := range want {
		if got, ok := out[k]; !ok || got != v {
			t.Errorf("field %q = %v (present=%v), want %v", k, got, ok, v)
		}
	}

	for _, internal := range []string{"DedupKey", "dedup_key", "Seen", "seen"} {
		if _, ok := out[internal]; ok {
			t.Errorf("internal field %q must not be serialized", internal)
		}
	}
}

func TestItemNilPublishedAtSerializesNull(t *testing.T) {
	item := core.Item{Title: "no date", PublishedAt: nil}

	b, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got, ok := raw["published_at"]
	if !ok {
		t.Fatal(`"published_at" must be present even when nil`)
	}
	if string(got) != "null" {
		t.Errorf(`"published_at" = %s, want null`, got)
	}
}

// Behavior 2: Enclosure with zero Length omits length; non-zero includes it.
func TestEnclosureLengthOmitempty(t *testing.T) {
	zero := marshalToMap(t, core.Enclosure{URL: "u", Type: "audio/mpeg"})
	if _, ok := zero["length"]; ok {
		t.Error("zero Length must omit the length field")
	}

	nonzero := marshalToMap(t, core.Enclosure{URL: "u", Type: "audio/mpeg", Length: 5768960})
	if got, ok := nonzero["length"]; !ok || got != float64(5768960) {
		t.Errorf("non-zero Length = %v (present=%v), want 5768960", got, ok)
	}
}

// Behavior 3: FeedStatus constants marshal as "active" / "disabled".
func TestFeedStatusValues(t *testing.T) {
	cases := map[core.FeedStatus]string{
		core.FeedActive:   `"active"`,
		core.FeedDisabled: `"disabled"`,
	}
	for status, want := range cases {
		b, err := json.Marshal(status)
		if err != nil {
			t.Fatalf("marshal %v: %v", status, err)
		}
		if string(b) != want {
			t.Errorf("status %v marshaled as %s, want %s", status, b, want)
		}
	}
}

func marshalToMap(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return m
}
