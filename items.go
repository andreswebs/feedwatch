package feedwatch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/andreswebs/feedwatch/core"
)

// ItemsRequest is the filter, projection, sort, and pagination for a query over
// stored item history.
//
// The filter axis and the order axis are independent: TimeField chooses which
// time the Since/Until window matches, while Order chooses which time the
// results are sorted by, so a caller can window on fetch time but sort by
// publication time.
//
// Every filter composes as an intersection: Tags narrows to the feeds carrying
// a lane, and naming both Feeds and Tags selects the named feeds that are also
// in the lane rather than the union of the two. Items carry no tags of their
// own; a lane is a property of the subscription.
//
// Fields enumerates the projectable item field names in its usage text, which a
// struct tag cannot do because it is a compile-time constant; a frontend appends
// the enumeration from core.ItemFieldNames itself.
type ItemsRequest struct {
	Feeds     []string `flag:"feed" usage:"feed url or alias to query (repeatable); all feeds when omitted"`
	Tags      []string `flag:"tag" usage:"tag to filter by (repeatable); all feeds when omitted"`
	Match     string   `flag:"match" default:"all" usage:"multi-tag semantics: 'all' (default) or 'any'"`
	Since     string   `flag:"since" usage:"lower time bound: RFC3339 or relative such as 24h or 7d"`
	Until     string   `flag:"until" usage:"upper time bound: RFC3339 or relative such as 24h or 7d"`
	Limit     int      `flag:"limit" usage:"maximum items to return; 0 returns all"`
	Offset    int      `flag:"offset" usage:"items to skip before returning results"`
	Order     string   `flag:"order" default:"published desc" usage:"sort: 'published|fetched asc|desc'"`
	TimeField string   `flag:"time-field" default:"published" usage:"axis for --since/--until: 'published' or 'fetched'"`
	Contains  string   `flag:"contains" usage:"substring matched over title and content"`
	Fields    []string `flag:"fields" usage:"project to a subset of item fields; full item when omitted"`
}

// Validate reports whether the request's field names, time bounds, order, and
// time axis are usable, returning a usage-category error naming the first
// problem. It resolves the request against a fixed instant and discards the
// result, so the parsing rules are stated once in query and validation cannot
// drift from resolution.
func (r ItemsRequest) Validate() error {
	_, err := r.query(time.Unix(0, 0))
	return err
}

// query resolves the request into a core.ItemQuery, interpreting relative time
// bounds against now.
func (r ItemsRequest) query(now time.Time) (core.ItemQuery, error) {
	if err := validateItemFields(r.Fields); err != nil {
		return core.ItemQuery{}, err
	}

	filter, err := tagFilter(r.Tags, r.Match)
	if err != nil {
		return core.ItemQuery{}, err
	}

	q := core.ItemQuery{
		Feeds:    r.Feeds,
		Tags:     filter.Tags,
		Match:    filter.Match,
		Contains: r.Contains,
		Limit:    r.Limit,
		Offset:   r.Offset,
		Fields:   r.Fields,
	}

	if r.Since != "" {
		t, err := parseTimeRef(r.Since, now)
		if err != nil {
			return core.ItemQuery{}, usageErr("--since: " + err.Error())
		}
		q.Since = &t
	}
	if r.Until != "" {
		t, err := parseTimeRef(r.Until, now)
		if err != nil {
			return core.ItemQuery{}, usageErr("--until: " + err.Error())
		}
		q.Until = &t
	}

	order, err := parseItemOrder(r.Order)
	if err != nil {
		return core.ItemQuery{}, err
	}
	q.Order = order

	switch r.TimeField {
	case "", "published":
		q.TimeField = "published"
	case "fetched":
		q.TimeField = "fetched"
	default:
		return core.ItemQuery{}, usageErr("--time-field must be 'published' or 'fetched', got " + strconv.Quote(r.TimeField))
	}

	return q, nil
}

