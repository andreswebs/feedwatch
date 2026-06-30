package discover

import (
	"context"
	"mime"
	"net/url"
	"strings"

	"golang.org/x/net/html"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/fetch"
	"github.com/andreswebs/feedwatch/internal/parse"
)

// Source labels how a candidate feed was found.
const (
	SourceAutodiscovery = "autodiscovery"
	SourceProbe         = "probe"
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

// Deps are discover's collaborators: an HTTP fetcher and a feed parser. Discovery
// is read-only and never touches a store.
type Deps struct {
	Fetcher fetch.Fetcher
	Parser  parse.Parser
}

// probePaths are the common feed locations probed against the page's origin when
// autodiscovery finds nothing, in priority order.
var probePaths = []string{"/feed", "/rss", "/feed.xml", "/atom.xml", "/index.xml"}

// feedLinkTypes are the rel="alternate" link MIME types that declare a feed, used
// to tell a feed link from an i18n or print alternate.
var feedLinkTypes = map[string]bool{
	"application/rss+xml":   true,
	"application/atom+xml":  true,
	"application/feed+json": true,
	"application/json":      true,
	"application/rdf+xml":   true,
}

// Discover lists candidate feeds for pageURL: first the feeds the page declares
// via rel="alternate" autodiscovery links, then a bounded probe of common feed
// paths against the page's origin. Every candidate is fetched and parse-validated,
// so non-feeds (HTML pages, sitemaps) are dropped. The returned slice is never
// nil and is ordered autodiscovery first, then probe. Discovery is read-only.
func Discover(ctx context.Context, d Deps, pageURL string) ([]Candidate, error) {
	candidates := make([]Candidate, 0)
	seen := make(map[string]bool)

	page, pageErr := d.Fetcher.Fetch(ctx, core.FetchRequest{URL: pageURL})
	if pageErr == nil && isHTML(page.MIMEType) {
		for _, link := range autodiscoverLinks(page.Body, pageURL) {
			if seen[link.url] {
				continue
			}
			seen[link.url] = true
			if title, _, ok := d.validate(ctx, link.url); ok {
				candidates = append(candidates, Candidate{
					Title:  firstNonEmpty(link.title, title),
					URL:    link.url,
					Type:   link.typ,
					Source: SourceAutodiscovery,
				})
			}
		}
	}

	for _, p := range probePaths {
		probeURL := resolveProbe(pageURL, p)
		if probeURL == "" || seen[probeURL] {
			continue
		}
		seen[probeURL] = true
		if title, mimeType, ok := d.validate(ctx, probeURL); ok {
			candidates = append(candidates, Candidate{
				Title:  title,
				URL:    probeURL,
				Type:   mimeType,
				Source: SourceProbe,
			})
		}
	}

	return candidates, nil
}

// validate fetches a candidate URL and reports whether it is a real feed,
// returning the feed title and response MIME type. A content-type short-circuit
// rejects an HTML page outright (so a homepage returned for an unknown probe path
// is never a candidate); parsing then drops generic XML that is not a feed, such
// as a sitemap. Any fetch or parse failure drops the candidate silently, since
// discovery reports only feeds it could prove.
func (d Deps) validate(ctx context.Context, candidateURL string) (title, mimeType string, ok bool) {
	res, err := d.Fetcher.Fetch(ctx, core.FetchRequest{URL: candidateURL})
	if err != nil {
		return "", "", false
	}
	if isHTML(res.MIMEType) {
		return "", "", false
	}
	pf, err := d.Parser.Parse(ctx, res.Body, candidateURL)
	if err != nil {
		return "", "", false
	}
	return pf.Title, res.MIMEType, true
}

// autodiscoverLink is one rel="alternate" feed link extracted from a page.
type autodiscoverLink struct {
	url   string
	typ   string
	title string
}

// autodiscoverLinks scans an HTML body for <link rel="alternate"> elements whose
// type declares a feed, resolving each href against the page URL. A body that
// does not parse as HTML yields no links.
func autodiscoverLinks(body []byte, pageURL string) []autodiscoverLink {
	base, err := url.Parse(pageURL)
	if err != nil {
		return nil
	}
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return nil
	}

	var links []autodiscoverLink
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "link" {
			if link, ok := feedLinkFrom(n, base); ok {
				links = append(links, link)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return links
}

// feedLinkFrom builds a feed autodiscovery link from a <link> node, reporting
// false unless it carries rel="alternate", a feed-type attribute, and an href
// that resolves against base.
func feedLinkFrom(n *html.Node, base *url.URL) (autodiscoverLink, bool) {
	var rel, typ, href, title string
	for _, a := range n.Attr {
		switch strings.ToLower(a.Key) {
		case "rel":
			rel = a.Val
		case "type":
			typ = strings.ToLower(strings.TrimSpace(a.Val))
		case "href":
			href = a.Val
		case "title":
			title = a.Val
		}
	}
	if !relContains(rel, "alternate") || !feedLinkTypes[typ] || href == "" {
		return autodiscoverLink{}, false
	}
	ref, err := url.Parse(href)
	if err != nil {
		return autodiscoverLink{}, false
	}
	return autodiscoverLink{url: base.ResolveReference(ref).String(), typ: typ, title: title}, true
}

// relContains reports whether a space-separated rel attribute holds the given
// token, case-insensitively.
func relContains(rel, token string) bool {
	for _, f := range strings.Fields(rel) {
		if strings.EqualFold(f, token) {
			return true
		}
	}
	return false
}

// resolveProbe builds an absolute candidate URL from pageURL's origin (scheme and
// host) and an absolute feed path, returning "" when pageURL does not parse.
func resolveProbe(pageURL, path string) string {
	u, err := url.Parse(pageURL)
	if err != nil || u.Host == "" {
		return ""
	}
	origin := &url.URL{Scheme: u.Scheme, Host: u.Host}
	return origin.ResolveReference(&url.URL{Path: path}).String()
}

// isHTML reports whether a Content-Type media type is an HTML document rather
// than a feed.
func isHTML(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mt = strings.TrimSpace(strings.ToLower(contentType))
	}
	return mt == "text/html" || mt == "application/xhtml+xml"
}

// firstNonEmpty returns the first non-empty string of its arguments.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
