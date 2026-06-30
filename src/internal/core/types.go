package core

import "time"

// FeedStatus is the lifecycle state of a subscription.
type FeedStatus string

const (
	// FeedActive marks a feed that poll will fetch when due.
	FeedActive FeedStatus = "active"
	// FeedDisabled marks a feed that poll skips until re-enabled.
	FeedDisabled FeedStatus = "disabled"
)

// Feed is a subscription. The URL is the canonical identity and dedup key.
type Feed struct {
	URL          string        // canonical identity
	Alias        string        // optional, unique when set
	Interval     time.Duration // 0 means use the configured default
	Status       FeedStatus
	ETag         string // conditional-GET validator
	LastModified string // conditional-GET validator
	FailureCount int
	LastError    string
	LastErrorAt  *time.Time
	LastFetchAt  *time.Time
	NextDueAt    *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Enclosure is a media attachment advertised by a feed item.
type Enclosure struct {
	URL    string `json:"url"`
	Type   string `json:"type"`
	Length int64  `json:"length,omitempty"`
}

// Item is a normalized feed entry. Its JSON tags define the agent-facing shape.
type Item struct {
	FeedURL         string      `json:"feed_url"`
	DedupKey        string      `json:"-"`            // internal identity
	GUID            string      `json:"id,omitempty"` // raw guid / atom id
	Title           string      `json:"title"`
	Link            string      `json:"link"`
	Summary         string      `json:"summary,omitempty"`
	ContentHTML     string      `json:"content_html,omitempty"`
	ContentText     string      `json:"content_text,omitempty"`
	ContentMIMEType string      `json:"content_mime_type,omitempty"`
	BaseURL         string      `json:"base_url,omitempty"`
	Author          string      `json:"author,omitempty"`
	Categories      []string    `json:"categories,omitempty"`
	Enclosures      []Enclosure `json:"enclosures,omitempty"`
	PublishedAt     *time.Time  `json:"published_at"` // null when unparseable
	UpdatedAt       *time.Time  `json:"updated_at,omitempty"`
	FetchedAt       time.Time   `json:"-"`
	Seen            bool        `json:"-"`
}