// validateItemFields rejects a projection naming an unknown item field. It is
// shared by every command that projects items, so they accept the same names.
func validateItemFields(fields []string) error {
	for _, f := range fields {
		if f == "feed_url" { // always-on identity field: naming it is a no-op
			continue
		}
		if !core.ValidItemFields[f] {
			return usageErr(unknownFieldMessage(f))
		}
	}
	return nil
}

// Envelope selects the shape the request asked for: the projected envelope when
// Fields is set, otherwise the full one. It is the single place that choice is
// expressed, so no frontend inspects Fields itself.
func (r ItemsRequest) Envelope(res ItemsResult) any {
	if len(r.Fields) == 0 {
		return res
	}
	return res.Project(r.Fields)
}

// ItemsResult is the items result envelope for a full (unprojected) query: the
// matched item history in query order. OmittedNoDate, present only when nonzero,
// counts items a publication-axis date window excluded for a null publication
// time.
type ItemsResult struct {
	Head
	Items         []core.Item `json:"items"`
	OmittedNoDate int         `json:"omitted_no_date,omitempty"`
}

// MarshalJSON coalesces items so it always serializes as [] rather than null.
func (r ItemsResult) MarshalJSON() ([]byte, error) {
	type alias ItemsResult
	a := alias(r)
	if a.Items == nil {
		a.Items = []core.Item{}
	}
	return json.Marshal(a)
}

// Project narrows the result to the requested fields, mirroring the JSON
// projection of the items use case. The always-on feed_url identity field is
// emitted regardless of whether it was requested.
func (r ItemsResult) Project(fields []string) ProjectedItemsResult {
	return ProjectedItemsResult{
		Head:          r.Head,
		Items:         projectItems(r.Items, fields),
		OmittedNoDate: r.OmittedNoDate,
		fields:        fields,
	}
}

// projectItems narrows each item to the requested fields plus feed_url.
func projectItems(items []core.Item, fields []string) []map[string]any {
	projected := make([]map[string]any, len(items))
	for i, it := range items {
		projected[i] = core.ProjectItem(it, fields)
	}
	return projected
}

// ProjectedItemsResult is the items result envelope when a projection narrows
// the output to a subset of fields. Each item is a map of feed_url plus the
// requested fields; fields records the projection order so text rendering can
// mirror it. OmittedNoDate carries the same publication-axis exclusion count,
// regardless of projection.
type ProjectedItemsResult struct {
	Head
	Items         []map[string]any `json:"items" jsonschema:"opaque"`
	OmittedNoDate int              `json:"omitted_no_date,omitempty"`
	fields        []string
}

// MarshalJSON coalesces items so it always serializes as [] rather than null.
func (r ProjectedItemsResult) MarshalJSON() ([]byte, error) {
	type alias ProjectedItemsResult
	a := alias(r)
	if a.Items == nil {
		a.Items = []map[string]any{}
	}
	return json.Marshal(a)
}

// Items queries stored item history. It always returns the full result; a caller
// that asked for a projection renders it through ItemsRequest.Envelope, so the
// method's signature stays concrete and an embedder is never handed an interface
// to assert on.
func (a *App) Items(ctx context.Context, req ItemsRequest) (ItemsResult, error) {
	q, err := req.query(a.clock())
	if err != nil {
		return ItemsResult{}, err
	}
	st, err := a.resolveStore(ctx)
	if err != nil {
		return ItemsResult{}, err
	}

	qr, err := st.QueryItems(ctx, q)
	if err != nil {
		return ItemsResult{}, err
	}
	return ItemsResult{Head: OKHead(), Items: qr.Items, OmittedNoDate: qr.OmittedNoDate}, nil
}

