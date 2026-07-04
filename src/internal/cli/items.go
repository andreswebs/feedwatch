package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch/internal/core"
)

// ItemsResult is the items stdout envelope for a full (unprojected) query: the
// matched item history in query order. OmittedNoDate, present only when nonzero,
// counts items a publication-axis date window excluded for a null publication
// time.
type ItemsResult struct {
	Items         []core.Item `json:"items"`
	OmittedNoDate int         `json:"omitted_no_date,omitempty"`
}

// ProjectedItemsResult is the items stdout envelope when --fields narrows the
// output to a subset. Each item is a map of feed_url plus the requested fields;
// fields records the projection order so text rendering can mirror it.
// OmittedNoDate carries the same publication-axis exclusion count, regardless of
// projection.
type ProjectedItemsResult struct {
	Items         []map[string]any `json:"items" jsonschema:"opaque"`
	OmittedNoDate int              `json:"omitted_no_date,omitempty"`
	fields        []string
}

// itemsCommand registers the items subcommand: query stored item history with
// feed, time-window, substring, ordering, pagination, and projection filters.
func (d Deps) itemsCommand() *cliv3.Command {
	return &cliv3.Command{
		Name:  "items",
		Usage: "query stored item history with filters, ordering, and pagination",
		Flags: []cliv3.Flag{
			&cliv3.StringSliceFlag{Name: "feed", Usage: "feed url or alias to query (repeatable); all feeds when omitted"},
			&cliv3.StringFlag{Name: "since", Usage: "lower time bound: RFC3339 or relative such as 24h or 7d"},
			&cliv3.StringFlag{Name: "until", Usage: "upper time bound: RFC3339 or relative such as 24h or 7d"},
			&cliv3.IntFlag{Name: "limit", Usage: "maximum items to return; 0 returns all"},
			&cliv3.IntFlag{Name: "offset", Usage: "items to skip before returning results"},
			&cliv3.StringFlag{Name: "order", Value: "published desc", Usage: "sort: 'published|fetched asc|desc'"},
			&cliv3.StringFlag{Name: "time-field", Value: "published", Usage: "axis for --since/--until: 'published' or 'fetched'"},
			&cliv3.StringFlag{Name: "contains", Usage: "substring matched over title and content"},
			&cliv3.StringSliceFlag{Name: "fields", Usage: "project to a subset of item fields (" + strings.Join(core.ItemFieldNames(), ", ") + "); full item when omitted"},
		},
		Action: d.itemsAction,
	}
}

// itemsAction translates the flags into a core.ItemQuery, queries the store, and
// writes the result envelope. Unparseable --since/--until/--order values are
// usage errors (exit 1, empty stdout); a store failure propagates to the boundary.
func (d Deps) itemsAction(ctx context.Context, cmd *cliv3.Command) error {
	cfg := configFrom(ctx)
	r := rendererFrom(ctx)

	now := orSystemClock(d.Clock)()
	q, err := buildItemQuery(cmd, now)
	if err != nil {
		return err
	}

	rs := newResolver(d, cfg)
	defer rs.Close()
	st, err := rs.Store(ctx)
	if err != nil {
		return err
	}

	qr, err := st.QueryItems(ctx, q)
	if err != nil {
		return err
	}
	items := qr.Items

	if qr.OmittedNoDate > 0 {
		loggerFrom(ctx).InfoContext(ctx, "excluded items with no publication date",
			"count", qr.OmittedNoDate, "axis", "published")
	}

	if len(q.Fields) > 0 {
		projected := make([]map[string]any, len(items))
		for i, it := range items {
			projected[i] = core.ProjectItem(it, q.Fields)
		}
		return r.Result(ProjectedItemsResult{
			Items: projected, OmittedNoDate: qr.OmittedNoDate, fields: q.Fields,
		})
	}
	if items == nil {
		items = []core.Item{}
	}
	return r.Result(ItemsResult{Items: items, OmittedNoDate: qr.OmittedNoDate})
}

// buildItemQuery assembles a core.ItemQuery from the command flags, resolving
// the time bounds relative to now and parsing the order specifier.
func buildItemQuery(cmd *cliv3.Command, now time.Time) (core.ItemQuery, error) {
	fields := cmd.StringSlice("fields")
	for _, f := range fields {
		if f == "feed_url" { // always-on identity field: naming it is a no-op
			continue
		}
		if !core.ValidItemFields[f] {
			return core.ItemQuery{}, usageErr(unknownFieldMessage(f))
		}
	}

	q := core.ItemQuery{
		Feeds:    cmd.StringSlice("feed"),
		Contains: cmd.String("contains"),
		Limit:    cmd.Int("limit"),
		Offset:   cmd.Int("offset"),
		Fields:   fields,
	}

	if s := cmd.String("since"); s != "" {
		t, err := parseTimeRef(s, now)
		if err != nil {
			return core.ItemQuery{}, usageErr("--since: " + err.Error())
		}
		q.Since = &t
	}
	if s := cmd.String("until"); s != "" {
		t, err := parseTimeRef(s, now)
		if err != nil {
			return core.ItemQuery{}, usageErr("--until: " + err.Error())
		}
		q.Until = &t
	}

	order, err := parseItemOrder(cmd.String("order"))
	if err != nil {
		return core.ItemQuery{}, err
	}
	q.Order = order

	switch tf := cmd.String("time-field"); tf {
	case "", "published":
		q.TimeField = "published"
	case "fetched":
		q.TimeField = "fetched"
	default:
		return core.ItemQuery{}, usageErr("--time-field must be 'published' or 'fetched', got " + strconv.Quote(tf))
	}

	return q, nil
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

// usageErr builds a whole-invocation usage error that the boundary maps to exit 1.
func usageErr(msg string) error {
	return &core.FeedError{
		Category: core.CatUsage,
		Message:  msg,
		Err:      core.ErrUsage,
	}
}

// RenderText writes the full matched items as an aligned table under
// --format text, one row per item with its published time, feed, title, and
// link. A dash stands in for an absent published time or title so every column
// is present.
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

// RenderText writes a table whose columns are feed_url followed by the requested
// fields in order, mirroring the JSON projection under --format text. A dash
// stands in for an absent or empty value so every column is present.
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
