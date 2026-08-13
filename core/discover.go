package core

// Source labels how a candidate feed was found.
const (
	// SourceAutodiscovery marks a feed the page declared through
	// <link rel="alternate">.
	SourceAutodiscovery = "autodiscovery"
	// SourceProbe marks a feed guessed from a common feed path.
	SourceProbe = "probe"
)

// Candidate is one feed found for a page, validated by parsing. Source tells the
// agent whether the feed was declared by the page (autodiscovery) or guessed
// from a common path (probe).
type Candidate struct {
	Title  string `json:"title,omitempty"`
	URL    string `json:"url"`
	Type   string `json:"type,omitempty"`
	Source string `json:"source"`
}