// parseItemOrder parses an "<field> <direction>" specifier such as
// "published desc". The field is "published" or "fetched"; the direction is
// "asc" or "desc" and defaults to descending when omitted.
func parseItemOrder(spec string) (core.ItemOrder, error) {
	fields := strings.Fields(spec)
	if len(fields) == 0 {
		return core.ItemOrder{Field: "published", Desc: true}, nil
	}
	if len(fields) > 2 {
		return core.ItemOrder{}, usageErr("--order: want '<published|fetched> [asc|desc]', got " + strconv.Quote(spec))
	}

	field := fields[0]
	if field != "published" && field != "fetched" {
		return core.ItemOrder{}, usageErr("--order field must be 'published' or 'fetched', got " + strconv.Quote(field))
	}

	desc := true
	if len(fields) == 2 {
		switch fields[1] {
		case "asc":
			desc = false
		case "desc":
			desc = true
		default:
			return core.ItemOrder{}, usageErr("--order direction must be 'asc' or 'desc', got " + strconv.Quote(fields[1]))
		}
	}
	return core.ItemOrder{Field: field, Desc: desc}, nil
}

// parseTimeRef resolves a time bound that is either an absolute RFC3339 timestamp
// or a duration relative to now (such as 24h or 7d, which select items within
// that span before now). Relative values support the Go duration units plus 'd'
// (days) and 'w' (weeks).
func parseTimeRef(s string, now time.Time) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	if d, err := parseRelativeDuration(s); err == nil {
		return now.Add(-d), nil
	}
	return time.Time{}, fmt.Errorf("invalid time %s: want RFC3339 or relative such as 24h or 7d", strconv.Quote(s))
}

// parseRelativeDuration parses a Go duration, additionally accepting a single
// trailing 'd' (days) or 'w' (weeks) unit that time.ParseDuration rejects.
func parseRelativeDuration(s string) (time.Duration, error) {
	if d, err := time.ParseDuration(s); err == nil {
		return d, nil
	}
	if len(s) < 2 {
		return 0, fmt.Errorf("invalid duration %s", strconv.Quote(s))
	}
	unit := s[len(s)-1]
	hoursPerUnit := map[byte]float64{'d': 24, 'w': 24 * 7}[unit]
	if hoursPerUnit == 0 {
		return 0, fmt.Errorf("invalid duration %s", strconv.Quote(s))
	}
	n, err := strconv.ParseFloat(s[:len(s)-1], 64)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %s", strconv.Quote(s))
	}
	return time.Duration(n * hoursPerUnit * float64(time.Hour)), nil
}

// RenderText is the optional human-text projection of the same values: the
// matched items as an aligned table, one row per item with its published time,
// feed, title, and link. A frontend may ignore it and read the struct fields
// directly. A dash stands in for an absent published time or title so every
// column is present.
func (r ItemsResult) RenderText(w io.Writer, _ bool) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "PUBLISHED\tFEED\tTITLE\tLINK"); err != nil {
		return err
	}
	for _, it := range r.Items {
		published := "-"
		if it.PublishedAt != nil {
			published = it.PublishedAt.Format(time.RFC3339)
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
			published, it.FeedURL, dashIfEmpty(it.Title), dashIfEmpty(it.Link)); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// RenderText is the optional human-text projection of the same values: a table
// whose columns are feed_url followed by the requested fields in order,
// mirroring the JSON projection. A frontend may ignore it and read the struct
// fields directly. A dash stands in for an absent or empty value so every column
// is present.
func (r ProjectedItemsResult) RenderText(w io.Writer, _ bool) error {
	cols := append([]string{"feed_url"}, r.fields...)

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	header := make([]string, len(cols))
	for i, c := range cols {
		header[i] = strings.ToUpper(c)
	}
	if _, err := fmt.Fprintln(tw, strings.Join(header, "\t")); err != nil {
		return err
	}
	for _, row := range r.Items {
		cells := make([]string, len(cols))
		for i, c := range cols {
			cells[i] = dashIfEmpty(projectedCell(row[c]))
		}
		if _, err := fmt.Fprintln(tw, strings.Join(cells, "\t")); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// projectedCell formats a projected field value for the text table.
func projectedCell(v any) string {
	switch val := v.(type) {
	case nil:
		return ""
	case string:
		return val
	case *time.Time:
		if val == nil {
			return ""
		}
		return val.Format(time.RFC3339)
	case []string:
		return strings.Join(val, ", ")
	default:
		return fmt.Sprintf("%v", val)
	}
}
