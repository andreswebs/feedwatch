package parse_test

import (
	"testing"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/parse"
)

func TestDedupKeyPrecedence(t *testing.T) {
	tests := []struct {
		name string
		item core.Item
		want string
	}{
		{
			name: "guid wins over link and title",
			item: core.Item{GUID: "urn:uuid:1234", Link: "https://blog.example/post", Title: "A Post"},
			want: "urn:uuid:1234",
		},
		{
			name: "link used when guid absent",
			item: core.Item{Link: "https://blog.example/post", Title: "A Post"},
			want: "https://blog.example/post",
		},
		{
			name: "title used when guid and link absent",
			item: core.Item{Title: "A Post"},
			want: "A Post",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parse.DedupKey(tt.item); got != tt.want {
				t.Fatalf("DedupKey = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDedupKeyNeverEmpty(t *testing.T) {
	got := parse.DedupKey(core.Item{})
	if got == "" {
		t.Fatal("DedupKey returned empty for an item with no GUID, link, or title")
	}
}

func TestDedupKeyIsDeterministic(t *testing.T) {
	items := []core.Item{
		{GUID: "g", Link: "https://blog.example/p", Title: "T"},
		{}, // exercises the hashed last-resort path
	}
	for _, it := range items {
		first := parse.DedupKey(it)
		for i := 0; i < 3; i++ {
			if again := parse.DedupKey(it); again != first {
				t.Fatalf("DedupKey not deterministic: %q != %q", again, first)
			}
		}
	}
}
