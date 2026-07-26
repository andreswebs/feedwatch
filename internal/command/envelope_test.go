package command

import (
	"bytes"
	"encoding/json"
	"testing"
)

// envelopeCases lists every stdout result envelope paired with the json keys of
// the collections it owns. One table pins two contract rules for all of them:
// the head leads every result, and no owned collection ever serializes as null.
var envelopeCases = []struct {
	name        string
	zero        any
	collections []string
}{
	{"MigrateStatus", MigrateStatus{}, nil},
	{"MigrateApplied", MigrateApplied{}, nil},
	{"PollResult", PollResult{}, []string{"items", "failures", "renamed"}},
	{"CheckResult", CheckResult{}, []string{"failures"}},
	{"AddResult", AddResult{}, nil},
	{"ListResult", ListResult{}, []string{"feeds"}},
	{"RmResult", RmResult{}, nil},
	{"EnableResult", EnableResult{}, nil},
	{"DisableResult", DisableResult{}, nil},
	{"ItemsResult", ItemsResult{}, []string{"items"}},
	{"ProjectedItemsResult", ProjectedItemsResult{}, []string{"items"}},
	{"PruneResult", PruneResult{}, nil},
	{"DiscoverResult", DiscoverResult{}, []string{"candidates"}},
	{"ImportResult", ImportResult{}, []string{"failed"}},
	{"SchemaResult", SchemaResult{}, []string{"commands", "global_flags"}},
	{"Schema", Schema{}, []string{"args", "flags"}},
	{"VersionResult", VersionResult{}, nil},
}

// TestEnvelopeHeadLeadsEveryResult asserts every result envelope opens with the
// schema_version and ok head keys, in that order.
func TestEnvelopeHeadLeadsEveryResult(t *testing.T) {
	for _, tc := range envelopeCases {
		b, err := json.Marshal(tc.zero)
		if err != nil {
			t.Errorf("%s: marshal: %v", tc.name, err)
			continue
		}
		keys := objectKeys(t, b)
		if len(keys) < 2 || keys[0] != "schema_version" || keys[1] != "ok" {
			t.Errorf("%s: leading keys = %v, want schema_version then ok", tc.name, keys)
		}
	}
}

// TestEnvelopeCollectionsNeverNull asserts every collection an envelope owns
// serializes as [] from the zero value, never null.
func TestEnvelopeCollectionsNeverNull(t *testing.T) {
	for _, tc := range envelopeCases {
		b, err := json.Marshal(tc.zero)
		if err != nil {
			t.Errorf("%s: marshal: %v", tc.name, err)
			continue
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(b, &m); err != nil {
			t.Errorf("%s: unmarshal: %v", tc.name, err)
			continue
		}
		for _, key := range tc.collections {
			if got := string(m[key]); got != "[]" {
				t.Errorf("%s: %q = %s, want []", tc.name, key, got)
			}
		}
	}
}

// objectKeys returns the top-level keys of a JSON object in serialization order.
func objectKeys(t *testing.T, b []byte) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(b))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		t.Fatalf("not a JSON object: %s (%v)", b, err)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatalf("read key: %v", err)
		}
		keys = append(keys, tok.(string))
		skipValue(t, dec)
	}
	return keys
}

// skipValue consumes the next JSON value from dec, descending through nested
// objects and arrays so the caller resumes at the following key.
func skipValue(t *testing.T, dec *json.Decoder) {
	t.Helper()
	tok, err := dec.Token()
	if err != nil {
		t.Fatalf("read value: %v", err)
	}
	d, ok := tok.(json.Delim)
	if !ok || (d != '{' && d != '[') {
		return
	}
	for depth := 1; depth > 0; {
		tt, err := dec.Token()
		if err != nil {
			t.Fatalf("descend value: %v", err)
		}
		if dd, ok := tt.(json.Delim); ok {
			if dd == '{' || dd == '[' {
				depth++
			} else {
				depth--
			}
		}
	}
}
